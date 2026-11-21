package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	serveBase  = "https://serve.adviverse.com" // var so tests can point at httptest
	httpClient = &http.Client{Timeout: 15 * time.Second}

	scriptSrcRe = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)
	cspMetaRe   = regexp.MustCompile(`(?is)<meta\b[^>]*http-equiv\s*=\s*["']content-security-policy["'][^>]*>`)
	contentRe   = regexp.MustCompile(`(?is)\bcontent\s*=\s*["']([^"']*)["']`)
	dataTagRe   = regexp.MustCompile(`data-tag\s*=\s*["'](tk_[A-Za-z0-9_-]+)["']`)
	spaRootRe   = regexp.MustCompile(`(?i)id\s*=\s*["'](root|app|__next|__nuxt)["']|data-reactroot|@adviverse/react`)
)

type report struct {
	w     io.Writer
	fails int
}

func (r *report) line(level, format string, a ...any) {
	if level == "fail" {
		r.fails++
	}
	fmt.Fprintf(r.w, "%-4s  %s\n", level, fmt.Sprintf(format, a...))
}

func get(u string, hdr map[string]string) (*http.Response, string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "adviverse-cli/"+Version+" doctor")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp, string(b), nil
}

// isSDKScript reports whether src loads the Adviverse SDK: the official host, or
// /sdk/v1.js|/sdk.js on one of the account's custom serving domains.
func isSDKScript(src string, servingDomains map[string]bool) bool {
	u, err := url.Parse(src)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	sdkPath := u.Path == "/sdk/v1.js" || u.Path == "/sdk.js"
	return sdkPath && (host == "serve.adviverse.com" || servingDomains[host])
}

// cspSourceAllows checks one source list (e.g. "'self' https://serve.adviverse.com") for an
// origin like "https://serve.adviverse.com" or the scheme "data:".
func cspSourceAllows(sources []string, origin string) bool {
	if origin == "data:" {
		for _, s := range sources {
			if strings.EqualFold(s, "data:") {
				return true
			}
		}
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, s := range sources {
		s = strings.ToLower(s)
		if s == "*" || s == u.Scheme+":" {
			return true
		}
		s = strings.TrimPrefix(strings.TrimPrefix(s, u.Scheme+"://"), "//")
		s = strings.TrimSuffix(s, "/")
		if s == host || strings.HasPrefix(s, "*.") && strings.HasSuffix(host, s[1:]) {
			return true
		}
	}
	return false
}

// cspAllows reports whether policy permits origin for directive, falling back to default-src.
func cspAllows(policy, directive, origin string) bool {
	dirs := map[string][]string{}
	for _, part := range strings.Split(policy, ";") {
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		name := strings.ToLower(f[0])
		if _, dup := dirs[name]; !dup { // browsers ignore repeated directives
			dirs[name] = f[1:]
		}
	}
	src, ok := dirs[directive]
	if !ok {
		if src, ok = dirs["default-src"]; !ok {
			return true
		}
	}
	return cspSourceAllows(src, origin)
}

var cspNeeds = []struct{ directive, origin string }{
	{"script-src", "https://serve.adviverse.com"},
	{"connect-src", "https://serve.adviverse.com"},
	{"img-src", "https://cdn.adviverse.com"},
	{"img-src", "data:"},
}

// cspProblems lists what the policy blocks; empty means it allows the SDK.
func cspProblems(policy string) []string {
	var out []string
	for _, n := range cspNeeds {
		if !cspAllows(policy, n.directive, n.origin) {
			out = append(out, n.directive+" "+strings.TrimPrefix(n.origin, "https://"))
		}
	}
	return out
}

func newDoctorCmd() *cobra.Command {
	var site, app string
	c := &cobra.Command{
		Use: "doctor", Short: "Check a site's or app's Adviverse integration (SDK tag, CSP, ads.txt/app-ads.txt, placements)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (site == "") == (app == "") {
				return fmt.Errorf("pass exactly one of --site or --app")
			}
			r := &report{w: cmd.OutOrStdout()}
			if app != "" {
				if err := runAppDoctor(r, app); err != nil {
					return err
				}
			} else {
				if !strings.Contains(site, "://") {
					site = "https://" + site
				}
				su, err := url.Parse(site)
				if err != nil || su.Host == "" {
					return fmt.Errorf("invalid --site %q", site)
				}
				runDoctor(r, su)
			}
			if r.fails > 0 {
				cmd.SilenceErrors = true
				return ExitCodeError{1}
			}
			return nil
		},
	}
	c.Flags().StringVar(&site, "site", "", "site URL to check (e.g. https://example.com)")
	c.Flags().StringVar(&app, "app", "", "app id to check (verification, app-ads.txt, placements, test fill)")
	return c
}

