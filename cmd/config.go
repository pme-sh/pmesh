package cmd

import (
	"encoding/json"
	"log"
	"strings"

	"get.pme.sh/pmesh/config"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	// Add the config commands
	//
	getCmd := &cobra.Command{
		Use:     "get",
		Short:   "Get the pmesh node configuration",
		GroupID: refGroup("daemon", "Daemon Commands"),
	}
	setCmd := &cobra.Command{
		Use:     "set",
		Short:   "Set the pmesh node configuration",
		GroupID: refGroup("daemon", "Daemon Commands"),
	}
	dumpCmd := &cobra.Command{
		Use:   "all",
		Short: "Display all configuration",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			var res []byte
			if cmd.Flag("json").Value.String() == "true" {
				res, _ = json.Marshal(config.Get())
			} else {
				res, _ = yaml.Marshal(config.Get())
			}
			cmd.Println(string(res))
		},
	}
	dumpCmd.Flags().Bool("json", false, "Output in JSON format")
	getCmd.AddCommand(dumpCmd)
	config.RootCommand.AddCommand(getCmd, setCmd)

	// Add the top-level config settings
	//
	getset := func(name string, get func(*config.Config) any, set func(*config.Config, string) error) {
		setCmd.AddCommand(&cobra.Command{
			Use:   name + " [value]",
			Short: "Set the " + name + " for the pmesh node",
			Args:  cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				err := config.Update(func(s *config.Config) error {
					return set(s, args[0])
				})
				if err != nil {
					log.Fatal(err)
				}
			},
		})
		getCmd.AddCommand(&cobra.Command{
			Use:   name,
			Short: "Get the " + name + " for the pmesh node",
			Args:  cobra.NoArgs,
			Run: func(cmd *cobra.Command, args []string) {
				val := get(config.Get())
				cmd.Println(val)
			},
		})
	}
	strAccessor := func(name string, field func(*config.Config) *string) {
		getset(
			name,
			func(s *config.Config) any { return *field(s) },
			func(s *config.Config, v string) error { *field(s) = v; return nil },
		)
	}
	strAccessor("secret", func(ss *config.Config) *string { return &ss.Secret })
	strAccessor("host", func(ss *config.Config) *string { return &ss.Host })
	strAccessor("cluster", func(ss *config.Config) *string { return &ss.Cluster })
	strAccessor("advertised", func(ss *config.Config) *string { return &ss.Advertised })

	// Add the arbitrary PeerUD/LocalUD command
	//
	addTable := func(name, desc string, ref func(*config.Config) map[string]any) {
		setCmd.AddCommand(&cobra.Command{
			Use:   name + " [field] [value]",
			Short: "Set the " + desc + " field",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				field := args[0]
				valueStr := strings.Join(args[1:], " ")
				var value any
				err := yaml.Unmarshal([]byte(valueStr), &value)
				if err != nil {
					return err
				}
				err = config.Update(func(s *config.Config) error {
					t := ref(s)
					if value == nil {
						delete(t, field)
					} else {
						t[field] = value
					}
					return nil
				})
				if err != nil {
					log.Fatal(err)
				}
				return nil
			},
		})
		getCmd.AddCommand(&cobra.Command{
			Use:   name + " [field]",
			Short: "Get the " + desc + " field",
			Args:  cobra.MaximumNArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				val := ref(config.Get())
				var v any
				if len(args) == 0 {
					v = val
				} else {
					v = val[args[0]]
				}
				cmd.Println(string(lo.Must(json.Marshal(v))))
			},
		})
	}
	addTable("peer-ud", "peer user data", func(s *config.Config) map[string]any { return s.PeerUD })
	addTable("local-ud", "local user data", func(s *config.Config) map[string]any { return s.LocalUD })

}
