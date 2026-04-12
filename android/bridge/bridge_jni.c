// bridge_jni.c - JNI implementation for Android integration
// This file provides JNI bindings that forward calls from Java/Kotlin to the Go bridge.
// The Go library (libcloudflared-bridge.so) already contains all C helper functions
// via CGO preamble in bridge.go, so this file only contains JNI entry points.
//
// Do not wrap this file in #ifdef __ANDROID__: CGO may compile it without that macro
// defined, which would strip all JNI symbols and cause UnsatisfiedLinkError at runtime
// while System.loadLibrary still succeeds. This directory is only built for Android via build.sh.

#include <jni.h>
#include <android/log.h>
#include <stdlib.h>
#include <string.h>

#define LOG_TAG "CloudflaredBridge"

// Go exported functions (from bridge.go, compiled into the shared library)
extern char* StartTunnel(char* token, int proxyPort);
extern void StopTunnel(void);
extern int IsTunnelRunning(void);
extern char* GetLastError(void);
extern void FreeString(char* s);

// Java_info_thanhtunguet_myhome_CloudflareTunnelBridge_nativeStartTunnel
// Starts the tunnel with the given token and proxy port.
// Returns empty string on success, error message on failure.
JNIEXPORT jstring JNICALL
Java_info_thanhtunguet_myhome_CloudflareTunnelBridge_nativeStartTunnel(
    JNIEnv* env,
    jclass clazz,
    jstring token,
    jint proxyPort) {

    (void)clazz;

    const char* tokenChars = (*env)->GetStringUTFChars(env, token, NULL);
    if (tokenChars == NULL) {
        return (*env)->NewStringUTF(env, "failed to get token string");
    }

    char* result = StartTunnel((char*)tokenChars, (int)proxyPort);
    (*env)->ReleaseStringUTFChars(env, token, tokenChars);

    jstring jResult = (*env)->NewStringUTF(env, result ? result : "");
    if (result != NULL) {
        FreeString(result);
    }
    return jResult;
}

// Java_info_thanhtunguet_myhome_CloudflareTunnelBridge_nativeStopTunnel
// Stops the running tunnel.
JNIEXPORT void JNICALL
Java_info_thanhtunguet_myhome_CloudflareTunnelBridge_nativeStopTunnel(
    JNIEnv* env,
    jclass clazz) {

    (void)env;
    (void)clazz;
    StopTunnel();
}

// Java_info_thanhtunguet_myhome_CloudflareTunnelBridge_nativeIsTunnelRunning
// Returns 1 if tunnel is running, 0 otherwise.
JNIEXPORT jint JNICALL
Java_info_thanhtunguet_myhome_CloudflareTunnelBridge_nativeIsTunnelRunning(
    JNIEnv* env,
    jclass clazz) {

    (void)env;
    (void)clazz;
    return (jint)IsTunnelRunning();
}

// Java_info_thanhtunguet_myhome_CloudflareTunnelBridge_nativeGetLastError
// Returns the last error message (empty string if none).
JNIEXPORT jstring JNICALL
Java_info_thanhtunguet_myhome_CloudflareTunnelBridge_nativeGetLastError(
    JNIEnv* env,
    jclass clazz) {

    (void)clazz;
    char* err = GetLastError();
    jstring result = (*env)->NewStringUTF(env, err ? err : "");
    if (err != NULL) {
        FreeString(err);
    }
    return result;
}
