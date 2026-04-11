# Cloudflared Android Bridge

A reusable Android library for integrating Cloudflare tunnels into Android apps.

## Overview

This library provides a bridge between Android (Java/Kotlin) and the Go-based cloudflared implementation, allowing any Android app to:

- Start/stop Cloudflare tunnels programmatically
- Monitor tunnel connection state
- Receive log messages from the tunnel
- Handle connection retries automatically

## Project Structure

```
cloudflared/android/bridge/
├── bridge.go          # Go implementation with JNI exports
├── bridge_jni.c       # JNI C wrapper for Android integration
├── go.mod             # Go module definition
├── README.md          # This file
└── build.sh           # Build script (create this)
```

## Building the Library

### Prerequisites

1. Go 1.24.0 or later
2. Android NDK (version 21 or later recommended)
3. `CGO_ENABLED` must be set

### Build Commands

Build for a single architecture (arm64):

```bash
cd cloudflared/android/bridge

export NDK=/path/to/android-ndk
export TOOLCHAIN=$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin

CGO_ENABLED=1 \
GOOS=android \
GOARCH=arm64 \
CC=$TOOLCHAIN/aarch64-linux-android21-clang \
go build -buildmode=c-shared -o libcloudflared-bridge.so
```

Build for all architectures (using the provided script):

```bash
./build.sh
```

### Build Output

After building, you'll have:
- `libcloudflared-bridge.so` - The shared library for Android
- `libcloudflared-bridge.h` - Auto-generated C header (from cgo)

## Android Integration

### 1. Copy Library to Your Project

```
app/src/main/jniLibs/
├── arm64-v8a/
│   └── libcloudflared-bridge.so
├── armeabi-v7a/
│   └── libcloudflared-bridge.so
└── x86_64/
    └── libcloudflared-bridge.so
```

### 2. Create Java/Kotlin Wrapper

**Kotlin Example:**

```kotlin
package com.cloudflare.cloudflared

object CloudflaredBridge {

    init {
        System.loadLibrary("cloudflared-bridge")
    }

    interface LogCallback {
        fun onLog(level: Int, message: String)
    }

    @JvmStatic
    private external fun nativeStartTunnel(token: String, proxyPort: Int): String

    @JvmStatic
    private external fun nativeStopTunnel()

    @JvmStatic
    private external fun nativeIsTunnelRunning(): Int

    @JvmStatic
    private external fun nativeGetLastError(): String

    @JvmStatic
    private external fun nativeSetLogCallback(callback: LogCallback?)

    /**
     * Start a Cloudflare tunnel.
     *
     * @param token The tunnel token (base64-encoded JSON)
     * @param proxyPort The local proxy port to tunnel to
     * @return Empty string on success, error message on failure
     */
    fun startTunnel(token: String, proxyPort: Int): String {
        return nativeStartTunnel(token, proxyPort)
    }

    /**
     * Stop the running tunnel.
     */
    fun stopTunnel() {
        nativeStopTunnel()
    }

    /**
     * Check if tunnel is currently running.
     */
    fun isTunnelRunning(): Boolean {
        return nativeIsTunnelRunning() == 1
    }

    /**
     * Get the last error message.
     */
    fun getLastError(): String {
        return nativeGetLastError()
    }

    /**
     * Set a log callback to receive tunnel logs.
     */
    fun setLogCallback(callback: LogCallback?) {
        nativeSetLogCallback(callback)
    }
}
```

### 3. Usage Example

```kotlin
class MainActivity : AppCompatActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // Optional: Set up logging
        CloudflaredBridge.setLogCallback(object : CloudflaredBridge.LogCallback {
            override fun onLog(level: Int, message: String) {
                when (level) {
                    0, 1, 2 -> Log.e("Tunnel", message)
                    3 -> Log.w("Tunnel", message)
                    4 -> Log.i("Tunnel", message)
                    else -> Log.d("Tunnel", message)
                }
            }
        })

        // Start tunnel
        val token = "eyJhIjog..." // Your tunnel token
        val proxyPort = 8080

        val error = CloudflaredBridge.startTunnel(token, proxyPort)
        if (error.isEmpty()) {
            Log.i("Tunnel", "Tunnel started successfully")
        } else {
            Log.e("Tunnel", "Failed to start: $error")
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        CloudflaredBridge.stopTunnel()
    }
}
```

### 4. Required Permissions

Add to your `AndroidManifest.xml`:

```xml
<uses-permission android:name="android.permission.INTERNET" />
<uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" />
<uses-permission android:name="android.permission.FOREGROUND_SERVICE" />
```

## Tunnel Token Format

The tunnel token is a base64-encoded JSON object with these fields:

```json
{
  "a": "account_tag",
  "s": "base64_encoded_secret",
  "t": "tunnel_uuid",
  "e": "optional_endpoint"
}
```

To obtain a token:
1. Create a tunnel in the Cloudflare dashboard
2. Use `cloudflared tunnel token <tunnel-name>` to get the token
3. Or extract from `~/.cloudflared/<tunnel-id>.json`

## Log Levels

The log callback receives messages with these levels:

| Level | Description |
|-------|-------------|
| 0-2   | Error/Panic/Fatal |
| 3     | Warning |
| 4     | Info |
| 5+    | Debug/Trace |

## Architecture Support

The library supports these Android architectures:

- `arm64-v8a` (ARM64) - Modern Android devices
- `armeabi-v7a` (ARMv7) - Older ARM devices
- `x86_64` - Emulators and x86 devices

## Threading Notes

- `startTunnel()` returns immediately after starting the tunnel goroutine
- The tunnel runs in a background goroutine
- `stopTunnel()` blocks until the tunnel fully stops
- Log callbacks may be called from any thread - ensure your callback is thread-safe

## Error Handling

Common error messages:

| Error | Cause |
|-------|-------|
| "token is required" | Empty or null token |
| "base64 decode: ..." | Invalid base64 in token |
| "json unmarshal: ..." | Token JSON is malformed |
| "token missing required fields" | Missing a/s/t fields |
| "tunnel already running" | Tunnel is already active |
| "parse ingress: ..." | Invalid proxy port configuration |

## Building Custom Versions

To customize the build:

1. Edit `bridge.go` to modify tunnel configuration
2. Adjust `HAConnections`, `Retries`, `GracePeriod`, etc.
3. Rebuild with `./build.sh`

## License

This library inherits the license from the parent cloudflared project.

## See Also

- [Cloudflare Tunnel Documentation](https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/)
- [cloudflared GitHub](https://github.com/cloudflare/cloudflared)
