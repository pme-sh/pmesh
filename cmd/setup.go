package cmd

import (
	"fmt"
	"strings"

	"github.com/pme-sh/pmesh/config"
	"github.com/pme-sh/pmesh/setuputil"
	"github.com/pme-sh/pmesh/ui"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
)

func init() {
	runsetup := func(mod func(*config.Config, []string) error) func(cmd *cobra.Command, args []string) {
		return func(cmd *cobra.Command, args []string) {
			err := config.Update(func(cfg *config.Config) error {
				mod(cfg, args)
				return setuputil.RunSetup(cfg, true)
			})
			if err != nil {
				ui.ExitWithError(err)
			}
		}
	}

	seetURL := &cobra.Command{
		Use:     "get-seed",
		Short:   "Get the seed URL",
		Args:    cobra.NoArgs,
		GroupID: refGroup("daemon", "Daemon Commands"),
		Run: func(cmd *cobra.Command, args []string) {
			cfg := config.Get()
			if cfg.Role == config.RoleNotSet {
				ui.ExitWithError("Setup not complete yet")
			}

			url := strings.Builder{}
			url.WriteString("pmtp://")
			url.WriteString(cfg.Secret)
			url.WriteString("@")

			hosts := map[string]struct{}{}
			for _, h := range cfg.Topology {
				for _, host := range h {
					if host != "" {
						hosts[host] = struct{}{}
					}
				}
			}
			if cfg.Advertised != "" && (cfg.Role == config.RoleSeed || cfg.Role == config.RoleNode) {
				hosts[cfg.Advertised] = struct{}{}
			}

			for i, host := range lo.Keys(hosts) {
				if i > 0 {
					url.WriteString(",")
				}
				url.WriteString(host)
			}
			fmt.Println(url.String())
		},
	}

	setupCmd := &cobra.Command{
		Use:     "setup",
		Short:   "Run the setup utility",
		GroupID: refGroup("daemon", "Daemon Commands"),
		Run: runsetup(func(c *config.Config, s []string) error {
			c.Role = config.RoleNotSet
			return nil
		}),
	}
	config.RootCommand.AddCommand(setupCmd, seetURL)
	setupCmd.AddCommand(
		&cobra.Command{
			Use:   "seed [name] [cluster] [secret]",
			Short: "Setup the server as a seed node",
			Args:  cobra.MaximumNArgs(3),
			Run: runsetup(func(c *config.Config, s []string) error {
				c.Role = config.RoleSeed
				if len(s) > 0 && s[0] != "auto" {
					c.Host = s[0]
				}
				if len(s) > 1 && s[1] != "auto" {
					c.Cluster = s[1]
				}
				if len(s) > 2 && s[2] != "auto" {
					c.Secret = s[2]
				}
				return nil
			}),
		},
		&cobra.Command{
			Use:   "join [seed] [name] [cluster]",
			Short: "Setup the server as a part of a mesh",
			Args:  cobra.MaximumNArgs(3),
			Run: runsetup(func(c *config.Config, s []string) error {
				c.Role = config.RoleNode
				if len(s) > 0 {
					hosts, secret, err := setuputil.ParseSeed(s[0])
					if err != nil {
						return err
					}
					c.Topology[""] = append(c.Topology[""], hosts...)
					c.Secret = secret
				}
				if len(s) > 1 && s[1] != "auto" {
					c.Host = s[1]
				}
				if len(s) > 2 && s[2] != "auto" {
					c.Cluster = s[2]
				}
				return nil
			}),
		},
		&cobra.Command{
			Use:   "replica [seed] [name]",
			Short: "Setup the server as a replica",
			Args:  cobra.MaximumNArgs(2),
			Run: runsetup(func(c *config.Config, s []string) error {
				c.Role = config.RoleReplica
				if len(s) > 0 {
					hosts, secret, err := setuputil.ParseSeed(s[0])
					if err != nil {
						return err
					}
					c.Topology[""] = append(c.Topology[""], hosts...)
					c.Secret = secret
				}
				if len(s) > 1 && s[1] != "auto" {
					c.Host = s[1]
				}
				return nil
			}),
		},
		&cobra.Command{
			Use:   "client [seed] [name]",
			Short: "Setup the server as a client",
			Args:  cobra.MaximumNArgs(2),
			Run: runsetup(func(c *config.Config, s []string) error {
				c.Role = config.RoleClient
				if len(s) > 0 {
					hosts, secret, err := setuputil.ParseSeed(s[0])
					if err != nil {
						return err
					}
					c.Topology[""] = append(c.Topology[""], hosts...)
					c.Secret = secret
				}
				if len(s) > 1 && s[1] != "auto" {
					c.Host = s[1]
				}
				return nil
			}),
		},
	)
}
