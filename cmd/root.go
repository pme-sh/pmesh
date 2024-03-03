package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/pme-sh/pmesh/client"
	"github.com/pme-sh/pmesh/config"
	"github.com/pme-sh/pmesh/pmtp"

	"github.com/spf13/cobra"
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
	if err := config.RootCommand.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
