package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newApiCmd is the generic escape hatch: `adviverse api <path> --param k=v`.
func newApiCmd() *cobra.Command {
	var params []string
	c := &cobra.Command{
		Use:   "api <path>",
		Short: "Call any API path (e.g. 'api healthz', 'api v1/advertiser/campaigns')",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			path := args[0]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			query := map[string]string{}
			for _, kv := range params {
				i := strings.IndexByte(kv, '=')
				if i <= 0 {
					return fmt.Errorf("invalid --param %q, expected k=v", kv)
				}
				query[kv[:i]] = kv[i+1:]
			}
			res, err := cl.Do(context.Background(), "GET", path, query, nil)
			if err != nil {
				return err
			}
			printRaw(cmd, res)
			return nil
		},
	}
	c.Flags().StringArrayVar(&params, "param", nil, "query param k=v (repeatable)")
	return c
}
