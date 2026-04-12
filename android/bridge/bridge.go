// Package bridge provides Android JNI bindings for cloudflared tunnel functionality.
// This package can be used as a library to create Android apps with Cloudflare tunnel support.
//
// To use this library in your Android project:
//
//  1. Build the shared library:
//     CGO_ENABLED=1 GOOS=android GOARCH=arm64 \
//     CC=$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang \
//     go build -buildmode=c-shared -o libcloudflared-bridge.so
//
//  2. Copy the library and header to your Android project:
//     - libcloudflared-bridge.so -> jniLibs/arm64-v8a/
//     - libcloudflared-bridge.h -> jniLibs/include/
//
//  3. In your Android Kotlin/Java code, load the library and call the native methods:
//
//     object CloudflaredBridge {
//     init {
//     System.loadLibrary("cloudflared-bridge")
//     }
//
//     @JvmStatic private external fun nativeStartTunnel(token: String, proxyPort: Int): String
//     @JvmStatic private external fun nativeStopTunnel()
//     @JvmStatic private external fun nativeIsTunnelRunning(): Int
//     @JvmStatic private external fun nativeGetLastError(): String
//     @JvmStatic private external fun nativeSetLogCallback(callback: LogCallback?)
//
//     interface LogCallback {
//     fun onLog(level: Int, message: String)
//     }
//     }
//
// The bridge exposes functions that map to the JNI naming convention:
// Java_{package}_{class}_native{Function}
//
// For example, with package "com.example.app" and class "CloudflaredBridge":
// - nativeStartTunnel -> Java_com_example_app_CloudflaredBridge_nativeStartTunnel
//
// See the README.md for complete integration instructions.
package main

/*
#include <stdlib.h>
#include <android/log.h>

// Forward declarations for JNI functions
// These will be implemented in the companion C file (bridge_jni.c)

// Log callback typedef
typedef void (*log_callback_t)(int level, const char* msg);

// Global log callback (set from Java/JNI)
static log_callback_t g_log_callback = NULL;

static void set_log_callback(log_callback_t cb) {
    g_log_callback = cb;
}

static void forward_log(int level, const char* msg) {
    if (g_log_callback != NULL) {
        g_log_callback(level, msg);
    } else {
        // Fallback to Android log
        int android_level = ANDROID_LOG_DEBUG;
        switch (level) {
            case 0: case 1: case 2: android_level = ANDROID_LOG_ERROR; break;
            case 3: android_level = ANDROID_LOG_WARN; break;
            case 4: android_level = ANDROID_LOG_INFO; break;
        }
        __android_log_write(android_level, "CloudflaredBridge", msg);
    }
}
*/
import "C"
import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/cloudflare/cloudflared/client"
	cfconfig "github.com/cloudflare/cloudflared/config"
	"github.com/cloudflare/cloudflared/connection"
	"github.com/cloudflare/cloudflared/edgediscovery"
	"github.com/cloudflare/cloudflared/features"
	"github.com/cloudflare/cloudflared/ingress"
	"github.com/cloudflare/cloudflared/ingress/origins"
	"github.com/cloudflare/cloudflared/orchestration"
	"github.com/cloudflare/cloudflared/signal"
	"github.com/cloudflare/cloudflared/supervisor"
	"github.com/cloudflare/cloudflared/tlsconfig"
	"github.com/cloudflare/cloudflared/tunnelrpc/pogs"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

var (
	tunnelMu        sync.Mutex
	tunnelCancel    context.CancelFunc
	tunnelDone      chan struct{}
	tunnelRunning   bool
	tunnelConnected bool
	tunnelLastError string
	logger          zerolog.Logger
)

// tokenPayload mirrors connection.TunnelToken for local JSON parsing.
type tokenPayload struct {
	AccountTag   string    `json:"a"`
	TunnelSecret []byte    `json:"s"`
	TunnelID     uuid.UUID `json:"t"`
	Endpoint     string    `json:"e,omitempty"`
}

// noopMetrics implements origins.Metrics without prometheus dependency.
type noopMetrics struct{}

func (noopMetrics) IncrementDNSUDPRequests() {}
func (noopMetrics) IncrementDNSTCPRequests() {}

//export SetLogCallback
func SetLogCallback(cb unsafe.Pointer) {
	// cb is a function pointer from Java: void (*callback)(int level, const char* msg)
	C.set_log_callback((C.log_callback_t)(cb))
}

func forwardLogToJava(level int, msg string) {
	cmsg := C.CString(msg)
	defer C.free(unsafe.Pointer(cmsg))
	C.forward_log(C.int(level), cmsg)
}

func init() {
	// Initialize logger that forwards to Android/Java
	logger = zerolog.New(&javaLogWriter{}).
		With().
		Str("component", "cloudflared").
		Timestamp().
		Logger()
}

type javaLogWriter struct{}

