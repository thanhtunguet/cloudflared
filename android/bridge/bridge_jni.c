// bridge_jni.c - JNI implementation for Android integration
// This file provides JNI bindings that forward calls from Java/Kotlin to the Go bridge.
//
// To use in your Android app:
// 1. Include this file in your Android NDK build
// 2. The Go functions (StartTunnel, StopTunnel, etc.) are exported from the Go library
// 3. Java calls native methods which call the Go functions
//
// Expected Java class: com.cloudflare.cloudflared.CloudflaredBridge
// If you use a different package/class, adjust the JNI function names accordingly.

#ifdef __ANDROID__
#include <jni.h>
#include <android/log.h>
#include <stdlib.h>
#include <string.h>

#define LOG_TAG "CloudflaredBridge"

// Go exported functions (from bridge.go)
extern char* StartTunnel(char* token, int proxyPort);
extern void StopTunnel(void);
extern int IsTunnelRunning(void);
extern char* GetLastError(void);
extern void FreeString(char* s);
extern void SetLogCallback(void* callback);

// Java log callback storage
static JavaVM* g_vm = NULL;
static jobject g_logCallback = NULL;
static jmethodID g_logMethod = NULL;

// Callback from Go to Java
static void javaLogCallback(int level, const char* msg) {
    if (g_vm == NULL || g_logCallback == NULL || g_logMethod == NULL) {
        // Fallback to Android log
        int android_level = ANDROID_LOG_DEBUG;
        switch (level) {
            case 0: case 1: case 2: android_level = ANDROID_LOG_ERROR; break;
            case 3: android_level = ANDROID_LOG_WARN; break;
            case 4: android_level = ANDROID_LOG_INFO; break;
        }
        __android_log_write(android_level, LOG_TAG, msg ? msg : "null");
        return;
    }

    JNIEnv* env = NULL;
    int attached = 0;

    // Get current JNI environment
    int getEnvResult = (*g_vm)->GetEnv(g_vm, (void**)&env, JNI_VERSION_1_6);
    if (getEnvResult == JNI_EDETACHED) {
        if ((*g_vm)->AttachCurrentThread(g_vm, &env, NULL) != 0) {
            return;
        }
        attached = 1;
    } else if (getEnvResult != JNI_OK) {
        return;
    }

    // Call Java callback method
    jstring jmsg = (*env)->NewStringUTF(env, msg ? msg : "");
    (*env)->CallVoidMethod(env, g_logCallback, g_logMethod, level, jmsg);
    (*env)->DeleteLocalRef(env, jmsg);

    if (attached) {
        (*g_vm)->DetachCurrentThread(g_vm);
    }
}

// JNI_OnLoad - called when library is loaded
JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM* vm, void* reserved) {
    (void)reserved;
    g_vm = vm;
    return JNI_VERSION_1_6;
}

// JNI_OnUnload - called when library is unloaded
JNIEXPORT void JNICALL JNI_OnUnload(JavaVM* vm, void* reserved) {
    (void)vm;
    (void)reserved;
    if (g_logCallback != NULL) {
        JNIEnv* env = NULL;
        if ((*vm)->GetEnv(vm, (void**)&env, JNI_VERSION_1_6) == JNI_OK) {
            (*env)->DeleteGlobalRef(env, g_logCallback);
        }
        g_logCallback = NULL;
    }
    g_vm = NULL;
}

// Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeStartTunnel
// Starts the tunnel with the given token and proxy port.
// Returns empty string on success, error message on failure.
JNIEXPORT jstring JNICALL
Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeStartTunnel(
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

// Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeStopTunnel
// Stops the running tunnel.
JNIEXPORT void JNICALL
Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeStopTunnel(
    JNIEnv* env,
    jclass clazz) {

    (void)env;
    (void)clazz;
    StopTunnel();
}

// Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeIsTunnelRunning
// Returns 1 if tunnel is running, 0 otherwise.
JNIEXPORT jint JNICALL
Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeIsTunnelRunning(
    JNIEnv* env,
    jclass clazz) {

    (void)env;
    (void)clazz;
    return (jint)IsTunnelRunning();
}

// Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeGetLastError
// Returns the last error message (empty string if none).
JNIEXPORT jstring JNICALL
Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeGetLastError(
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

// Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeSetLogCallback
// Sets a callback for receiving log messages from the tunnel.
// The callback should have signature: void onLog(int level, String message)
JNIEXPORT void JNICALL
Java_com_cloudflare_cloudflared_CloudflaredBridge_nativeSetLogCallback(
    JNIEnv* env,
    jclass clazz,
    jobject callback) {

    (void)clazz;

    // Clear existing callback
    if (g_logCallback != NULL) {
        (*env)->DeleteGlobalRef(env, g_logCallback);
        g_logCallback = NULL;
        g_logMethod = NULL;
    }

    if (callback == NULL) {
        SetLogCallback(NULL);
        return;
    }

    // Store global reference to callback object
    g_logCallback = (*env)->NewGlobalRef(env, callback);
    if (g_logCallback == NULL) {
        return;
    }

    // Find the callback method
    jclass callbackClass = (*env)->GetObjectClass(env, callback);
    if (callbackClass == NULL) {
        (*env)->DeleteGlobalRef(env, g_logCallback);
        g_logCallback = NULL;
        return;
    }

    g_logMethod = (*env)->GetMethodID(env, callbackClass, "onLog", "(ILjava/lang/String;)V");
    (*env)->DeleteLocalRef(env, callbackClass);

    if (g_logMethod == NULL) {
        (*env)->DeleteGlobalRef(env, g_logCallback);
        g_logCallback = NULL;
        return;
    }

    // Set the Go callback
    SetLogCallback((void*)javaLogCallback);
}

#endif // __ANDROID__
