// Package tunnel provides a context-owned Cloudflare tunnel manager for Android integration.
package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"runtime"
	"sync"
	"time"

	"github.com/cloudflare/cloudflared/client"
	cfconfig "github.com/cloudflare/cloudflared/config"
	"github.com/cloudflare/cloudflared/connection"
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

// Manager owns the lifecycle of an embedded Cloudflare tunnel daemon.
type Manager struct {
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	running     bool
	connected   bool
	lastError   string
	logger      zerolog.Logger
	logCallback func(level int, msg string)
}

// logWriter forwards zerolog messages to logCallback if set.
type logWriter struct {
	m *Manager
}

func (w *logWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.m.mu.Lock()
	cb := w.m.logCallback
	w.m.mu.Unlock()
	if cb != nil {
		cb(4, string(p)) // 4 = INFO
	}
	return len(p), nil
}

// NewManager creates an unstarted tunnel manager.
func NewManager() *Manager {
	m := &Manager{}
	m.logger = zerolog.New(&logWriter{m: m}).
		With().
		Str("component", "cloudflared").
		Timestamp().
		Logger()
	return m
}

// DefaultManager is the global default tunnel manager instance.
var DefaultManager = NewManager()

// SetLogCallback registers a callback invoked when cloudflared logs a message.
func (m *Manager) SetLogCallback(cb func(level int, msg string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logCallback = cb
}

// ParseToken decodes and validates a base64-encoded tunnel token.
func ParseToken(tokenStr string) (*connection.TunnelProperties, error) {
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

func buildEdgeTLSConfigs() (map[connection.Protocol]*tls.Config, error) {
	configs := make(map[connection.Protocol]*tls.Config, len(connection.ProtocolList))
	for _, p := range connection.ProtocolList {
		tlsSettings := p.TLSSettings()
		if tlsSettings == nil {
			return nil, fmt.Errorf("%s has unknown TLS settings", p)
		}
		cfg, err := tlsconfig.CreateTunnelConfig("", tlsSettings.ServerName)
		if err != nil {
			return nil, fmt.Errorf("TLS config for %s: %w", p, err)
		}
		if len(tlsSettings.NextProtos) > 0 {
			cfg.NextProtos = tlsSettings.NextProtos
		}
		configs[p] = cfg
	}
	return configs, nil
}

// Start reserves the tunnel lifecycle and launches its initialization asynchronously.
//
// Feature discovery can perform DNS requests, which are particularly slow or unavailable
// on Android TV networks. JNI must return promptly so that a Stop request can cancel that
// initialization instead of blocking the app in a perpetual "Starting" state.
func (m *Manager) Start(ctx context.Context, tokenStr string, proxyPort int, protocol string) error {
	namedTunnel, err := ParseToken(tokenStr)
	if err != nil {
		return fmt.Errorf("parse token: %w", err)
	}

	if protocol == "" {
		protocol = connection.AutoSelectFlag
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		cancel()
		return fmt.Errorf("tunnel already running")
	}
	m.cancel = cancel
	m.done = done
	m.running = true
	m.connected = false
	m.lastError = ""
	m.mu.Unlock()

	go m.run(runCtx, done, namedTunnel, proxyPort, protocol)
	return nil
}

func (m *Manager) run(
	runCtx context.Context,
	done chan struct{},
	namedTunnel *connection.TunnelProperties,
	proxyPort int,
	protocol string,
) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Error().Msgf("tunnel panic: %v", r)
			m.setError(fmt.Sprintf("panic: %v", r))
		}
		m.mu.Lock()
		if m.done == done {
			m.running = false
			m.connected = false
			m.cancel = nil
			m.done = nil
		}
		m.mu.Unlock()
		close(done)
	}()

	// Observer
	observer := connection.NewObserver(&m.logger)
	observer.RegisterSink(&tunnelEventSink{m: m})

	// Feature discovery is optional. Android TV boxes commonly have a captive or
	// unreachable DNS resolver; never let that auxiliary lookup delay the tunnel
	// connection indefinitely. The selector safely falls back to default features.
	featureCtx, cancelFeatureLookup := context.WithTimeout(runCtx, 3*time.Second)
	featureSelector, err := features.NewFeatureSelector(featureCtx, namedTunnel.Credentials.AccountTag, nil, false, &m.logger)
	cancelFeatureLookup()
	if err != nil {
		m.recordStartupError(runCtx, fmt.Errorf("feature selector: %w", err))
		return
	}

	// Client config
	clientConfig, err := client.NewConfig("android-embedded", runtime.GOARCH, featureSelector)
	if err != nil {
		m.recordStartupError(runCtx, fmt.Errorf("client config: %w", err))
		return
	}

	m.logger.Info().Msgf("Connector ID: %s", clientConfig.ConnectorID)

	tags := []pogs.Tag{
		{Name: "ID", Value: clientConfig.ConnectorID.String()},
	}

	protocolSelector, err := connection.NewProtocolSelector(protocol, &m.logger)
	if err != nil {
		m.recordStartupError(runCtx, fmt.Errorf("protocol selector: %w", err))
		return
	}

	edgeTLSConfigs, err := buildEdgeTLSConfigs()
	if err != nil {
		m.recordStartupError(runCtx, fmt.Errorf("edge TLS: %w", err))
		return
	}

	ingressRules, err := ingress.ParseIngress(&cfconfig.Configuration{
		Ingress: []cfconfig.UnvalidatedIngressRule{
			{Service: fmt.Sprintf("http://127.0.0.1:%d", proxyPort)},
		},
	})
	if err != nil {
		m.recordStartupError(runCtx, fmt.Errorf("parse ingress: %w", err))
		return
	}

	warpConfig := ingress.NewWarpRoutingConfig(&cfconfig.WarpRoutingConfig{})
	dialer := ingress.NewDialer(warpConfig)
	originDialerService := ingress.NewOriginDialer(ingress.OriginConfig{
		DefaultDialer: dialer,
	}, &m.logger)

	dnsService := origins.NewStaticDNSResolverService(
		[]netip.AddrPort{
			netip.AddrPortFrom(netip.MustParseAddr("1.1.1.1"), 53),
			netip.AddrPortFrom(netip.MustParseAddr("1.0.0.1"), 53),
		},
		origins.NewDNSDialer(),
		&m.logger,
		noopMetrics{},
	)
	originDialerService.AddReservedService(dnsService, []netip.AddrPort{origins.VirtualDNSServiceAddr})

	tunnelConfig := &supervisor.TunnelConfig{
		ClientConfig:        clientConfig,
		GracePeriod:         30 * time.Second,
		HAConnections:       1,
		Tags:                tags,
		Log:                 &m.logger,
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

	orchConfig := &orchestration.Config{
		Ingress:             &ingressRules,
		WarpRouting:         warpConfig,
		OriginDialerService: originDialerService,
	}

	orchestrator, err := orchestration.NewOrchestrator(runCtx, orchConfig, tags, nil, &m.logger)
	if err != nil {
		m.recordStartupError(runCtx, fmt.Errorf("orchestrator: %w", err))
		return
	}

	connectedSignal := signal.New(make(chan struct{}))
	graceShutdownC := make(chan struct{})

	err = supervisor.StartTunnelDaemon(runCtx, tunnelConfig, orchestrator, connectedSignal, graceShutdownC)
	if err != nil && runCtx.Err() == nil {
		m.logger.Error().Err(err).Msg("tunnel daemon exited with error")
		m.setError(err.Error())
	}
}