func (w *javaLogWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	forwardLogToJava(4, string(p)) // 4 = info level
	return len(p), nil
}

func parseToken(tokenStr string) (*connection.TunnelProperties, error) {
	raw, err := base64.StdEncoding.DecodeString(tokenStr)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var tok tokenPayload
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	if tok.AccountTag == "" || tok.TunnelID == uuid.Nil || len(tok.TunnelSecret) == 0 {
		return nil, fmt.Errorf("token missing required fields")
	}
	return &connection.TunnelProperties{
		Credentials: connection.Credentials{
			AccountTag:   tok.AccountTag,
			TunnelSecret: tok.TunnelSecret,
			TunnelID:     tok.TunnelID,
			Endpoint:     tok.Endpoint,
		},
	}, nil
}

// buildEdgeTLSConfigs creates TLS configs for each protocol.
func buildEdgeTLSConfigs() (map[connection.Protocol]*tls.Config, error) {
	configs := make(map[connection.Protocol]*tls.Config, len(connection.ProtocolList))

	for _, p := range connection.ProtocolList {
		tlsSettings := p.TLSSettings()
		if tlsSettings == nil {
			return nil, fmt.Errorf("%s has unknown TLS settings", p)
		}

		cfg, err := tlsconfig.GetConfig(&tlsconfig.TLSParameters{
			ServerName: tlsSettings.ServerName,
		})
		if err != nil {
			return nil, fmt.Errorf("TLS config for %s: %w", p, err)
		}

		if cfg.RootCAs == nil {
			rootCAs, err := x509.SystemCertPool()
			if err != nil {
				rootCAs = x509.NewCertPool()
			}
			cfRootCAs, err := tlsconfig.GetCloudflareRootCA()
			if err != nil {
				return nil, fmt.Errorf("cloudflare root CA: %w", err)
			}
			for _, cert := range cfRootCAs {
				rootCAs.AddCert(cert)
			}
			cfg.RootCAs = rootCAs
		}

		if len(tlsSettings.NextProtos) > 0 {
			cfg.NextProtos = tlsSettings.NextProtos
		}

		configs[p] = cfg
	}

	return configs, nil
}

//export StartTunnel
func StartTunnel(token *C.char, proxyPort C.int) *C.char {
	return StartTunnelWithProtocol(token, proxyPort, nil)
}

