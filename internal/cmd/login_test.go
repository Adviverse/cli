package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// isolateConfig points os.UserConfigDir at a temp dir and clears env credentials.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("ADVIVERSE_API_KEY", "")
	t.Setenv("ADVIVERSE_BASE_URL", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestLoginDeviceFlow(t *testing.T) {
	isolateConfig(t)
	var codeBody map[string]any
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" {
			t.Errorf("device endpoints must be unauthenticated")
		}
		switch r.URL.Path {
		case "/v1/auth/device/code":
			json.NewDecoder(r.Body).Decode(&codeBody)
			w.Write([]byte(`{"device_code":"dc1","user_code":"ABCD-EFGH","verification_uri":"https://x/device",
				"verification_uri_complete":"https://x/device?code=ABCD-EFGH","expires_in":600,"interval":5}`))
		case "/v1/auth/device/token":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["device_code"] != "dc1" {
				t.Errorf("device_code = %q", b["device_code"])
			}
			polls++
			switch polls {
			case 1:
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"authorization_pending"}`))
			case 2:
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"slow_down"}`))
			default:
				w.Write([]byte(`{"api_key":"ak_new","key_id":"key_1","scopes":["read"],"expires_at":"2099-01-01T00:00:00Z"}`))
			}
		}
	}))
	defer srv.Close()

	var waits []time.Duration
	var opened string
	oldSleep, oldTTY, oldOpen := sleep, stdinIsTTY, openBrowser
	sleep = func(d time.Duration) { waits = append(waits, d) }
	stdinIsTTY = func() bool { return true }
	openBrowser = func(u string) error { opened = u; return nil }
	defer func() { sleep, stdinIsTTY, openBrowser = oldSleep, oldTTY, oldOpen }()

	out, err := runRoot([]string{"login", "--base-url", srv.URL, "--scopes", "read", "--expires", "30"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "ABCD-EFGH") || opened != "https://x/device?code=ABCD-EFGH" {
		t.Fatalf("code not shown / browser not opened (%q):\n%s", opened, out)
	}
	if codeBody["expires_in_days"] != 30.0 || codeBody["scopes"].([]any)[0] != "read" {
		t.Fatalf("code request body: %v", codeBody)
	}
	// RFC 8628: wait interval before each poll, +5s after slow_down.
	if len(waits) != 3 || waits[0] != 5*time.Second || waits[1] != 5*time.Second || waits[2] != 10*time.Second {
		t.Fatalf("poll waits = %v", waits)
	}
	cfg, err := loadConfig()
	if err != nil || cfg.APIKey != "ak_new" || cfg.KeyID != "key_1" || cfg.ExpiresAt != "2099-01-01T00:00:00Z" || cfg.BaseURL != srv.URL {
		t.Fatalf("saved config = %+v, %v", cfg, err)
	}
	p, _ := configPath()
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config mode: %v %v", fi.Mode(), err)
	}
}

func TestLoginDeviceFlowDenied(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/device/code" {
			w.Write([]byte(`{"device_code":"d","user_code":"U","verification_uri":"v","expires_in":60,"interval":1}`))
			return
		}
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer srv.Close()
	oldSleep, oldTTY := sleep, stdinIsTTY
	sleep, stdinIsTTY = func(time.Duration) {}, func() bool { return true }
	defer func() { sleep, stdinIsTTY = oldSleep, oldTTY }()
	if _, err := runRoot([]string{"login", "--base-url", srv.URL, "--no-browser"}); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("want denied error, got %v", err)
	}
}

func TestLoginWithKeyAndLogout(t *testing.T) {
	isolateConfig(t)
	revoked := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/v1/keys/current" {
			revoked = r.Header.Get("x-api-key")
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	if _, err := runRoot([]string{"login", "--key", "ak_x", "--base-url", srv.URL}); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot([]string{"logout"})
	if err != nil || revoked != "ak_x" || !strings.Contains(out, "revoked") {
		t.Fatalf("logout: %v revoked=%q\n%s", err, revoked, out)
	}
	if cfg, _ := loadConfig(); cfg.APIKey != "" || cfg.BaseURL != srv.URL {
		t.Fatalf("key not removed: %+v", cfg)
	}

	// Server failure still removes the key locally, with a warning.
	runRoot([]string{"login", "--key", "ak_y", "--base-url", srv.URL + "/broken"})
	out, err = runRoot([]string{"logout"})
	if err != nil || !strings.Contains(out, "could not revoke") || !strings.Contains(out, "removed") {
		t.Fatalf("logout on failure: %v\n%s", err, out)
	}
	if cfg, _ := loadConfig(); cfg.APIKey != "" {
		t.Fatalf("key not removed after server failure: %+v", cfg)
	}
}

func TestConfigExpired(t *testing.T) {
	if !(config{ExpiresAt: "2000-01-01T00:00:00Z"}).expired() || (config{ExpiresAt: "2099-01-01T00:00:00Z"}).expired() || (config{}).expired() {
		t.Fatal("expired() wrong")
	}
}
