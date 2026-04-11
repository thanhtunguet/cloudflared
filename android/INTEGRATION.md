# Using the Cloudflared Android Bridge in CLIProxyAPI

This document explains how CLIProxyAPI can integrate with the new `cloudflared/android/bridge` submodule.

## Current Architecture

CLIProxyAPI currently uses:
- `mobile/main.go` - JNI bridge that calls `internal/cloudflared`
- `internal/cloudflared/` - Go adapter for cloudflared tunnel

## New Architecture Options

### Option 1: Keep Current (Recommended for Stability)

Keep the existing `internal/cloudflared` implementation. It works well and is integrated with the main codebase.

**Pros:**
- No changes needed to existing code
- Full control over the implementation
- Easy to customize for CLIProxyAPI needs

**Cons:**
- Code duplication if you want to reuse in other apps
- Updates require modifying both places

### Option 2: Migrate to Bridge Submodule

Replace `internal/cloudflared` with the new bridge submodule.

**Steps to migrate:**

1. **Add go.mod dependency:**
   ```go
   // In go.mod, add:
   require github.com/cloudflare/cloudflared/android/bridge v0.0.0
   
   replace github.com/cloudflare/cloudflared/android/bridge => ./cloudflared/android/bridge
   ```

2. **Update imports in mobile/main.go:**
   ```go
   // Replace:
   cfadapter "github.com/router-for-me/CLIProxyAPI/v6/internal/cloudflared"
   
   // With:
   cfbridge "github.com/cloudflare/cloudflared/android/bridge"
   ```

3. **Update function calls:**
   ```go
   // Old:
   cfadapter.StartTunnel(tokenStr, int(proxyPort))
   
   // New:
   cfbridge.StartTunnel(C.CString(tokenStr), C.int(proxyPort))
   ```

**Pros:**
- Single source of truth for tunnel logic
- Can be used by other Android apps
- Cleaner separation of concerns

**Cons:**
- Requires changes to existing code
- The bridge is more generic (less CLIProxyAPI-specific)

### Option 3: Hybrid Approach

Use the bridge for the core tunnel logic, but keep CLIProxyAPI-specific code in `internal/cloudflared`.

**Structure:**
```
internal/cloudflared/
├── adapter.go      # Thin wrapper, CLIProxyAPI-specific logic
└── bridge/         # Re-exports from cloudflared/android/bridge
```

**Implementation:**

```go
// internal/cloudflared/bridge/bridge.go
package bridge

import (
    "github.com/cloudflare/cloudflared/android/bridge"
)

// Re-export functions with Go-friendly signatures
func StartTunnel(token string, proxyPort int) error {
    ct := C.CString(token)
    defer C.free(unsafe.Pointer(ct))
    
    result := bridge.StartTunnel(ct, C.int(proxyPort))
    if result != nil {
        errStr := C.GoString(result)
        bridge.FreeString(result)
        if errStr != "" {
            return errors.New(errStr)
        }
    }
    return nil
}

func StopTunnel() {
    bridge.StopTunnel()
}

func IsTunnelRunning() bool {
    return bridge.IsTunnelRunning() == 1
}

func GetLastError() string {
    errPtr := bridge.GetLastError()
    if errPtr == nil {
        return ""
    }
    errStr := C.GoString(errPtr)
    bridge.FreeString(errPtr)
    return errStr
}
```

## Building for CLIProxyAPI

### Build the Bridge Library

```bash
cd cloudflared/android/bridge

# Build for all architectures
./build.sh

# Or build for specific arch
./build.sh arm64-v8a
```

### Copy to CLIProxyAPI Android Project

The bridge generates:
- `libcloudflared-bridge.so` (for each architecture)
- `libcloudflared-bridge.h` (C header)

**Option A: Replace JNI Bridge Completely**

Replace `mobile/jni_bridge.c` with `bridge_jni.c`:

```bash
cp bridge_jni.c mobile/
```

Update the Go build to use `cloudflared/android/bridge` instead of `mobile/main.go`.

**Option B: Keep Existing, Add Bridge as Library**

Add the bridge as a secondary library:

1. Copy `.so` files to `jniLibs/`
2. Update `build.gradle.kts` to link both libraries
3. Load both in Java:
   ```kotlin
   System.loadLibrary("cloudflared-bridge")  // New bridge
   System.loadLibrary("cliproxy")             // Existing CLIProxy
   ```

## Recommended Approach for CLIProxyAPI

For production use, I recommend **Option 1** (Keep Current) for now because:

1. The current `internal/cloudflared` has been tested and works
2. It has CLIProxyAPI-specific logging and error handling
3. Migration requires significant testing

However, if you want to use the bridge in **new Android apps** or **share with other projects**, the bridge submodule is ready to use.

## Future Migration Path

If you decide to migrate CLIProxyAPI to use the bridge:

1. **Phase 1:** Keep both implementations, test bridge in parallel
2. **Phase 2:** Switch `mobile/main.go` to use bridge
3. **Phase 3:** Remove `internal/cloudflared` once bridge is stable
4. **Phase 4:** Contribute improvements back to the bridge submodule

## Testing the Bridge

You can test the bridge independently:

```bash
cd cloudflared/android/bridge

# Build
./build.sh

# Check the output
ls -la build/
```

## Documentation

- [Bridge README](bridge/README.md) - Full usage documentation
- The bridge is designed to be a drop-in replacement for basic tunnel functionality

## Questions?

If you have questions about integrating the bridge, check:
1. The bridge's README.md for API documentation
2. This file for integration options
3. The example Java/Kotlin code in the bridge README