//export StartTunnelWithProtocol
func StartTunnelWithProtocol(token *C.char, proxyPort C.int, protocol *C.char) *C.char {
	tokenStr := C.GoString(token)
	if tokenStr == "" {
		return C.CString("token is required")
	}

	protocolStr := connection.AutoSelectFlag
	if protocol != nil {
		protocolStr = C.GoString(protocol)
	}

	err := startTunnelInternal(tokenStr, int(proxyPort), protocolStr)
	if err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

func startTunnelInternal(tokenStr string, proxyPort int, protocol string) error {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()

	if tunnelRunning {
		return fmt.Errorf("tunnel already running")
	}

	namedTunnel, err := parseToken(tokenStr)
	if err != nil {
		return fmt.Errorf("parse token: %w", err)
	}

	logTransport := logger.With().Str("transport", "edge").Logger()

	ctx, cancel := context.WithCancel(context.Background())

	// Observer
	observer := connection.NewObserver(&logger, &logTransport)
	observer.RegisterSink(&tunnelEventSink{})

	// Feature selector
	featureSelector, err := features.NewFeatureSelector(ctx, namedTunnel.Credentials.AccountTag, nil, false, &logger)
	if err != nil {
		cancel()
		return fmt.Errorf("feature selector: %w", err)
	}

	// Client config
	clientConfig, err := client.NewConfig("android-embedded", runtime.GOARCH, featureSelector)
	if err != nil {
		cancel()
		return fmt.Errorf("client config: %w", err)
	}

	logger.Info().Msgf("Connector ID: %s", clientConfig.ConnectorID)

	// Tags
	tags := []pogs.Tag{
		{Name: "ID", Value: clientConfig.ConnectorID.String()},
	}

	// Protocol selector
	protocolSelector, err := connection.NewProtocolSelector(
		protocol,
		namedTunnel.Credentials.AccountTag,
		true,
		false,
		edgediscovery.ProtocolPercentage,
		connection.ResolveTTL,
		&logger,
	)
	if err != nil {
		cancel()
		return fmt.Errorf("protocol selector: %w", err)
	}

	// Edge TLS configs
	edgeTLSConfigs, err := buildEdgeTLSConfigs()
	if err != nil {
		cancel()
		return fmt.Errorf("edge TLS: %w", err)
	}

	// Ingress - single catch-all rule pointing to local proxy
	ingressRules, err := ingress.ParseIngress(&cfconfig.Configuration{
		Ingress: []cfconfig.UnvalidatedIngressRule{
			{Service: fmt.Sprintf("http://127.0.0.1:%d", proxyPort)},
		},
	})
	if err != nil {
		cancel()
		return fmt.Errorf("parse ingress: %w", err)
	}

	// Warp routing
	warpConfig := ingress.NewWarpRoutingConfig(&cfconfig.WarpRoutingConfig{})

	// Origin dialer
	dialer := ingress.NewDialer(warpConfig)
	originDialerService := ingress.NewOriginDialer(ingress.OriginConfig{
		DefaultDialer: dialer,
	}, &logger)

	// DNS resolver service - use public resolvers on Android
	dnsService := origins.NewStaticDNSResolverService(
		[]netip.AddrPort{
			netip.AddrPortFrom(netip.MustParseAddr("1.1.1.1"), 53),
			netip.AddrPortFrom(netip.MustParseAddr("1.0.0.1"), 53),
		},
		origins.NewDNSDialer(),
		&logger,
		noopMetrics{},
	)
	originDialerService.AddReservedService(dnsService, []netip.AddrPort{origins.VirtualDNSServiceAddr})

	// Tunnel config
	tunnelConfig := &supervisor.TunnelConfig{
		ClientConfig:        clientConfig,
		GracePeriod:         30 * time.Second,
		HAConnections:       1,
		Tags:                tags,
		Log:                 &logger,
		LogTransport:        &logTransport,
		Observer:            observer,
		ReportedVersion:     "android-embedded",
		Retries:             5,
		MaxEdgeAddrRetries:  8,
		NamedTunnel:         namedTunnel,
		ProtocolSelector:    protocolSelector,
		EdgeTLSConfigs:      edgeTLSConfigs,
		Region:              namedTunnel.Credentials.Endpoint,
		OriginDNSService:    dnsService,
		OriginDialerService: originDialerService,
		RPCTimeout:          5 * time.Second,
		WriteStreamTimeout:  10 * time.Second,
	}

	// Orchestrator config
	orchConfig := &orchestration.Config{
		Ingress:             &ingressRules,
		WarpRouting:         warpConfig,
		OriginDialerService: originDialerService,
	}

	orchestrator, err := orchestration.NewOrchestrator(ctx, orchConfig, tags, nil, &logger)
	if err != nil {
		cancel()
		return fmt.Errorf("orchestrator: %w", err)
	}

	connectedSignal := signal.New(make(chan struct{}))
	reconnectCh := make(chan supervisor.ReconnectSignal, 1)
	graceShutdownC := make(chan struct{})

	done := make(chan struct{})
	tunnelCancel = cancel
	tunnelDone = done
	tunnelRunning = true
	tunnelConnected = false
	tunnelLastError = ""

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error().Msgf("tunnel panic: %v", r)
				setTunnelError(fmt.Sprintf("panic: %v", r))
			}
			tunnelMu.Lock()
			tunnelRunning = false
			tunnelConnected = false
			tunnelCancel = nil
			tunnelDone = nil
			tunnelMu.Unlock()
			close(done)
		}()

		err := supervisor.StartTunnelDaemon(ctx, tunnelConfig, orchestrator, connectedSignal, reconnectCh, graceShutdownC)
		if err != nil && ctx.Err() == nil {
			logger.Error().Err(err).Msg("tunnel daemon exited with error")
			setTunnelError(err.Error())
		}
	}()

	return nil
}

//export StopTunnel
func StopTunnel() {
	tunnelMu.Lock()
	cancel := tunnelCancel
	done := tunnelDone
	tunnelMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(25 * time.Second):
			logger.Warn().Msg("StopTunnel: timeout waiting for tunnel goroutine; process exit will still tear down")
		}
	}
}

//export IsTunnelRunning
func IsTunnelRunning() C.int {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	if tunnelRunning {
		return 1
	}
	return 0
}

//export IsTunnelConnected
func IsTunnelConnected() C.int {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	if tunnelConnected {
		return 1
	}
	return 0
}

//export GetLastError
func GetLastError() *C.char {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	return C.CString(tunnelLastError)
}

//export FreeString
func FreeString(s *C.char) {
	if s != nil {
		C.free(unsafe.Pointer(s))
	}
}

func setTunnelError(err string) {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	tunnelLastError = err
}

func setTunnelConnected(connected bool) {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	tunnelConnected = connected
}

// tunnelEventSink implements connection.EventSink.
type tunnelEventSink struct{}

func (s *tunnelEventSink) OnTunnelEvent(e connection.Event) {
	switch e.EventType {
	case connection.Connected:
		setTunnelConnected(true)
		setTunnelError("")
	case connection.Disconnected:
		setTunnelConnected(false)
		setTunnelError(fmt.Sprintf("disconnected (conn %d, location %s)", e.Index, e.Location))
	case connection.Reconnecting:
		setTunnelConnected(false)
		setTunnelError(fmt.Sprintf("reconnecting (conn %d)", e.Index))
	}
}

// main is required for c-shared buildmode but not used.
func main() {}
