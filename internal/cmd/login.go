package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var key, base string
	c := &cobra.Command{
		Use: "login", Short: "Store an API key for future calls",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if key == "" {
				fmt.Fprint(cmd.OutOrStdout(), "API key: ")
				r := bufio.NewReader(os.Stdin)
				line, _ := r.ReadString('\n')
				key = strings.TrimSpace(line)
			}
			if key == "" {
				return fmt.Errorf("no api key provided")
			}
			if base == "" {
				base = "https://adviverse.com"
			}
			if err := saveConfig(config{APIKey: key, BaseURL: base}); err != nil {
				return err
			}
			p, _ := configPath()
			fmt.Fprintf(cmd.OutOrStdout(), "saved to %s\n", p)
			return nil
		},
	}
	c.Flags().StringVar(&key, "key", "", "API key (else prompt)")
	c.Flags().StringVar(&base, "base-url", "", "API base URL")
	return c
}
