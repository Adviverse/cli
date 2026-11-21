package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	adviverse "github.com/Adviverse/go-sdk"
	"github.com/spf13/cobra"
)

const defaultBase = "https://adviverse.com"

// Swappable in tests.
var (
	sleep      = time.Sleep
	stdinIsTTY = func() bool {
		fi, err := os.Stdin.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice != 0
	}
	openBrowser = func(u string) error {
		switch runtime.GOOS {
		case "darwin":
			return exec.Command("open", u).Start()
		case "windows":
			return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
		default:
			return exec.Command("xdg-open", u).Start()
		}
	}
)

type deviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type deviceToken struct {
	APIKey    string   `json:"api_key"`
	KeyID     string   `json:"key_id"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at"`
	Error     string   `json:"error"`
}

// postPublic POSTs JSON to an unauthenticated endpoint and decodes a JSON reply into out.
// Non-2xx replies still decode (the device flow reports state as 400 {error}); err is set
// only when the request fails or the body is not JSON.
func postPublic(base, path string, body, out any) (int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", strings.TrimRight(base, "/")+path, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	req.Header.Set("User-Agent", "adviverse-cli/"+Version)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, fmt.Errorf("%s: HTTP %d: %s", path, resp.StatusCode, raw)
	}
	return resp.StatusCode, nil
}

// deviceLogin runs the RFC 8628 device authorization flow and returns the issued key.
func deviceLogin(cmd *cobra.Command, base string, scopes []string, days int, noBrowser bool) (deviceToken, error) {
	w := cmd.OutOrStdout()
	host, _ := os.Hostname()
	var dc struct {
		deviceCode
		Message any `json:"message"`
	}
	status, err := postPublic(base, "/v1/auth/device/code",
		map[string]any{"scopes": scopes, "expires_in_days": days, "client_name": "adviverse-cli on " + host}, &dc)
	if err != nil {
		return deviceToken{}, err
	}
	if status >= 300 || dc.DeviceCode == "" {
		return deviceToken{}, fmt.Errorf("device code request failed (HTTP %d): %v", status, dc.Message)
	}
	fmt.Fprintf(w, "To authorize this device, open:\n  %s\nand enter the code: %s\n", dc.VerificationURI, dc.UserCode)
	if !noBrowser && dc.VerificationURIComplete != "" {
		if openBrowser(dc.VerificationURIComplete) == nil {
			fmt.Fprintln(w, "(opened your browser)")
		}
	}
	fmt.Fprintln(w, "Waiting for approval...")
	interval := time.Duration(max(dc.Interval, 1)) * time.Second
	deadline := time.Now().Add(time.Duration(max(dc.ExpiresIn, 1)) * time.Second)
	for time.Now().Before(deadline) {
		sleep(interval)
		var tok deviceToken
		status, err := postPublic(base, "/v1/auth/device/token", map[string]any{"device_code": dc.DeviceCode}, &tok)
		switch {
		case status == 200 && tok.APIKey != "":
			return tok, nil
		case tok.Error == "authorization_pending":
		case tok.Error == "slow_down":
			interval += 5 * time.Second
		case tok.Error == "access_denied":
			return tok, fmt.Errorf("authorization was denied")
		case tok.Error == "expired_token":
			return tok, fmt.Errorf("the code expired; run `adviverse login` again")
		case err != nil:
			return tok, err
		default:
			return tok, fmt.Errorf("unexpected device token response (HTTP %d, error %q)", status, tok.Error)
		}
	}
	return deviceToken{}, fmt.Errorf("the code expired; run `adviverse login` again")
}

func newLoginCmd() *cobra.Command {
	var key, base, scopes string
	var days int
	var noBrowser bool
	c := &cobra.Command{
		Use:   "login",
		Short: "Authorize this machine (browser device flow) or store an API key",
		Long: "Without --key, opens a browser to approve a new scoped API key for this machine.\n" +
			"With --key <key> (or --key - / piped stdin) stores an existing key instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if base == "" {
				base = defaultBase
			}
			cfg := config{BaseURL: base}
			if key == "" {
				key = os.Getenv("ADVIVERSE_API_KEY") // agents: no prompt, no browser
			}
			if key == "-" || (key == "" && !stdinIsTTY()) {
				fmt.Fprint(cmd.ErrOrStderr(), "API key: ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				key = strings.TrimSpace(line)
				if key == "" {
					return fmt.Errorf("no api key provided")
				}
			}
			if key != "" {
				cfg.APIKey = key
			} else {
				var list []string
				for _, s := range strings.Split(scopes, ",") {
					if s = strings.TrimSpace(s); s != "" {
						list = append(list, s)
					}
				}
				tok, err := deviceLogin(cmd, base, list, days, noBrowser)
				if err != nil {
					return err
				}
				cfg.APIKey, cfg.KeyID, cfg.ExpiresAt = tok.APIKey, tok.KeyID, tok.ExpiresAt
				fmt.Fprintf(cmd.OutOrStdout(), "Logged in: key %s, scopes %s, expires %s\n",
					tok.KeyID, strings.Join(tok.Scopes, ","), tok.ExpiresAt)
			}
			if err := saveConfig(cfg); err != nil {
				return err
			}
			p, _ := configPath()
			fmt.Fprintf(cmd.OutOrStdout(), "saved to %s\n", p)
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&key, "key", "", "store this API key instead of the browser flow ('-' reads stdin)")
	f.StringVar(&base, "base-url", "", "API base URL (default "+defaultBase+")")
	f.StringVar(&scopes, "scopes", "read,write", "comma-separated scopes for the new key")
	f.IntVar(&days, "expires", 90, "key lifetime in days")
	f.BoolVar(&noBrowser, "no-browser", false, "print the URL instead of opening a browser")
	return c
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use: "logout", Short: "Revoke the stored API key and remove it from this machine", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil || cfg.APIKey == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "not logged in")
				return nil
			}
			w := cmd.OutOrStdout()
			cl, err := adviverse.New(adviverse.WithAPIKey(cfg.APIKey), adviverse.WithBaseURL(cfg.BaseURL))
			if err == nil {
				_, err = cl.Do(context.Background(), "DELETE", "/v1/keys/current", nil, nil)
			}
			if err != nil {
				fmt.Fprintf(w, "warning: could not revoke the key on the server (%v); revoke it in the dashboard\n", err)
			} else {
				fmt.Fprintln(w, "revoked the API key")
			}
			if err := saveConfig(config{BaseURL: cfg.BaseURL}); err != nil {
				return err
			}
			fmt.Fprintln(w, "removed the stored key")
			return nil
		},
	}
}
