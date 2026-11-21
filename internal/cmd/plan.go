package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ---- adviverse.yaml ----

type freqCap struct {
	Impressions   int `yaml:"impressions" json:"impressions"`
	WindowSeconds int `yaml:"windowSeconds" json:"windowSeconds"`
}

type specPlacement struct {
	Name         string   `yaml:"name"`
	Format       string   `yaml:"format"`
	Width        int      `yaml:"width"`
	Height       int      `yaml:"height"`
	Responsive   *bool    `yaml:"responsive"`
	FloorCpm     *float64 `yaml:"floorCpm"`
	FrequencyCap *freqCap `yaml:"frequencyCap"`
}

type specProperty struct {
	Domain       string          `yaml:"domain"`
	Name         string          `yaml:"name"`
	VerifyMethod string          `yaml:"verifyMethod"`
	Placements   []specPlacement `yaml:"placements"`
}

type spec struct {
	Properties []specProperty `yaml:"properties"`
	Apps       []specApp      `yaml:"apps"`
}

func loadSpec(path string) (spec, error) {
	var s spec
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true) // typos in keys fail loudly instead of silently doing nothing
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	for _, p := range s.Properties {
		if p.Domain == "" {
			return s, fmt.Errorf("%s: every property needs a domain", path)
		}
		for _, pl := range p.Placements {
			if pl.Name == "" || pl.Format == "" {
				return s, fmt.Errorf("%s: placements under %s need name and format", path, p.Domain)
			}
		}
	}
	return s, validateApps(path, s.Apps)
}

// ---- remote rows (snake_case DB rows from the API) ----

type remoteProperty struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Domain             string `json:"domain"`
	Name               string `json:"name"`
	VerificationMethod string `json:"verification_method"`
	Status             string `json:"status"`
	ServingDomain      string `json:"serving_domain"`
}

// flexFloat accepts both 0.5 and "0.5000" (Postgres numeric arrives as a string).
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	*f = flexFloat(v)
	return err
}

