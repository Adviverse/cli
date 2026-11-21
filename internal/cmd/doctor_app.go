package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

var appAdsScheme = "https" // var so tests can point at a plain-http httptest server

// sellerFields returns the lowercased domain, seller id and relationship of the first
// adviverse.com record in an ads.txt-format body, or nil.
func sellerFields(body string) []string {
	for _, l := range strings.Split(body, "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		f := strings.Split(l, ",")
		if len(f) < 3 {
			continue
		}
		for i := range f {
			f[i] = strings.ToLower(strings.TrimSpace(f[i]))
		}
		if f[0] == "adviverse.com" {
			return f[:3]
		}
	}
	return nil
}

// hasSellerLine reports whether body has an adviverse.com record matching want (domain, id, relationship).
func hasSellerLine(body string, want []string) bool {
	for _, l := range strings.Split(body, "\n") {
		if got := sellerFields(l); got != nil && strings.Join(got, ",") == strings.Join(want, ",") {
			return true
		}
	}
	return false
}

func runAppDoctor(r *report, id string) error {
	cl, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	raw, err := cl.Do(ctx, "GET", "/v1/publisher/apps/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return err
	}
	var app remoteApp
	if err := json.Unmarshal(raw, &app); err != nil {
		return fmt.Errorf("decode app: %w", err)
	}
	label := app.Platform + "/" + app.BundleID

	// 1. Ownership verification + approval.
	if app.VerificationStatus == "verified" {
		r.line("ok", "app %s is verified", label)
	} else {
		r.line("fail", "app %s verification is %q (adviverse apps verify %s)", label, app.VerificationStatus, id)
	}
	if app.Status != "" && app.Status != "approved" {
		r.line("fail", "app %s is %s, not approved", label, app.Status)
	}

	// 2. Developer property + its app-ads.txt.
	if domain := devDomain(r, app); domain != "" {
		txt, err := cl.Do(ctx, "GET", "/v1/publisher/apps/"+url.PathEscape(id)+"/app-ads.txt", nil, nil)
		if want := sellerFields(string(txt)); err != nil || want == nil {
			r.line("warn", "could not read the expected app-ads.txt seller line: %v", err)
		} else {
			checkAppAdsTxt(r, domain, want, id)
		}
	}

	// 3. Placements + a test fill for the first one.
	var all []remotePlacement
	if err := listInto("/v1/publisher/placements", &all); err != nil {
		r.line("warn", "could not list placements: %v", err)
		return nil
	}
	var tag string
	for _, pl := range all {
		if pl.PropertyID != app.ID {
			continue
		}
		if pl.Status == "active" {
			r.line("ok", "placement %s (%s) is active", pl.Name, pl.Format)
		} else {
			r.line("warn", "placement %s (%s) is %s", pl.Name, pl.Format, pl.Status)
		}
		if tag == "" {
			tag = pl.TagKey
		}
	}
	if tag == "" {
		r.line("fail", "no placements on this app (adviverse placements create --app %s --name ... --format ...)", id)
		return nil
	}
	q := url.Values{"tag": {tag}, "client": {"app"}, "bundle": {app.BundleID}, "platform": {app.Platform}}
	probeFill(r, tag, serveBase+"/mobile/ad?"+q.Encode(), nil)
	return nil
}

// devDomain reports on the app's developer property and returns its domain ("" if unusable).
func devDomain(r *report, app remoteApp) string {
	if app.DeveloperPropertyID == "" {
		r.line("fail", "no developer property set (adviverse apps update %s --developer-property <domain>)", app.ID)
		return ""
	}
	var props []remoteProperty
	if err := listInto("/v1/publisher/properties", &props); err != nil {
		r.line("warn", "could not list properties: %v", err)
		return ""
	}
	for _, p := range props {
		if p.ID == app.DeveloperPropertyID {
			r.line("ok", "developer property %s", p.Domain)
			return normDomain(p.Domain)
		}
	}
	r.line("fail", "developer property %s is not on this account", app.DeveloperPropertyID)
	return ""
}

func checkAppAdsTxt(r *report, domain string, want []string, id string) {
	u := appAdsScheme + "://" + domain + "/app-ads.txt"
	line := strings.Join(want, ", ")
	resp, body, err := get(u, nil)
	switch {
	case err != nil:
		r.line("fail", "app-ads.txt: %v", err)
	case resp.StatusCode != 200:
		r.line("fail", "app-ads.txt: HTTP %d at %s", resp.StatusCode, u)
	case hasSellerLine(body, want):
		r.line("ok", "%s has the seller line %q", u, line)
	default:
		r.line("fail", "%s lacks the seller line %q (see: adviverse apps app-ads-txt %s)", u, line, id)
	}
}
