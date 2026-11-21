package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignupStoresKey(t *testing.T) {
	isolateConfig(t)
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/agent-signup" || r.Header.Get("x-api-key") != "" {
			t.Errorf("unexpected %s (key %q)", r.URL.Path, r.Header.Get("x-api-key"))
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(201)
		w.Write([]byte(`{"account":{"id":"acc1","email":"a@b.co","status":"pending"},
			"apiKey":{"id":"k1","key":"ak_x.y","scopes":["read","write"],"expiresAt":"2099-01-01T00:00:00Z"},"next":"check email"}`))
	}))
	defer srv.Close()
	out, err := runRoot([]string{"signup", "--email", "a@b.co", "--type", "publisher", "--base-url", srv.URL, "--expires", "30"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if body["email"] != "a@b.co" || body["type"] != "publisher" || body["expiresInDays"] != 30.0 {
		t.Fatalf("request body: %v", body)
	}
	cfg, _ := loadConfig()
	if cfg.APIKey != "ak_x.y" || cfg.KeyID != "k1" || cfg.BaseURL != srv.URL || !strings.Contains(out, "check email") {
		t.Fatalf("config %+v\n%s", cfg, out)
	}
}

func TestSignupFailure(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"message":"email already registered"}`))
	}))
	defer srv.Close()
	if _, err := runRoot([]string{"signup", "--email", "a@b.co", "--base-url", srv.URL}); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginUsesEnvKey(t *testing.T) {
	isolateConfig(t)
	t.Setenv("ADVIVERSE_API_KEY", "ak_env.k")
	if out, err := runRoot([]string{"login", "--base-url", "http://x"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if cfg, _ := loadConfig(); cfg.APIKey != "ak_env.k" {
		t.Fatalf("config %+v", cfg)
	}
}
