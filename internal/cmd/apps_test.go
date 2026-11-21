package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppsCreateResolvesDeveloperDomain(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/publisher/properties":
			// An app row whose bundle id looks like a domain must not match.
			w.Write([]byte(`[{"id":"a0","kind":"mobile_app","domain":"acme.com"},{"id":"p1","kind":"website","domain":"acme.com"}]`))
		case "POST /v1/publisher/apps":
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"id":"a1"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("ADVIVERSE_API_KEY", "k")
	t.Setenv("ADVIVERSE_BASE_URL", srv.URL)

	_, err := runRoot([]string{"apps", "create", "--platform", "ios", "--bundle", "com.acme.app", "--name", "Acme",
		"--developer-property", "ACME.com", "--app-store-id", "123"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"kind": "mobile_app", "name": "Acme", "bundleId": "com.acme.app", "platform": "ios",
		"appStoreId": "123", "developerPropertyId": "p1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("body = %v\nwant %v", got, want)
	}

	if _, err := runRoot([]string{"apps", "create", "--platform", "ios", "--bundle", "b", "--name", "n", "--developer-property", "nope.com"}); err == nil {
		t.Fatal("expected unknown domain error")
	}
	if _, err := runRoot([]string{"apps", "create", "--platform", "psp", "--bundle", "b", "--name", "n"}); err == nil {
		t.Fatal("expected bad platform error")
	}
}

func TestPlacementsCreateApp(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	t.Setenv("ADVIVERSE_API_KEY", "k")
	t.Setenv("ADVIVERSE_BASE_URL", srv.URL)
	if _, err := runRoot([]string{"placements", "create", "--app", "a1", "--name", "Inter", "--format", "interstitial"}); err != nil {
		t.Fatal(err)
	}
	if got["appId"] != "a1" || got["propertyId"] != nil {
		t.Fatalf("body = %v", got)
	}
	if _, err := runRoot([]string{"placements", "create", "--app", "a1", "--property", "p1", "--name", "n", "--format", "banner"}); err == nil {
		t.Fatal("expected error for both --app and --property")
	}
}

func TestDiffApps(t *testing.T) {
	s := spec{Apps: []specApp{
		{Platform: "iOS", BundleID: "com.acme.app", Name: "Acme 2", DeveloperProperty: "acme.com",
			Placements: []specPlacement{{Name: "Inter", Format: "interstitial"}}},
		{Platform: "android", BundleID: "com.acme.app", Name: "Acme", DeveloperProperty: "new.com",
			Placements: []specPlacement{{Name: "Banner", Format: "banner"}}},
	}}
	apps := []remoteApp{
		{ID: "a1", Platform: "ios", BundleID: "com.acme.app", Name: "Acme", DeveloperPropertyID: "p1"},
		{ID: "a9", Platform: "roku", BundleID: "ch1"},
	}
	props := []remoteProperty{{ID: "p1", Domain: "acme.com"}}
	pls := []remotePlacement{{ID: "l1", PropertyID: "a1", Name: "Inter", Format: "interstitial"}, {ID: "l2", PropertyID: "a1", Name: "Old"}}
	cs := diffApps(s, apps, props, pls)
	want := []string{"update app ios/com.acme.app", "unmanaged placement ios/com.acme.app/Old",
		"create app android/com.acme.app", "create placement android/com.acme.app/Banner", "unmanaged app roku/ch1"}
	if got := summarize(cs); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if cs[0].Body["name"] != "Acme 2" || cs[0].Body["developerPropertyId"] != nil {
		t.Fatalf("update body = %v (dev property unchanged must not be sent)", cs[0].Body)
	}
	if cs[2].DevRef != "new.com" || !cs[3].App {
		t.Fatalf("create app: DevRef=%q app=%v", cs[2].DevRef, cs[3].App)
	}
}

func TestApplyAppsBlock(t *testing.T) {
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/publisher/properties":
			w.Write([]byte(`[{"id":"a1","kind":"mobile_app","domain":"com.acme.old"}]`))
		case "GET /v1/publisher/apps":
			w.Write([]byte(`[{"id":"a1","kind":"mobile_app","platform":"ios","bundle_id":"com.acme.old","name":"Old"}]`))
		case "GET /v1/publisher/placements":
			w.Write([]byte(`[]`))
		case "POST /v1/publisher/properties":
			posts = append(posts, "property")
			w.Write([]byte(`{"id":"p9"}`))
		case "POST /v1/publisher/apps":
			posts = append(posts, fmt.Sprintf("app dev=%v", b["developerPropertyId"]))
			w.Write([]byte(`{"id":"a2"}`))
		case "POST /v1/publisher/placements":
			posts = append(posts, fmt.Sprintf("placement app=%v prop=%v", b["appId"], b["propertyId"]))
			w.Write([]byte(`{"id":"x"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("ADVIVERSE_API_KEY", "k")
	t.Setenv("ADVIVERSE_BASE_URL", srv.URL)
	file := filepath.Join(t.TempDir(), "adviverse.yaml")
	os.WriteFile(file, []byte(`properties:
  - domain: acme.com
apps:
  - platform: ios
    bundleId: com.acme.app
    name: Acme
    developerProperty: acme.com
    placements:
      - {name: Inter, format: interstitial}
`), 0o600)

	out, err := runRoot([]string{"plan", "-f", file})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+ create app ios/com.acme.app", "+ create placement ios/com.acme.app/Inter", "? unmanaged app ios/com.acme.old", "Plan: 3 to create"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in plan:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unmanaged property com.acme.old") {
		t.Fatalf("app rows must not show as properties:\n%s", out)
	}
	if _, err := runRoot([]string{"apply", "-f", file}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(posts, ","); got != "property,app dev=p9,placement app=a2 prop=<nil>" {
		t.Fatalf("apply posts: %s", got)
	}
}

func TestDoctorApp(t *testing.T) {
	devSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# app-ads\nadviverse.com, PUB-1, DIRECT, f08c47fec0942fa0\n"))
	}))
	defer devSite.Close()
	devHost := strings.TrimPrefix(devSite.URL, "http://")
	var fillQuery url.Values
	serve := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fillQuery = r.URL.Query()
		w.Header().Set("X-Adviverse-NoFill", "no_eligible_campaigns")
		w.WriteHeader(204)
	}))
	defer serve.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/publisher/apps/a1":
			w.Write([]byte(`{"id":"a1","platform":"ios","bundle_id":"com.acme.app","status":"approved","verification_status":"verified","developer_property_id":"p1"}`))
		case "/v1/publisher/apps/a1/app-ads.txt":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("adviverse.com, pub-1, DIRECT\n"))
		case "/v1/publisher/properties":
			w.Write([]byte(`[{"id":"p1","domain":"` + devHost + `"}]`))
		case "/v1/publisher/placements":
			w.Write([]byte(`[{"id":"l1","property_id":"a1","name":"Inter","format":"interstitial","status":"active","tag_key":"tk_app"},
				{"id":"l2","property_id":"a1","name":"Paused","format":"banner","status":"paused","tag_key":"tk_2"},
				{"id":"l3","property_id":"zz","name":"Other","status":"active","tag_key":"tk_3"}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	t.Setenv("ADVIVERSE_API_KEY", "k")
	t.Setenv("ADVIVERSE_BASE_URL", api.URL)
	oldServe, oldScheme := serveBase, appAdsScheme
	serveBase, appAdsScheme = serve.URL, "http"
	defer func() { serveBase, appAdsScheme = oldServe, oldScheme }()

	out, err := runRoot([]string{"doctor", "--app", "a1"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"ok    app ios/com.acme.app is verified",
		"ok    developer property " + devHost,
		"has the seller line \"adviverse.com, pub-1, direct\"",
		"ok    placement Inter (interstitial) is active",
		"warn  placement Paused (banner) is paused",
		"warn  tag tk_app: no fill (no_eligible_campaigns)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Other") || fillQuery.Get("tag") != "tk_app" || fillQuery.Get("client") != "app" ||
		fillQuery.Get("bundle") != "com.acme.app" || fillQuery.Get("platform") != "ios" {
		t.Errorf("bad fill probe %v:\n%s", fillQuery, out)
	}
}
