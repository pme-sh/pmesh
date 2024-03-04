package cmd

import (
	"os"
	"strings"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/lyml"
	"get.pme.sh/pmesh/service"
	"get.pme.sh/pmesh/session"
	"get.pme.sh/pmesh/setuputil"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	config.RootCommand.AddCommand(&cobra.Command{
		Use:     "preview [manifest]",
		Short:   "Previews the rendered manifest",
		Args:    cobra.MaximumNArgs(1),
		GroupID: refGroup("daemon", "Daemon"),
		RunE: func(cmd *cobra.Command, args []string) error {
			var node *yaml.Node
			if err := lyml.Load(session.GetManifestPathFromArgs(args), &node); err != nil {
				return err
			}
			buf := &strings.Builder{}
			enc := yaml.NewEncoder(buf)
			enc.SetIndent(2)
			enc.Encode(node)
			lines := strings.Split(buf.String(), "\n")
			buf.Reset()
			for _, line := range lines {
				before, _, _ := strings.Cut(line, "#")
				if strings.TrimSpace(before) == "" {
					continue
				}
				buf.WriteString(before)
				buf.WriteByte('\n')
			}

			if err := quick.Highlight(os.Stdout, buf.String(), "yaml", "terminal256", "monokai"); err != nil {
				cmd.Println(buf.String())
			}
			return nil
		},
	})

	config.RootCommand.AddCommand(&cobra.Command{
		Use:     "go [manifest]",
		Short:   "Start the pmesh node with a manifest",
		Args:    cobra.MaximumNArgs(1),
		GroupID: refGroup("daemon", "Daemon"),
		Run: func(cmd *cobra.Command, args []string) {
			setuputil.RunSetupIf(true)
			session.Run(args)
		},
	})

	config.RootCommand.AddCommand(&cobra.Command{
		Use:     "kill-orphan",
		Hidden:  true,
		Short:   "Kill all processes started by pmesh",
		Args:    cobra.MaximumNArgs(1),
		GroupID: refGroup("daemon", "Daemon"),
		Run: func(cmd *cobra.Command, args []string) {
			service.KillOrphans()
		},
	})
}
