module github.com/cloudflare/cloudflared/android/bridge

go 1.24.0

// The bridge module depends on the parent cloudflared module
require github.com/cloudflare/cloudflared v0.0.0

replace github.com/cloudflare/cloudflared => ../..
