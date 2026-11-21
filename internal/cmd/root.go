package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	adviverse "github.com/Adviverse/go-sdk"
	"github.com/spf13/cobra"
)

var jsonOut bool

func newClient() (*adviverse.Client, error) {
	key := os.Getenv("ADVIVERSE_API_KEY")
	base := os.Getenv("ADVIVERSE_BASE_URL")
	if key == "" || base == "" {
		if cfg, err := loadConfig(); err == nil {
			if key == "" {
				key = cfg.APIKey
				if key != "" && cfg.expired() {
					fmt.Fprintf(os.Stderr, "warning: stored API key expired at %s; run `adviverse login` again\n", cfg.ExpiresAt)
				}
			}
			if base == "" {
				base = cfg.BaseURL
			}
		}
	}
	opts := []adviverse.Option{adviverse.WithAPIKey(key)}
	if base != "" {
		opts = append(opts, adviverse.WithBaseURL(base))
	}
	return adviverse.New(opts...)
}

type config struct {
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url"`
	KeyID     string `json:"key_id,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"` // RFC 3339
}

// expired reports whether the stored key's expires_at is in the past.
func (c config) expired() bool {
	t, err := time.Parse(time.RFC3339, c.ExpiresAt)
	return err == nil && time.Now().After(t)
}

func configPath() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "adviverse", "config.json"), nil
}

func loadConfig() (config, error) {
	var c config
	p, err := configPath()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func saveConfig(c config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(p), 0o700) // MkdirAll leaves an existing dir's mode alone
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(p, 0o600)
}

// printRaw prints a raw JSON response: compact with --json, pretty otherwise.
func printRaw(cmd *cobra.Command, raw json.RawMessage) {
	var buf bytes.Buffer
	if jsonOut {
		if len(raw) > 0 && json.Compact(&buf, raw) == nil {
			fmt.Fprintln(cmd.OutOrStdout(), buf.String())
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), string(raw))
		}
		return
	}
	if len(raw) == 0 || json.Indent(&buf, raw, "", "  ") != nil {
		fmt.Fprintln(cmd.OutOrStdout(), string(raw))
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), buf.String())
}

func NewRoot() *cobra.Command {
	root := &cobra.Command{Use: "adviverse", Short: "Adviverse API command-line client", SilenceUsage: true}
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "JSON output")
	root.AddCommand(newLoginCmd(), newLogoutCmd(), newApiCmd(), newVersionCmd(),
		newPropertiesCmd(), newAppsCmd(), newPlacementsCmd(), newCombosCmd(), newPlanCmd(), newApplyCmd(), newDoctorCmd())
	addResourceCmds(root)
	return root
}

// runRoot executes the CLI with args and captures stdout — used by tests.
func runRoot(args []string) (string, error) {
	root := NewRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}
