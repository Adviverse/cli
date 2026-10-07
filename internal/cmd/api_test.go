package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApiCommandSendsKeyAndPrints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k_cli" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"path":"` + r.URL.Path + `"}`))
	}))
	defer srv.Close()
	t.Setenv("ADVIVERSE_API_KEY", "k_cli")
	t.Setenv("ADVIVERSE_BASE_URL", srv.URL)

	out, err := runRoot([]string{"api", "healthz", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `{"ok":true,"path":"/healthz"}`) {
		t.Fatalf("expected compact output with --json: %s", out)
	}
	out, err = runRoot([]string{"api", "healthz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("expected pretty output by default: %s", out)
	}
}

func TestApiCommandMissingKeyErrors(t *testing.T) {
	t.Setenv("ADVIVERSE_API_KEY", "")
	t.Setenv("ADVIVERSE_BASE_URL", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := runRoot([]string{"api", "healthz"}); err == nil {
		t.Fatal("expected auth error without a key")
	}
}

func TestApiCommandServerErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()
	t.Setenv("ADVIVERSE_API_KEY", "k_cli")
	t.Setenv("ADVIVERSE_BASE_URL", srv.URL)
	_, err := runRoot([]string{"api", "something"})
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") && !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error lacks status/message: %v", err)
	}
}
