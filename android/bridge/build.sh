#!/usr/bin/env bash
#
# Build script for Cloudflared Android Bridge library
# This script builds the shared library for all supported Android architectures.
#
# Usage:
#   ./build.sh                      # Build all architectures
#   ./build.sh arm64-v8a            # Build only arm64
#   ANDROID_NDK_HOME=/path/to/ndk ./build.sh  # Use specific NDK
#
# Output:
#   build/arm64-v8a/libcloudflared-bridge.so
#   build/armeabi-v7a/libcloudflared-bridge.so
#   build/x86_64/libcloudflared-bridge.so
#   build/include/libcloudflared-bridge.h

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/build"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Find NDK
find_ndk() {
    # Check environment variables
    if [[ -n "${ANDROID_NDK_HOME:-}" ]]; then
        echo "$ANDROID_NDK_HOME"
        return 0
    fi

    if [[ -n "${ANDROID_NDK_ROOT:-}" ]]; then
        echo "$ANDROID_NDK_ROOT"
        return 0
    fi

    # Check Android SDK locations
    local sdk_paths=()
    case "$(uname -s)" in
        Darwin)
            sdk_paths=(
                "$HOME/Library/Android/sdk"
                "/usr/local/share/android-sdk"
            )
            ;;
        Linux)
            sdk_paths=(
                "$HOME/Android/Sdk"
                "/opt/android-sdk"
                "/usr/local/android-sdk"
            )
            ;;
        *)
            sdk_paths=("$HOME/Android/Sdk")
            ;;
    esac

    for sdk_path in "${sdk_paths[@]}"; do
        local ndk_dir="${sdk_path}/ndk"
        if [[ -d "$ndk_dir" ]]; then
            # Find the newest NDK version
            local ndk_version
            ndk_version=$(ls -1 "$ndk_dir" 2>/dev/null | sort -V | tail -n1 || true)
            if [[ -n "$ndk_version" && -d "$ndk_dir/$ndk_version" ]]; then
                echo "$ndk_dir/$ndk_version"
                return 0
            fi
        fi
    done

    # Check standalone NDK installations
    local common_paths=(
        "/opt/android-ndk"
        "/usr/local/android-ndk"
    )
    for path in "${common_paths[@]}"; do
        if [[ -d "$path" ]]; then
            echo "$path"
            return 0
        fi
    done

    return 1
}

# Get toolchain path
get_toolchain() {
    local ndk="$1"
    local host_tag

    case "$(uname -s)" in
        Darwin)
            # NDK only provides darwin-x86_64 host toolchains (universal binaries work on both Intel and Apple Silicon)
            host_tag="darwin-x86_64"
            ;;
        Linux)
            host_tag="linux-x86_64"
            ;;
        MINGW*|CYGWIN*|MSYS*)
            host_tag="windows-x86_64"
            ;;
        *)
            log_error "Unsupported OS: $(uname -s)"
            exit 1
            ;;
    esac

    local toolchain="${ndk}/toolchains/llvm/prebuilt/${host_tag}/bin"

    if [[ ! -d "$toolchain" ]]; then
        log_error "Toolchain directory not found: $toolchain"
        return 1
    fi

    echo "$toolchain"
}

# Build for a specific architecture
build_arch() {
    local abi="$1"
    local goarch="$2"
    local clang_base="$3"

    log_info "Building for $abi ($goarch)..."

    local out_dir="${BUILD_DIR}/${abi}"
    mkdir -p "$out_dir"

    # Clang binary name format: aarch64-linux-android21-clang (no dash before API level)
    local cc="${TOOLCHAIN}/${clang_base}${api_level}-clang"

    if [[ ! -x "$cc" ]]; then
        log_error "Compiler not found: $cc"
        exit 1
    fi

    local output="${out_dir}/libcloudflared-bridge.so"

    CGO_ENABLED=1 \
    GOOS=android \
    GOARCH="$goarch" \
    CC="$cc" \
    go build \
        -buildmode=c-shared \
        -ldflags="-s -w" \
        -trimpath \
        -o "$output" \
        "${SCRIPT_DIR}"

    log_info "Built: $output"
}

# Main build function
main() {
    log_info "Starting Cloudflared Android Bridge build..."

    # API level for Android
    api_level="21"

    # Find NDK
    NDK_HOME=$(find_ndk) || true
    if [[ -z "$NDK_HOME" ]]; then
        log_error "Android NDK not found!"
        log_error "Please set ANDROID_NDK_HOME or install NDK at a standard location."
        exit 1
    fi

    log_info "Using NDK: $NDK_HOME"

    # Get toolchain
    TOOLCHAIN=$(get_toolchain "$NDK_HOME")
    if [[ ! -d "$TOOLCHAIN" ]]; then
        log_error "Toolchain not found: $TOOLCHAIN"
        exit 1
    fi

    log_info "Using toolchain: $TOOLCHAIN"

    # Create build directory
    mkdir -p "$BUILD_DIR"

    # Determine which architectures to build
    local target_arch="${1:-all}"

    case "$target_arch" in
        all|arm64-v8a)
            build_arch "arm64-v8a" "arm64" "aarch64-linux-android"
            ;;
    esac

    case "$target_arch" in
        all|armeabi-v7a)
            build_arch "armeabi-v7a" "arm" "armv7a-linux-androideabi"
            ;;
    esac

    case "$target_arch" in
        all|x86_64)
            build_arch "x86_64" "amd64" "x86_64-linux-android"
            ;;
    esac

    # Copy header file (generated from arm64 build)
    local header_src="${BUILD_DIR}/arm64-v8a/libcloudflared-bridge.h"
    local header_dst="${BUILD_DIR}/include/"

    if [[ -f "$header_src" ]]; then
        mkdir -p "$header_dst"
        cp "$header_src" "$header_dst"
        log_info "Copied header to: $header_dst"
    fi

    log_info "Build complete!"
    log_info "Output directory: $BUILD_DIR"
    echo ""
    log_info "Files generated:"
    find "$BUILD_DIR" -type f \( -name "*.so" -o -name "*.h" \) | while read -r file; do
        echo "  - $file"
    done
}

# Run main function
main "$@"
