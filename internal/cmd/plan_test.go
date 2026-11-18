package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }
func bptr(v bool) *bool      { return &v }

// summarize renders changes as "action kind domain/name" for compact comparison.
func summarize(cs []change) []string {
	var out []string
	for _, c := range cs {
		s := fmt.Sprintf("%s %s %s", c.Action, c.Kind, c.Domain)
		if c.Name != "" {
			s += "/" + c.Name
		}
		out = append(out, s)
	}
	return out
}

func TestDiffPlan(t *testing.T) {
	remoteProps := []remoteProperty{
		{ID: "p1", Domain: "example.com", Name: "Example", VerificationMethod: "dns"},
		{ID: "p2", Domain: "other.com"},
	}
	remotePls := []remotePlacement{
		{ID: "l1", PropertyID: "p1", Name: "Sidebar", Format: "banner", Width: 300, Height: 250, FloorCpm: 0.5},
		{ID: "l2", PropertyID: "p1", Name: "Old", Format: "native"},
		{ID: "l3", PropertyID: "p2", Name: "Sidebar", Format: "banner"},
	}
	sidebar := specPlacement{Name: "Sidebar", Format: "banner", Width: 300, Height: 250, FloorCpm: f64(0.5)}

	tests := []struct {
		name    string
		spec    spec
		want    []string
		pending int
	}{
		{
			name: "in sync; extras are unmanaged, never deleted",
			spec: spec{Properties: []specProperty{{Domain: "Example.com", Name: "Example", VerifyMethod: "dns", Placements: []specPlacement{sidebar}}}},
			want: []string{"unmanaged placement example.com/Old", "unmanaged property other.com"},
		},
		{
			name: "new property creates property and its placements",
			spec: spec{Properties: []specProperty{{Domain: "new.com", Placements: []specPlacement{sidebar}}}},
			want: []string{"create property new.com", "create placement new.com/Sidebar",
				"unmanaged property example.com", "unmanaged property other.com"},
			pending: 2,
		},
		{
			name: "floor change is an update; name+method update property",
			spec: spec{Properties: []specProperty{{Domain: "example.com", Name: "Renamed", VerifyMethod: "meta",
				Placements: []specPlacement{{Name: "Sidebar", Format: "banner", FloorCpm: f64(1.25)}, {Name: "Old", Format: "native"}}}}},
			want:    []string{"update property example.com", "update placement example.com/Sidebar", "unmanaged property other.com"},
			pending: 2,
		},
		{
			name: "format/size/cap changes are drift, not pending",
			spec: spec{Properties: []specProperty{{Domain: "example.com",
				Placements: []specPlacement{
					{Name: "Sidebar", Format: "native", Width: 728, Height: 90, Responsive: bptr(true), FrequencyCap: &freqCap{3, 3600}},
					{Name: "Old", Format: "native"},
				}}}},
			want: []string{"drift placement example.com/Sidebar", "unmanaged property other.com"},
		},
		{
			name: "placement names are matched within the property only",
			spec: spec{Properties: []specProperty{{Domain: "other.com", Placements: []specPlacement{sidebar, {Name: "New", Format: "banner"}}}}},
			want: []string{"update placement other.com/Sidebar", "drift placement other.com/Sidebar",
				"create placement other.com/New", "unmanaged property example.com"},
			pending: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := diffPlan(tt.spec, remoteProps, remotePls)
			got := summarize(cs)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			if p := pending(cs); p != tt.pending {
				t.Fatalf("pending = %d, want %d", p, tt.pending)
			}
		})
	}
}

func TestDiffPlanBodies(t *testing.T) {
	cs := diffPlan(spec{Properties: []specProperty{{Domain: "example.com", VerifyMethod: "meta",
		Placements: []specPlacement{{Name: "Sidebar", Format: "banner", FloorCpm: f64(2)}}}}},
		[]remoteProperty{{ID: "p1", Domain: "example.com", VerificationMethod: "dns"}},
		[]remotePlacement{{ID: "l1", PropertyID: "p1", Name: "Sidebar", Format: "banner", FloorCpm: 0.5}})
	if len(cs) != 2 || cs[0].Body["verificationMethod"] != "meta" || cs[1].Body["floorCpm"] != 2.0 || cs[1].ID != "l1" {
		t.Fatalf("unexpected changes: %+v", cs)
	}
	if _, ok := cs[0].Body["name"]; ok {
		t.Fatal("unset name must not be sent")
	}
}

func TestFlexFloat(t *testing.T) {
	var p remotePlacement
	if err := json.Unmarshal([]byte(`{"floor_cpm":"0.5000","frequency_cap":{"impressions":1,"windowSeconds":86400}}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.FloorCpm != 0.5 || p.FrequencyCap == nil || p.FrequencyCap.WindowSeconds != 86400 {
		t.Fatalf("bad decode: %+v", p)
	}
}

func TestPlanApplyEndToEnd(t *testing.T) {
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/publisher/properties":
			w.Write([]byte(`[{"id":"p1","domain":"example.com","name":"Example"}]`))
		case "GET /v1/publisher/placements":
			w.Write([]byte(`[]`))
		case "POST /v1/publisher/properties":
			posts = append(posts, "property")
			w.Write([]byte(`{"id":"p9","domain":"new.com"}`))
		case "POST /v1/publisher/placements":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			posts = append(posts, fmt.Sprintf("placement %v", b["propertyId"]))
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
  - domain: new.com
    placements:
      - {name: Top, format: banner, width: 728, height: 90}
`), 0o600)

	out, err := runRoot([]string{"plan", "-f", file, "--exit-code"})
	if ec, ok := err.(ExitCodeError); !ok || ec.Code != 2 {
		t.Fatalf("want exit 2, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "+ create property new.com") || !strings.Contains(out, "? unmanaged property example.com") {
		t.Fatalf("plan output: %s", out)
	}
	if _, err := runRoot([]string{"apply", "-f", file}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(posts, ",") != "property,placement p9" {
		t.Fatalf("apply posts: %v", posts)
	}
}

func TestLoadSpecRejectsUnknownKeys(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.yaml")
	os.WriteFile(file, []byte("properties:\n  - domain: a.com\n    floor: 1\n"), 0o600)
	if _, err := loadSpec(file); err == nil {
		t.Fatal("expected error for unknown key")
	}
}