func (m *Manager) recordStartupError(ctx context.Context, err error) {
	if ctx.Err() == nil {
		m.logger.Error().Err(err).Msg("tunnel startup failed")
		m.setError(err.Error())
	}
}

// Stop requests that the running tunnel daemon terminate gracefully.
//
// StartTunnelDaemon may need time to unwind an in-flight network operation. JNI callers
// must never block their service lifecycle on that cleanup: they observe IsRunning and
// start a replacement only after the daemon goroutine has returned.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// IsRunning returns true if the tunnel goroutine is active.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// IsConnected returns true if the tunnel has an active edge connection.
func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

// GetLastError returns the last observed error message.
func (m *Manager) GetLastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastError
}

func (m *Manager) setError(err string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = err
}

func (m *Manager) setConnected(connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = connected
}

type tunnelEventSink struct {
	m *Manager
}

func (s *tunnelEventSink) OnTunnelEvent(e connection.Event) {
	switch e.EventType {
	case connection.Connected:
		s.m.setConnected(true)
		s.m.setError("")
	case connection.Disconnected:
		s.m.setConnected(false)
		s.m.setError(fmt.Sprintf("disconnected (conn %d, location %s)", e.Index, e.Location))
	case connection.Reconnecting:
		s.m.setConnected(false)
		s.m.setError(fmt.Sprintf("reconnecting (conn %d)", e.Index))
	}
}

// Package-level helpers forwarding to DefaultManager

func Start(ctx context.Context, tokenStr string, proxyPort int, protocol string) error {
	return DefaultManager.Start(ctx, tokenStr, proxyPort, protocol)
}

func Stop() {
	DefaultManager.Stop()
}

func IsRunning() bool {
	return DefaultManager.IsRunning()
}

func IsConnected() bool {
	return DefaultManager.IsConnected()
}

func GetLastError() string {
	return DefaultManager.GetLastError()
}

func SetLogCallback(cb func(level int, msg string)) {
	DefaultManager.SetLogCallback(cb)
}
