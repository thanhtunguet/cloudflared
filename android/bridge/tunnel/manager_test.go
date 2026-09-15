package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseTokenValid(t *testing.T) {
	tid := uuid.New()
	tok := tokenPayload{
		AccountTag:   "test-account-tag",
		TunnelSecret: []byte("secret-key-bytes-12345678"),
		TunnelID:     tid,
		Endpoint:     "region1.cftunnel.com",
	}
	data, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(data)

	props, err := ParseToken(b64)
	if err != nil {
		t.Fatalf("expected valid parse, got error: %v", err)
	}
	if props.Credentials.AccountTag != "test-account-tag" {
		t.Errorf("expected account tag test-account-tag, got %s", props.Credentials.AccountTag)
	}
	if props.Credentials.TunnelID != tid {
		t.Errorf("expected tunnel ID %s, got %s", tid, props.Credentials.TunnelID)
	}
}

func TestParseTokenInvalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"not-base64", "!!!not base64!!!"},
		{"missing-fields", base64.StdEncoding.EncodeToString([]byte(`{"a": ""}`))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseToken(tc.raw)
			if err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestManagerInitialState(t *testing.T) {
	mgr := NewManager()
	if mgr.IsRunning() {
		t.Error("expected running to be false")
	}
	if mgr.IsConnected() {
		t.Error("expected connected to be false")
	}
	if mgr.GetLastError() != "" {
		t.Errorf("expected empty error, got %s", mgr.GetLastError())
	}
}

func TestStopDoesNotWaitForDaemonShutdown(t *testing.T) {
	mgr := NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	mgr.cancel = cancel
	mgr.done = make(chan struct{}) // Simulate a daemon that has not unwound yet.

	returned := make(chan struct{})
	go func() {
		mgr.Stop()
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Stop blocked while waiting for daemon shutdown")
	}

	select {
	case <-ctx.Done():
	default:
		t.Fatal("Stop did not cancel the daemon context")
	}
}

func TestStartReturnsBeforeFeatureDiscoveryCompletes(t *testing.T) {
	tid := uuid.New()
	token, err := json.Marshal(tokenPayload{
		AccountTag:   "test-account-tag",
		TunnelSecret: []byte("secret-key-bytes-12345678"),
		TunnelID:     tid,
	})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}

	mgr := NewManager()
	startedAt := time.Now()
	if err := mgr.Start(context.Background(), base64.StdEncoding.EncodeToString(token), 8080, "auto"); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("Start blocked for %s while initializing", elapsed)
	}
	if !mgr.IsRunning() {
		t.Fatal("expected manager to reserve the running state immediately")
	}
	mgr.Stop()
}
