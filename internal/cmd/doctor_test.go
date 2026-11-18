package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSPProblems(t *testing.T) {
	tests := []struct {
		name, policy, want string
	}{
		{"empty policy allows everything", "", ""},
		{"unrelated directives only", "frame-ancestors 'none'; upgrade-insecure-requests", ""},
		{"explicit hosts", "script-src 'self' https://serve.adviverse.com; connect-src serve.adviverse.com; img-src https://cdn.adviverse.com data:", ""},
		{"wildcard subdomain", "default-src 'self' https://*.adviverse.com data:", ""},
		{"scheme source", "default-src https: data:", ""},
		{"star", "default-src *; img-src * data:", ""},
		{"default-src fallback blocks all", "default-src 'self'", "script-src serve.adviverse.com, connect-src serve.adviverse.com, img-src cdn.adviverse.com, img-src data:"},
		{"missing data: in img-src", "script-src serve.adviverse.com; connect-src serve.adviverse.com; img-src cdn.adviverse.com", "img-src data:"},
		{"none", "script-src 'none'", "script-src serve.adviverse.com"},
		{"connect-src missing while script ok", "default-src 'self' data: cdn.adviverse.com; script-src serve.adviverse.com", "connect-src serve.adviverse.com"},
		{"first duplicate directive wins", "script-src 'self'; script-src serve.adviverse.com", "script-src serve.adviverse.com"},
		{"wrong scheme host", "default-src http://serve.adviverse.com https://cdn.adviverse.com data:", "script-src serve.adviverse.com, connect-src serve.adviverse.com"},
		{"lookalike domain", "default-src *.adviverse.com.evil.io data:", "script-src serve.adviverse.com, connect-src serve.adviverse.com, img-src cdn.adviverse.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Join(cspProblems(tt.policy), ", "); got != tt.want {
				t.Fatalf("cspProblems(%q) = %q, want %q", tt.policy, got, tt.want)
			}
		})
	}
}

func TestIsSDKScript(t *testing.T) {
	custom := map[string]bool{"ads.example.com": true}
	tests := []struct {
		src  string
		want bool
	}{
		{"https://serve.adviverse.com/sdk/v1.js", true},
		{"https://serve.adviverse.com/sdk.js?v=2", true},
		{"https://ads.example.com/sdk/v1.js", true},
		{"https://evil.com/sdk/v1.js", false},
		{"https://serve.adviverse.com/pixel.js", false},
	}
	for _, tt := range tests {
		if got := isSDKScript(tt.src, custom); got != tt.want {
			t.Errorf("isSDKScript(%q) = %v, want %v", tt.src, got, tt.want)
		}
	}
}

func TestDoctorEndToEnd(t *testing.T) {
	t.Setenv("ADVIVERSE_API_KEY", "")
	t.Setenv("ADVIVERSE_BASE_URL", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var gotOrigin string
	serve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin = r.Header.Get("Origin")
		if r.URL.Query().Get("tag") == "tk_fill" {
			w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("X-Adviverse-NoFill", "no_eligible_campaigns")
		w.WriteHeader(204)
	}))
	defer serve.Close()
	old := serveBase
	serveBase = serve.URL
	defer func() { serveBase = old }()

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ads.txt" {
			w.Write([]byte("google.com, pub-1, DIRECT\nadviverse.com, abc, DIRECT\n"))
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Write([]byte(`<html><script async src="https://serve.adviverse.com/sdk/v1.js"></script>
<div data-tag="tk_fill"></div><div data-tag='tk_empty'></div><div data-tag="tk_fill"></div></html>`))
	}))
	defer site.Close()

	out, err := runRoot([]string{"doctor", "--site", site.URL})
	if ec, ok := err.(ExitCodeError); !ok || ec.Code != 1 {
		t.Fatalf("want exit 1 (CSP fail), got %v\n%s", err, out)
	}
	for _, want := range []string{
		"ok    Adviverse SDK script present once",
		"fail  CSP blocks the SDK",
		"ok    ads.txt has an adviverse.com line",
		"ok    tag tk_fill serves an ad",
		"warn  tag tk_empty: no fill (no_eligible_campaigns)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "tk_fill") != 1 {
		t.Errorf("duplicate tag probed twice:\n%s", out)
	}
	if gotOrigin != site.URL {
		t.Errorf("Origin = %q, want %q", gotOrigin, site.URL)
	}
}
