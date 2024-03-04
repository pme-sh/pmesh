package cmd

import (
	"log"

	"get.pme.sh/pmesh/client"
	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/pmtp"
	"get.pme.sh/pmesh/revision"
	"get.pme.sh/pmesh/ui"

	"runtime"

	"github.com/spf13/cobra"
	"go.uber.org/automaxprocs/maxprocs"
)

var optURL = config.RootCommand.PersistentFlags().StringP(
	"url", "R",
	"",
	"Specifies the node URL for the command if relevant",
)

func refGroup(id, name string) string {
	if !config.RootCommand.ContainsGroup(id) {
		config.RootCommand.AddGroup(&cobra.Group{
			ID:    id,
			Title: name + ":",
		})
	}
	return id
}

func getClient() client.Client {
	url := *optURL
	if url == "" {
		url = pmtp.DefaultURL
	}
	cli, err := client.ConnectTo(url)
	if err != nil {
		log.Fatal("Failed to connect to pmesh node: ", err)
	}
	return cli
}
func getClientIf() client.Client {
	if *optURL == "" {
		return client.Client{}
	}
	return getClient()

}

func Execute() {
	if runtime.GOMAXPROCS(0) > 32 {
		runtime.GOMAXPROCS(32)
	}
	maxprocs.Set()
	config.RootCommand.Short += ui.FaintStyle.Render(" (" + revision.GetVersion() + ")")
	if err := config.RootCommand.Execute(); err != nil {
		ui.ExitWithError(err)
	}
}