func runDoctor(r *report, su *url.URL) {
	host := strings.ToLower(su.Hostname())
	origin := su.Scheme + "://" + su.Host

	// Account properties (optional): approval status + custom serving domains.
	servingDomains := map[string]bool{}
	var props []remoteProperty
	if _, err := newClient(); err == nil {
		if err := listInto("/v1/publisher/properties", &props); err != nil {
			r.line("warn", "could not list account properties: %v", err)
			props = nil
		}
		for _, p := range props {
			if p.ServingDomain != "" {
				servingDomains[strings.ToLower(p.ServingDomain)] = true
			}
		}
	}

	resp, html, err := get(su.String(), nil)
	if err != nil {
		r.line("fail", "fetch %s: %v", su, err)
		return
	}
	if resp.StatusCode >= 400 {
		r.line("fail", "fetch %s: HTTP %d", su, resp.StatusCode)
	}

	// 1. SDK script tag.
	n := 0
	for _, m := range scriptSrcRe.FindAllStringSubmatch(html, -1) {
		src := m[1]
		if strings.HasPrefix(src, "//") {
			src = "https:" + src
		}
		if isSDKScript(src, servingDomains) {
			n++
		}
	}
	switch {
	case n == 1:
		r.line("ok", "Adviverse SDK script present once")
	case n > 1:
		r.line("fail", "Adviverse SDK script included %d times (include it exactly once)", n)
	case spaRootRe.MatchString(html) || strings.Contains(html, `data-tag="tk_`):
		r.line("warn", "SDK script not in server HTML; fine if rendered client-side (@adviverse/react), otherwise add serve.adviverse.com/sdk/v1.js")
	default:
		r.line("fail", "no Adviverse SDK script found (expected https://serve.adviverse.com/sdk/v1.js)")
	}

	// 2. CSP (header and/or meta; every policy present must allow the SDK).
	var policies []string
	policies = append(policies, resp.Header.Values("Content-Security-Policy")...)
	for _, m := range cspMetaRe.FindAllString(html, -1) {
		if c := contentRe.FindStringSubmatch(m); c != nil {
			policies = append(policies, c[1])
		}
	}
	if len(policies) == 0 {
		r.line("ok", "no Content-Security-Policy (nothing to allow)")
	}
	for _, p := range policies {
		if probs := cspProblems(p); len(probs) > 0 {
			r.line("fail", "CSP blocks the SDK: needs %s", strings.Join(probs, ", "))
		} else {
			r.line("ok", "CSP allows serve.adviverse.com and cdn.adviverse.com")
		}
	}

	// 3. ads.txt.
	if aresp, body, err := get(origin+"/ads.txt", nil); err != nil {
		r.line("fail", "ads.txt: %v", err)
	} else if aresp.StatusCode != 200 {
		r.line("fail", "ads.txt: HTTP %d at %s/ads.txt", aresp.StatusCode, origin)
	} else if hasAdviverseLine(body) {
		r.line("ok", "ads.txt has an adviverse.com line")
	} else {
		r.line("fail", "ads.txt has no 'adviverse.com, ...' line (see: adviverse properties ads-txt <id>)")
	}

	// 4. Each tag key on the page serves.
	seen := map[string]bool{}
	for _, m := range dataTagRe.FindAllStringSubmatch(html, -1) {
		tag := m[1]
		if seen[tag] {
			continue
		}
		seen[tag] = true
		q := url.Values{"tag": {tag}, "client": {"web"}, "w": {"300"}, "h": {"250"}, "ac": {"0"}}
		probeFill(r, tag, serveBase+"/ad?"+q.Encode(), map[string]string{"Origin": origin})
	}
	if len(seen) == 0 {
		r.line("warn", "no data-tag=\"tk_...\" placements in server HTML")
	}

	// 5. Property approval (only with an API key).
	if props != nil {
		bare := strings.TrimPrefix(host, "www.")
		var match *remoteProperty
		for i := range props {
			if d := normDomain(props[i].Domain); d == host || d == bare {
				match = &props[i]
				break
			}
		}
		switch {
		case match == nil:
			r.line("fail", "%s is not a property on this account (adviverse properties create --domain %s)", host, bare)
		case match.Status != "approved":
			r.line("fail", "property %s is %s, not approved (adviverse properties verify %s)", match.Domain, match.Status, match.ID)
		default:
			r.line("ok", "property %s is approved", match.Domain)
		}
	}
}

func hasAdviverseLine(body string) bool {
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "adviverse.com,") {
			return true
		}
	}
	return false
}

// probeFill requests an ad for tag and reports fill, no-fill (with the engine's reason) or error.
func probeFill(r *report, tag, u string, hdr map[string]string) {
	aresp, _, err := get(u, hdr)
	switch {
	case err != nil:
		r.line("fail", "tag %s: %v", tag, err)
	case aresp.StatusCode == 200:
		r.line("ok", "tag %s serves an ad", tag)
	case aresp.StatusCode == 204:
		reason := aresp.Header.Get("X-Adviverse-NoFill")
		if reason == "" {
			reason = "no reason header: the request was treated as invalid traffic, usually because it came from a datacenter IP; run doctor from a normal connection"
		}
		r.line("warn", "tag %s: no fill (%s)", tag, reason)
	default:
		r.line("fail", "tag %s: HTTP %d", tag, aresp.StatusCode)
	}
}
