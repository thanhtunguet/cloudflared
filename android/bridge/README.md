# cloudflared Android tunnel package

This directory holds the Go package `tunnel` (module
`github.com/cloudflare/cloudflared/android/bridge`), which implements the
Cloudflare tunnel manager used on Android: start/stop, connection-state
monitoring, retries, and structured logging.

It is **not** built into a standalone `.so`. The MyHome app builds a single
shared library, `libmyhome-go.so`, from `myhome-go-bridge/`, which imports this
package together with the CLIProxyAPI and CPA Usage Keeper Android SDK packages:

```
myhome-go-bridge/bridge.go
├── github.com/cloudflare/cloudflared/android/bridge/tunnel   ← this package
├── github.com/router-for-me/CLIProxyAPI/v7/sdk/android
└── cpa-usage-keeper/sdk/android
```

Because all three are linked into one `-buildmode=c-shared` library, they share a
single embedded Go runtime (one scheduler, GC, and netpoller).

## Layout

```
cloudflared/android/bridge/
├── go.mod      # Module definition (hosts the tunnel package)
├── go.sum
├── README.md   # This file
└── tunnel/
    ├── manager.go       # Tunnel manager
    └── manager_test.go
```

## Building

There is no separate build step. The package is compiled as part of the app's
unified bridge; see `myhome-go-bridge/build.sh` and the `myhomeGoBridgeBuild`
task in `app/build.gradle` at the repository root.

## Tests

```bash
cd cloudflared/android/bridge
go test ./tunnel
```

## History

An earlier standalone bridge (`bridge.go` + `bridge_jni.c` + `build.sh`) built a
separate `libcloudflared-bridge.so`. It was superseded by the unified
`libmyhome-go.so` in 2026-09 and removed in 2026-09-25. See the app changelog
`docs/changelogs/2026-09-25-remove-cpp-and-rust-native-code.md`.

## License

This library inherits the license from the parent cloudflared project.
