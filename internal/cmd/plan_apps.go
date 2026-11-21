package cmd

import (
	"fmt"
	"strings"
)

// specApp is an `apps:` entry in adviverse.yaml; identity is platform + bundleId.
type specApp struct {
	Platform          string          `yaml:"platform"`
	BundleID          string          `yaml:"bundleId"`
	Name              string          `yaml:"name"`
	Kind              string          `yaml:"kind"` // mobile_app (default) | ctv
	StoreURL          string          `yaml:"storeUrl"`
	AppStoreID        string          `yaml:"appStoreId"`
	DeveloperProperty string          `yaml:"developerProperty"` // property id or domain
	Placements        []specPlacement `yaml:"placements"`
}

type remoteApp struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	BundleID            string `json:"bundle_id"`
	Platform            string `json:"platform"`
	StoreURL            string `json:"store_url"`
	AppStoreID          string `json:"app_store_id"`
	DeveloperPropertyID string `json:"developer_property_id"`
	Status              string `json:"status"`
	VerificationStatus  string `json:"verification_status"`
}

func appKey(platform, bundle string) string {
	return strings.ToLower(strings.TrimSpace(platform)) + "/" + strings.TrimSpace(bundle)
}

func validateApps(path string, apps []specApp) error {
	for _, a := range apps {
		if a.Platform == "" || a.BundleID == "" || a.Name == "" {
			return fmt.Errorf("%s: every app needs platform, bundleId and name", path)
		}
		if !appPlatforms[strings.ToLower(a.Platform)] {
			return fmt.Errorf("%s: app %s: unknown platform %q", path, a.BundleID, a.Platform)
		}
		if a.Kind != "" && !isAppKind(a.Kind) {
			return fmt.Errorf("%s: app %s: kind must be mobile_app or ctv", path, a.BundleID)
		}
		for _, pl := range a.Placements {
			if pl.Name == "" || pl.Format == "" {
				return fmt.Errorf("%s: placements under app %s need name and format", path, a.BundleID)
			}
		}
	}
	return nil
}

// diffApps mirrors diffPlan for the apps block. developerProperty domains resolve against
// props; a domain not yet on the account is resolved at apply time (it may be created by
// the same plan), via change.DevRef.
func diffApps(s spec, apps []remoteApp, props []remoteProperty, pls []remotePlacement) []change {
	var out []change
	byKey := map[string]remoteApp{}
	for _, a := range apps {
		byKey[appKey(a.Platform, a.BundleID)] = a
	}
	propID := map[string]string{}
	for _, p := range props {
		propID[normDomain(p.Domain)] = p.ID
	}
	managed := map[string]bool{}
	for _, sa := range s.Apps {
		k := appKey(sa.Platform, sa.BundleID)
		managed[k] = true
		devID, devRef := sa.DeveloperProperty, ""
		if strings.Contains(devID, ".") {
			if devID = propID[normDomain(sa.DeveloperProperty)]; devID == "" {
				devRef = normDomain(sa.DeveloperProperty)
			}
		}
		ra, exists := byKey[k]
		if !exists {
			kind := sa.Kind
			if kind == "" {
				kind = "mobile_app"
			}
			body := map[string]any{"kind": kind, "name": sa.Name, "bundleId": strings.TrimSpace(sa.BundleID), "platform": strings.ToLower(sa.Platform)}
			setNonEmpty(body, "storeUrl", sa.StoreURL)
			setNonEmpty(body, "appStoreId", sa.AppStoreID)
			setNonEmpty(body, "developerPropertyId", devID)
			out = append(out, change{Action: "create", Kind: "app", Domain: k, Body: body, DevRef: devRef})
		} else {
			body := map[string]any{}
			var notes []string
			for _, f := range []struct{ key, want, have string }{
				{"name", sa.Name, ra.Name}, {"storeUrl", sa.StoreURL, ra.StoreURL}, {"appStoreId", sa.AppStoreID, ra.AppStoreID},
			} {
				if f.want != "" && f.want != f.have {
					body[f.key] = f.want
					notes = append(notes, fmt.Sprintf("%s %q -> %q", f.key, f.have, f.want))
				}
			}
			if devRef != "" || devID != "" && devID != ra.DeveloperPropertyID {
				setNonEmpty(body, "developerPropertyId", devID)
				notes = append(notes, fmt.Sprintf("developerProperty %q -> %q", ra.DeveloperPropertyID, sa.DeveloperProperty))
			}
			if len(notes) > 0 {
				out = append(out, change{Action: "update", Kind: "app", Domain: k, ID: ra.ID, Body: body, Notes: notes, DevRef: devRef})
			}
		}
		ownerID := ""
		if exists {
			ownerID = ra.ID
		}
		out = append(out, diffPlacements(k, ownerID, sa.Placements, pls, true)...)
	}
	for _, a := range apps {
		if k := appKey(a.Platform, a.BundleID); !managed[k] {
			out = append(out, change{Action: "unmanaged", Kind: "app", Domain: k, ID: a.ID})
		}
	}
	return out
}

func setNonEmpty(b map[string]any, k, v string) {
	if v != "" {
		b[k] = v
	}
}
