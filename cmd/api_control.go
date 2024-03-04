package cmd

import (
	"fmt"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/ui"

	"github.com/spf13/cobra"
)

func init() {

	config.RootCommand.AddCommand(&cobra.Command{
		Use:     "shutdown",
		Short:   "Shuts down the pmesh node",
		GroupID: refGroup("svct", "Management"),
		Run: func(_ *cobra.Command, args []string) {
			cli := getClient()
			res := ui.SpinnyWait("Sending request...", func() (string, error) {
				return "Applied", cli.Shutdown()
			})
			fmt.Println(ui.RenderOkLine(res))
		},
	})

	reloadcmd := &cobra.Command{
		Use:     "reload",
		Short:   "Reloads the manifest, restarts all services",
		GroupID: refGroup("svct", "Management"),
	}
	inval := reloadcmd.PersistentFlags().BoolP("invalidate", "i", false, "Invalidates cached builds")
	reloadcmd.Run = func(_ *cobra.Command, args []string) {
		cli := getClient()
		res := ui.SpinnyWait("Reloading...", func() (string, error) {
			return "Done", cli.Reload(*inval)
		})
		fmt.Println(ui.RenderOkLine(res))
	}
	config.RootCommand.AddCommand(reloadcmd)
}
