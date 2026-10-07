package cmd

import (
	"context"

	"github.com/spf13/cobra"
)

func getCmd(use, short, path string) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := newClient()
			if err != nil {
				return err
			}
			res, err := cl.Do(context.Background(), "GET", path, nil, nil)
			if err != nil {
				return err
			}
			printRaw(cmd, res)
			return nil
		},
	}
}

// addResourceCmds wires a few curated verbs on top of the generic api command.
func addResourceCmds(root *cobra.Command) {
	campaigns := &cobra.Command{Use: "campaigns", Short: "Manage campaigns"}
	campaigns.AddCommand(getCmd("list", "List campaigns", "/v1/advertiser/campaigns"))
	report := &cobra.Command{Use: "report", Short: "Reporting"}
	report.AddCommand(getCmd("summary", "Account reporting summary", "/v1/reporting/advertiser"))
	root.AddCommand(campaigns, report)
}
