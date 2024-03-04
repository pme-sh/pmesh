package cmd

import (
	"fmt"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/ui"

	"github.com/spf13/cobra"
)

func init() {
	config.RootCommand.AddCommand(&cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List services",
		Args:    cobra.NoArgs,
		GroupID: refGroup("ctrl", "Service"),
		Run: func(cmd *cobra.Command, args []string) {
			ui.Run(ui.MakeServiceListModel(getClient()))
		},
	})
	config.RootCommand.AddCommand(&cobra.Command{
		Use:     "view [service]",
		Short:   "Show service details",
		Aliases: []string{"inspect", "info", "show"},
		Args:    cobra.MaximumNArgs(1),
		GroupID: refGroup("ctrl", "Service"),
		Run: func(cmd *cobra.Command, args []string) {
			cli := getClient()
			var svc string
			if len(args) == 0 {
				svc = ui.PromptSelectService(cli)
			} else {
				svc = args[0]
			}
			ui.Run(ui.MakeServiceDetailModel(cli, &ui.ServiceItem{
				Name: svc,
			}))
		},
	})

	for _, cmd := range ui.ServiceControls {
		config.RootCommand.AddCommand(&cobra.Command{
			Use:     cmd.Use,
			Short:   cmd.Short,
			Aliases: cmd.Aliases,
			Args:    cobra.MaximumNArgs(1),
			GroupID: refGroup("ctrl", "Service"),
			Run: func(_ *cobra.Command, args []string) {
				cli := getClient()
				var svc string
				if len(args) == 0 {
					svc = ui.PromptSelectService(cli)
				} else {
					svc = args[0]
				}

				res := ui.SpinnyWait(cmd.WaitMsg, func() (string, error) {
					return cmd.Do(cli, svc)
				})
				fmt.Println(ui.RenderOkLine(res))
			},
		})
	}
}
