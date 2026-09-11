package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"testing"

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
