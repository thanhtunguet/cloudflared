// Package main provides Android JNI bindings for cloudflared tunnel functionality.
package main

/*
#include <stdlib.h>
#include <android/log.h>

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
	"unsafe"

	"github.com/cloudflare/cloudflared/android/bridge/tunnel"
)

//export SetLogCallback
func SetLogCallback(cb unsafe.Pointer) {
	C.set_log_callback((C.log_callback_t)(cb))
}

func forwardLogToJava(level int, msg string) {
	cmsg := C.CString(msg)
	defer C.free(unsafe.Pointer(cmsg))
	C.forward_log(C.int(level), cmsg)
}

func init() {
	tunnel.DefaultManager.SetLogCallback(func(level int, msg string) {
		forwardLogToJava(level, msg)
	})
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

	protocolStr := ""
	if protocol != nil {
		protocolStr = C.GoString(protocol)
	}

	err := tunnel.DefaultManager.Start(context.Background(), tokenStr, int(proxyPort), protocolStr)
	if err != nil {
		return C.CString(err.Error())
	}
	return C.CString("")
}

//export StopTunnel
func StopTunnel() {
	tunnel.DefaultManager.Stop()
}

//export IsTunnelRunning
func IsTunnelRunning() C.int {
	if tunnel.DefaultManager.IsRunning() {
		return 1
	}
	return 0
}

//export IsTunnelConnected
func IsTunnelConnected() C.int {
	if tunnel.DefaultManager.IsConnected() {
		return 1
	}
	return 0
}

//export GetLastError
func GetLastError() *C.char {
	return C.CString(tunnel.DefaultManager.GetLastError())
}

//export FreeString
func FreeString(s *C.char) {
	if s != nil {
		C.free(unsafe.Pointer(s))
	}
}

func main() {}
