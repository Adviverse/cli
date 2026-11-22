package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var appPlatforms = map[string]bool{
	"ios": true, "android": true, "roku": true, "firetv": true, "appletv": true, "androidtv": true, "web": true,
	"chrome": true, "edge": true, "firefox": true, // browser extensions
}

// isAppKind reports whether a properties row is an app (apps share the properties table).
func isAppKind(k string) bool { return k == "mobile_app" || k == "ctv" }

// resolvePropertyRef turns a property id or a site domain into a property id.
// Ids never contain dots; anything with a dot is treated as a domain.
func resolvePropertyRef(ref string) (string, error) {
	if !strings.Contains(ref, ".") {
		return ref, nil
	}
	var props []remoteProperty
	if err := listInto("/v1/publisher/properties", &props); err != nil {
		return "", err
	}
	d := normDomain(ref)
	for _, p := range props {
		if !isAppKind(p.Kind) && normDomain(p.Domain) == d {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("no property with domain %s on this account (adviverse properties create --domain %s)", d, d)
}

func newAppsCmd() *cobra.Command {
	c := &cobra.Command{Use: "apps", Short: "Manage mobile app and CTV inventory"}
	c.AddCommand(getCmd("list", "List apps", "/v1/publisher/apps"),
		callCmd("get <id>", "Show an app", "GET", 1, idPath("/v1/publisher/apps/%s"), nil),
		callCmd("delete <id>", "Delete an app", "DELETE", 1, idPath("/v1/publisher/apps/%s"), nil),
		callCmd("app-ads-txt <id>", "Print the app-ads.txt lines for an app's developer site", "GET", 1, idPath("/v1/publisher/apps/%s/app-ads.txt"), nil),
	)

	var platform, bundle, name, kind, storeURL, storeID, devProp string
	create := callCmd("create", "Register an app", "POST", 0, fixed("/v1/publisher/apps"), func(cmd *cobra.Command) (any, error) {
		if platform == "" || bundle == "" || name == "" {
			return nil, fmt.Errorf("--platform, --bundle and --name are required")
		}
		if !appPlatforms[platform] {
			return nil, fmt.Errorf("--platform must be one of ios, android, roku, firetv, appletv, androidtv, web, chrome, edge, firefox")
		}
		if kind != "mobile_app" && kind != "ctv" {
			return nil, fmt.Errorf("--kind must be mobile_app or ctv")
		}
		b := map[string]any{"kind": kind, "name": name, "bundleId": bundle, "platform": platform}
		return b, appFields(cmd, b, storeURL, storeID, devProp)
	})
	f := create.Flags()
	f.StringVar(&platform, "platform", "", "ios|android|roku|firetv|appletv|androidtv|web|chrome|edge|firefox")
	f.StringVar(&bundle, "bundle", "", "bundle id / package name (e.g. com.acme.app)")
	f.StringVar(&name, "name", "", "display name")
	f.StringVar(&kind, "kind", "mobile_app", "mobile_app|ctv")
	appFlags(create, &storeURL, &storeID, &devProp)

	var uName, uStoreURL, uStoreID, uDevProp string
	update := callCmd("update <id>", "Update an app", "PATCH", 1, idPath("/v1/publisher/apps/%s"), func(cmd *cobra.Command) (any, error) {
		b := map[string]any{}
		setIfChanged(cmd, b, "name", "name", uName)
		if err := appFields(cmd, b, uStoreURL, uStoreID, uDevProp); err != nil {
			return nil, err
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("nothing to update (pass --name, --store-url, --app-store-id or --developer-property)")
		}
		return b, nil
	})
	update.Flags().StringVar(&uName, "name", "", "display name")
	appFlags(update, &uStoreURL, &uStoreID, &uDevProp)

	var method string
	verify := callCmd("verify <id>", "Check app ownership via the developer site or store listing", "POST", 1, idPath("/v1/publisher/apps/%s/verify"), func(cmd *cobra.Command) (any, error) {
		b := map[string]any{}
		if method != "" && method != "developer_site" && method != "store_listing" {
			return nil, fmt.Errorf("--method must be developer_site or store_listing")
		}
		setIfChanged(cmd, b, "method", "method", method)
		return b, nil
	})
	verify.Flags().StringVar(&method, "method", "", "developer_site|store_listing")

	c.AddCommand(create, update, verify)
	return c
}

func appFlags(c *cobra.Command, storeURL, storeID, devProp *string) {
	f := c.Flags()
	f.StringVar(storeURL, "store-url", "", "App Store / Play / channel store URL")
	f.StringVar(storeID, "app-store-id", "", "numeric App Store id / store product id")
	f.StringVar(devProp, "developer-property", "", "developer site: property id or domain (hosts app-ads.txt)")
}

// appFields copies the optional app flags the user passed into b.
func appFields(cmd *cobra.Command, b map[string]any, storeURL, storeID, devProp string) error {
	setIfChanged(cmd, b, "store-url", "storeUrl", storeURL)
	setIfChanged(cmd, b, "app-store-id", "appStoreId", storeID)
	if devProp != "" {
		id, err := resolvePropertyRef(devProp)
		if err != nil {
			return err
		}
		b["developerPropertyId"] = id
	}
	return nil
}
