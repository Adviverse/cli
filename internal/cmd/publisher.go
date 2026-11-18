package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

// Version is the build version, set via -ldflags "-X github.com/Adviverse/cli/internal/cmd.Version=v1.2.3".
var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Print the CLI version", Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) { fmt.Fprintln(cmd.OutOrStdout(), Version) },
	}
}

var comboPresets = map[string]bool{
	"standard_combo": true, "leaderboard_pop": true, "social_push": true,
	"sticky_interstitial": true, "full_monetization": true,
}

// callCmd builds a command that sends method to the path built from its args, with an optional body.
func callCmd(use, short, method string, nargs int, path func(args []string) string, body func(cmd *cobra.Command) (any, error)) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(nargs),
		RunE: func(cmd *cobra.Command, args []string) error {
			var b any
			if body != nil {
				var err error
				if b, err = body(cmd); err != nil {
					return err
				}
			}
			cl, err := newClient()
			if err != nil {
				return err
			}
			res, err := cl.Do(context.Background(), method, path(args), nil, b)
			if err != nil {
				return err
			}
			printRaw(cmd, res)
			return nil
		},
	}
}

func idPath(format string) func([]string) string {
	return func(a []string) string { return fmt.Sprintf(format, url.PathEscape(a[0])) }
}

func fixed(p string) func([]string) string { return func([]string) string { return p } }

// setIfChanged copies only flags the user passed, so server defaults apply otherwise.
func setIfChanged(cmd *cobra.Command, body map[string]any, flag, key string, val any) {
	if cmd.Flags().Changed(flag) {
		body[key] = val
	}
}

func newPropertiesCmd() *cobra.Command {
	c := &cobra.Command{Use: "properties", Short: "Manage publisher properties (sites)"}
	c.AddCommand(getCmd("list", "List properties", "/v1/publisher/properties"))

	var domain, name, method string
	create := callCmd("create", "Create a property", "POST", 0, fixed("/v1/publisher/properties"), func(cmd *cobra.Command) (any, error) {
		if domain == "" {
			return nil, fmt.Errorf("--domain is required")
		}
		if method != "" && method != "dns" && method != "file" && method != "meta" {
			return nil, fmt.Errorf("--verify-method must be dns, file or meta")
		}
		b := map[string]any{"domain": domain}
		setIfChanged(cmd, b, "name", "name", name)
		setIfChanged(cmd, b, "verify-method", "verificationMethod", method)
		return b, nil
	})
	create.Flags().StringVar(&domain, "domain", "", "site domain (e.g. example.com)")
	create.Flags().StringVar(&name, "name", "", "display name")
	create.Flags().StringVar(&method, "verify-method", "", "ownership verification: dns|file|meta")

	c.AddCommand(create,
		callCmd("verify <id>", "Check domain ownership (returns verified, token, method)", "POST", 1, idPath("/v1/publisher/properties/%s/verify"), nil),
		callCmd("ads-txt <id>", "Print the ads.txt lines for a property", "GET", 1, idPath("/v1/publisher/properties/%s/ads.txt"), nil),
	)
	return c
}

func newPlacementsCmd() *cobra.Command {
	c := &cobra.Command{Use: "placements", Short: "Manage ad placements"}
	c.AddCommand(getCmd("list", "List placements", "/v1/publisher/placements"))

	var prop, name, format string
	var width, height int
	var responsive bool
	var floor float64
	create := callCmd("create", "Create a placement", "POST", 0, fixed("/v1/publisher/placements"), func(cmd *cobra.Command) (any, error) {
		if prop == "" || name == "" || format == "" {
			return nil, fmt.Errorf("--property, --name and --format are required")
		}
		b := map[string]any{"propertyId": prop, "name": name, "format": format}
		setIfChanged(cmd, b, "width", "width", width)
		setIfChanged(cmd, b, "height", "height", height)
		setIfChanged(cmd, b, "responsive", "responsive", responsive)
		setIfChanged(cmd, b, "floor", "floorCpm", floor)
		return b, nil
	})
	f := create.Flags()
	f.StringVar(&prop, "property", "", "property id")
	f.StringVar(&name, "name", "", "placement name")
	f.StringVar(&format, "format", "", "format (banner, native, popunder, ...)")
	f.IntVar(&width, "width", 0, "width in px")
	f.IntVar(&height, "height", 0, "height in px")
	f.BoolVar(&responsive, "responsive", false, "serve any creative size that fits the container")
	f.Float64Var(&floor, "floor", 0, "floor CPM")

	c.AddCommand(create,
		callCmd("tag <id>", "Print the embed tag (format, tagKey, script)", "GET", 1, idPath("/v1/publisher/placements/%s/tag"), nil),
	)
	return c
}

func newCombosCmd() *cobra.Command {
	c := &cobra.Command{Use: "combos", Short: "Provision multi-format placement bundles"}
	var prop, preset string
	create := callCmd("create", "Create a combo from a preset", "POST", 0,
		func([]string) string { return fmt.Sprintf("/v1/publisher/properties/%s/combos", url.PathEscape(prop)) },
		func(*cobra.Command) (any, error) {
			if prop == "" {
				return nil, fmt.Errorf("--property is required")
			}
			if !comboPresets[preset] {
				return nil, fmt.Errorf("--preset must be one of standard_combo, leaderboard_pop, social_push, sticky_interstitial, full_monetization")
			}
			return map[string]any{"presetKey": preset}, nil
		})
	create.Flags().StringVar(&prop, "property", "", "property id")
	create.Flags().StringVar(&preset, "preset", "", "preset key")
	c.AddCommand(create)
	return c
}

// listInto GETs path and decodes the JSON array into out.
func listInto(path string, out any) error {
	cl, err := newClient()
	if err != nil {
		return err
	}
	res, err := cl.Do(context.Background(), "GET", path, nil, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(res, out)
}