type remotePlacement struct {
	ID           string    `json:"id"`
	PropertyID   string    `json:"property_id"`
	Name         string    `json:"name"`
	Format       string    `json:"format"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	Responsive   bool      `json:"responsive"`
	FloorCpm     flexFloat `json:"floor_cpm"`
	FrequencyCap *freqCap  `json:"frequency_cap"`
	Status       string    `json:"status"`
	TagKey       string    `json:"tag_key"`
}

// ---- diff ----

type change struct {
	Action string // create | update | drift | unmanaged
	Kind   string // property | placement
	Domain string // owning property domain
	Name   string // placement name (empty for properties)
	ID     string // remote id for update
	Body   map[string]any
	Notes  []string // human-readable field diffs
	App    bool     // placement belongs to an app (Domain is the app key platform/bundle)
	DevRef string   // app developer property domain, resolved to an id at apply time
}

func normDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

func capStr(c *freqCap) string {
	if c == nil {
		return "none"
	}
	return fmt.Sprintf("%d/%ds", c.Impressions, c.WindowSeconds)
}

// diffPlan compares the desired spec to remote state. It never proposes deletes.
func diffPlan(s spec, props []remoteProperty, pls []remotePlacement) []change {
	var out []change
	byDomain := map[string]remoteProperty{}
	for _, p := range props {
		byDomain[normDomain(p.Domain)] = p
	}
	managed := map[string]bool{}
	for _, sp := range s.Properties {
		d := normDomain(sp.Domain)
		managed[d] = true
		rp, exists := byDomain[d]
		if !exists {
			body := map[string]any{"domain": d}
			if sp.Name != "" {
				body["name"] = sp.Name
			}
			if sp.VerifyMethod != "" {
				body["verificationMethod"] = sp.VerifyMethod
			}
			out = append(out, change{Action: "create", Kind: "property", Domain: d, Body: body})
		} else {
			body := map[string]any{}
			var notes []string
			if sp.Name != "" && sp.Name != rp.Name {
				body["name"] = sp.Name
				notes = append(notes, fmt.Sprintf("name %q -> %q", rp.Name, sp.Name))
			}
			if sp.VerifyMethod != "" && sp.VerifyMethod != rp.VerificationMethod {
				body["verificationMethod"] = sp.VerifyMethod
				notes = append(notes, fmt.Sprintf("verifyMethod %q -> %q (resets verification)", rp.VerificationMethod, sp.VerifyMethod))
			}
			if len(body) > 0 {
				out = append(out, change{Action: "update", Kind: "property", Domain: d, ID: rp.ID, Body: body, Notes: notes})
			}
		}

		ownerID := ""
		if exists {
			ownerID = rp.ID
		}
		out = append(out, diffPlacements(d, ownerID, sp.Placements, pls, false)...)
	}
	for _, p := range props {
		if !managed[normDomain(p.Domain)] {
			out = append(out, change{Action: "unmanaged", Kind: "property", Domain: normDomain(p.Domain), ID: p.ID})
		}
	}
	return out
}

// diffPlacements diffs the placements wanted under one owner (property or app, keyed
// by owner; ownerID empty when the owner is still to be created) against remote rows.
func diffPlacements(owner, ownerID string, want []specPlacement, pls []remotePlacement, app bool) []change {
	var out []change
	existing := map[string]remotePlacement{}
	for _, pl := range pls {
		if ownerID != "" && pl.PropertyID == ownerID {
			existing[pl.Name] = pl
		}
	}
	wanted := map[string]bool{}
	for _, sp := range want {
		wanted[sp.Name] = true
		rpl, ok := existing[sp.Name]
		if !ok {
			out = append(out, change{Action: "create", Kind: "placement", Domain: owner, Name: sp.Name, Body: placementBody(sp)})
			continue
		}
		out = append(out, diffPlacement(owner, sp, rpl)...)
	}
	for _, pl := range pls {
		if ownerID != "" && pl.PropertyID == ownerID && !wanted[pl.Name] {
			out = append(out, change{Action: "unmanaged", Kind: "placement", Domain: owner, Name: pl.Name, ID: pl.ID})
		}
	}
	for i := range out {
		out[i].App = app
	}
	return out
}

func placementBody(p specPlacement) map[string]any {
	b := map[string]any{"name": p.Name, "format": p.Format}
	if p.Width > 0 {
		b["width"] = p.Width
	}
	if p.Height > 0 {
		b["height"] = p.Height
	}
	if p.Responsive != nil {
		b["responsive"] = *p.Responsive
	}
	if p.FloorCpm != nil {
		b["floorCpm"] = *p.FloorCpm
	}
	if p.FrequencyCap != nil {
		b["frequencyCap"] = p.FrequencyCap
	}
	return b
}

// diffPlacement: floorCpm is PATCHable; format/size/responsive/frequencyCap
// are fixed at creation by the API, so differences there are reported as drift.
func diffPlacement(d string, want specPlacement, have remotePlacement) []change {
	var out []change
	if want.FloorCpm != nil && math.Abs(*want.FloorCpm-float64(have.FloorCpm)) > 1e-9 {
		out = append(out, change{Action: "update", Kind: "placement", Domain: d, Name: want.Name, ID: have.ID,
			Body:  map[string]any{"floorCpm": *want.FloorCpm},
			Notes: []string{fmt.Sprintf("floorCpm %g -> %g", float64(have.FloorCpm), *want.FloorCpm)}})
	}
	var drift []string
	if want.Format != have.Format {
		drift = append(drift, fmt.Sprintf("format %s -> %s", have.Format, want.Format))
	}
	if want.Width > 0 && want.Width != have.Width || want.Height > 0 && want.Height != have.Height {
		drift = append(drift, fmt.Sprintf("size %dx%d -> %dx%d", have.Width, have.Height, want.Width, want.Height))
	}
	if want.Responsive != nil && *want.Responsive != have.Responsive {
		drift = append(drift, fmt.Sprintf("responsive %v -> %v", have.Responsive, *want.Responsive))
	}
	if want.FrequencyCap != nil && (have.FrequencyCap == nil || *want.FrequencyCap != *have.FrequencyCap) {
		drift = append(drift, fmt.Sprintf("frequencyCap %s -> %s", capStr(have.FrequencyCap), capStr(want.FrequencyCap)))
	}
	if len(drift) > 0 {
		out = append(out, change{Action: "drift", Kind: "placement", Domain: d, Name: want.Name, ID: have.ID, Notes: drift})
	}
	return out
}

// pending counts the changes apply would perform.
func pending(cs []change) int {
	n := 0
	for _, c := range cs {
		if c.Action == "create" || c.Action == "update" {
			n++
		}
	}
	return n
}

func label(c change) string {
	if c.Kind == "property" || c.Kind == "app" {
		return c.Kind + " " + c.Domain
	}
	return "placement " + c.Domain + "/" + c.Name
}

func printPlan(cmd *cobra.Command, cs []change) {
	w := cmd.OutOrStdout()
	sym := map[string]string{"create": "+", "update": "~", "drift": "!", "unmanaged": "?"}
	creates, updates := 0, 0
	for _, c := range cs {
		line := fmt.Sprintf("%s %s %s", sym[c.Action], c.Action, label(c))
		switch c.Action {
		case "create":
			creates++
		case "update":
			updates++
		case "drift":
			line += " (not updatable via API; recreate to change)"
		case "unmanaged":
			line += " (not in file; left untouched)"
		}
		fmt.Fprintln(w, line)
		for _, n := range c.Notes {
			fmt.Fprintln(w, "    "+n)
		}
	}
	fmt.Fprintf(w, "Plan: %d to create, %d to update, 0 to delete.\n", creates, updates)
}

// ExitCodeError makes main exit with Code without printing an error.
type ExitCodeError struct{ Code int }

func (e ExitCodeError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

type state struct {
	props []remoteProperty // websites only
	apps  []remoteApp
	pls   []remotePlacement
}

// fetchState loads remote state; apps are only fetched when the spec manages any.
func fetchState(s spec) (state, error) {
	var st state
	var all []remoteProperty
	if err := listInto("/v1/publisher/properties", &all); err != nil {
		return st, err
	}
	for _, p := range all {
		if !isAppKind(p.Kind) {
			st.props = append(st.props, p)
		}
	}
	if len(s.Apps) > 0 {
		if err := listInto("/v1/publisher/apps", &st.apps); err != nil {
			return st, err
		}
	}
	if err := listInto("/v1/publisher/placements", &st.pls); err != nil {
		return st, err
	}
	return st, nil
}

func (st state) diff(s spec) []change {
	return append(diffPlan(s, st.props, st.pls), diffApps(s, st.apps, st.props, st.pls)...)
}

func newPlanCmd() *cobra.Command {
	var file string
	var exitCode bool
	c := &cobra.Command{
		Use: "plan", Short: "Show what apply would change for adviverse.yaml", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := loadSpec(file)
			if err != nil {
				return err
			}
			st, err := fetchState(s)
			if err != nil {
				return err
			}
			cs := st.diff(s)
			printPlan(cmd, cs)
			if exitCode && pending(cs) > 0 {
				cmd.SilenceErrors = true
				return ExitCodeError{2}
			}
			return nil
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "adviverse.yaml", "config file")
	c.Flags().BoolVar(&exitCode, "exit-code", false, "exit 2 when there are changes")
	return c
}

func newApplyCmd() *cobra.Command {
	var file string
	c := &cobra.Command{
		Use: "apply", Short: "Create/update properties, apps and placements from adviverse.yaml (never deletes)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := loadSpec(file)
			if err != nil {
				return err
			}
			st, err := fetchState(s)
			if err != nil {
				return err
			}
			cs := st.diff(s)
			printPlan(cmd, cs)
			cl, err := newClient()
			if err != nil {
				return err
			}
			ids := map[string]string{}
			for _, p := range st.props {
				ids[normDomain(p.Domain)] = p.ID
			}
			for _, a := range st.apps {
				ids[appKey(a.Platform, a.BundleID)] = a.ID
			}
			ctx := context.Background()
			// Owner changes precede their placements (and properties precede apps) in cs,
			// so new ids are known in time.
			for _, c := range cs {
				var path, method string
				body := c.Body
				if c.DevRef != "" {
					if ids[c.DevRef] == "" {
						return fmt.Errorf("%s: developerProperty %s is not a property on this account or in this file", label(c), c.DevRef)
					}
					body = map[string]any{"developerPropertyId": ids[c.DevRef]}
					for k, v := range c.Body {
						body[k] = v
					}
				}
				switch {
				case c.Action == "create" && c.Kind == "app":
					method, path = "POST", "/v1/publisher/apps"
				case c.Action == "update" && c.Kind == "app":
					method, path = "PATCH", "/v1/publisher/apps/"+url.PathEscape(c.ID)
				case c.Action == "create" && c.Kind == "property":
					method, path = "POST", "/v1/publisher/properties"
				case c.Action == "update" && c.Kind == "property":
					method, path = "PATCH", "/v1/publisher/properties/"+url.PathEscape(c.ID)
				case c.Action == "create" && c.Kind == "placement":
					owner := "propertyId"
					if c.App {
						owner = "appId"
					}
					body = map[string]any{owner: ids[c.Domain]}
					for k, v := range c.Body {
						body[k] = v
					}
					method, path = "POST", "/v1/publisher/placements"
				case c.Action == "update" && c.Kind == "placement":
					method, path = "PATCH", "/v1/publisher/placements/"+url.PathEscape(c.ID)
				default:
					continue
				}
				res, err := cl.Do(ctx, method, path, nil, body)
				if err != nil {
					return fmt.Errorf("%s %s: %w", c.Action, label(c), err)
				}
				if c.Action == "create" && (c.Kind == "property" || c.Kind == "app") {
					var row remoteProperty
					if err := json.Unmarshal(res, &row); err != nil || row.ID == "" {
						return fmt.Errorf("create %s: no id in response", label(c))
					}
					ids[c.Domain] = row.ID
				}
				fmt.Fprintf(cmd.OutOrStdout(), "done: %s %s\n", c.Action, label(c))
			}
			return nil
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "adviverse.yaml", "config file")
	return c
}
