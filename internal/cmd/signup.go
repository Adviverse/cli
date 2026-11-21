package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type agentSignupReply struct {
	Account struct {
		ID     string `json:"id"`
		Email  string `json:"email"`
		Status string `json:"status"`
	} `json:"account"`
	APIKey struct {
		ID        string   `json:"id"`
		Key       string   `json:"key"`
		Scopes    []string `json:"scopes"`
		ExpiresAt string   `json:"expiresAt"`
	} `json:"apiKey"`
	Next    string `json:"next"`
	Message any    `json:"message"`
}

// signup: headless account creation for agents (no captcha, browser or password).
func newSignupCmd() *cobra.Command {
	var email, typ, company, country, base, scopes string
	var days int
	c := &cobra.Command{
		Use:   "signup",
		Short: "Create an account headlessly and store its API key (no browser)",
		Long: "Creates an Adviverse account from just an email and saves the returned API key, so an\n" +
			"agent can continue with properties/apps/placements right away. The owner gets a\n" +
			"verification email; payouts wait until it is clicked.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if email == "" {
				return fmt.Errorf("--email is required")
			}
			if base == "" {
				base = defaultBase
			}
			body := map[string]any{"email": email, "expiresInDays": days}
			for k, v := range map[string]string{"type": typ, "companyName": company, "country": country} {
				if v != "" {
					body[k] = v
				}
			}
			var list []string
			for _, s := range strings.Split(scopes, ",") {
				if s = strings.TrimSpace(s); s != "" {
					list = append(list, s)
				}
			}
			body["scopes"] = list
			var r agentSignupReply
			status, err := postPublic(base, "/v1/auth/agent-signup", body, &r)
			if err != nil {
				return err
			}
			if status >= 300 || r.APIKey.Key == "" {
				return fmt.Errorf("signup failed (HTTP %d): %v", status, r.Message)
			}
			if err := saveConfig(config{APIKey: r.APIKey.Key, BaseURL: base, KeyID: r.APIKey.ID, ExpiresAt: r.APIKey.ExpiresAt}); err != nil {
				return err
			}
			p, _ := configPath()
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Account %s (%s, %s)\nKey %s, scopes %s, expires %s — saved to %s\n",
				r.Account.ID, r.Account.Email, r.Account.Status, r.APIKey.ID, strings.Join(r.APIKey.Scopes, ","), r.APIKey.ExpiresAt, p)
			if r.Next != "" {
				fmt.Fprintln(w, r.Next)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&email, "email", "", "owner email (receives the verification link)")
	f.StringVar(&typ, "type", "", "advertiser | publisher (default landing side; every account is both)")
	f.StringVar(&company, "company", "", "company name")
	f.StringVar(&country, "country", "", "ISO country code")
	f.StringVar(&base, "base-url", "", "API base URL (default "+defaultBase+")")
	f.StringVar(&scopes, "scopes", "read,write", "comma-separated scopes for the key")
	f.IntVar(&days, "expires", 90, "key lifetime in days")
	return c
}
