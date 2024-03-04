package cmd

import (
	"os"
	"path/filepath"
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
	getManifestPath := func(args []string) string {
		// Determine the path user wants to use for the manifest file
		manifestPath := ""
		if len(args) != 0 {
			manifestPath = args[0]
		}

		// If the path is empty, use the current working directory
		if manifestPath == "" {
			manifestPath = "."
		}

		// If the path is not a valid yml file, try the default names
		if !strings.HasSuffix(manifestPath, ".yml") || !strings.HasSuffix(manifestPath, ".yaml") {
			yml := filepath.Join(manifestPath, "pm3.yml")
			if _, err := os.Stat(yml); err == nil {
				manifestPath = yml
			} else {
				manifestPath = filepath.Join(manifestPath, "pm3.yaml")
			}
		}
		// Clean up the path and make it absolute if possible
		if abs, err := filepath.Abs(manifestPath); err == nil {
			manifestPath = abs
		}
		return filepath.Clean(manifestPath)
	}
	config.RootCommand.AddCommand(&cobra.Command{
		Use:     "preview [manifest]",
		Short:   "Previews the rendered manifest",
		Args:    cobra.MaximumNArgs(1),
		GroupID: refGroup("daemon", "Daemon Commands"),
		RunE: func(cmd *cobra.Command, args []string) error {
			var node *yaml.Node
			if err := lyml.Load(getManifestPath(args), &node); err != nil {
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
		Short:   "Start the pmesh node, optionally with a manifest file",
		Args:    cobra.MaximumNArgs(1),
		GroupID: refGroup("daemon", "Daemon Commands"),
		Run: func(cmd *cobra.Command, args []string) {
			// Validate the options.
			setuputil.RunSetupIf(true)
			session.Start(getManifestPath(args))
		},
	})

	config.RootCommand.AddCommand(&cobra.Command{
		Use:     "kill-orphan",
		Hidden:  true,
		Short:   "Kill all processes started by pmesh",
		Args:    cobra.MaximumNArgs(1),
		GroupID: refGroup("daemon", "Daemon Commands"),
		Run: func(cmd *cobra.Command, args []string) {
			service.KillOrphans()
		},
	})
}
