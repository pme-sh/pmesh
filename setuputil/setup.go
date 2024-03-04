package setuputil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"get.pme.sh/pmesh/autonats"
	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/geo"
	"get.pme.sh/pmesh/netx"
	"get.pme.sh/pmesh/pmtp"
	"get.pme.sh/pmesh/ui"
	"get.pme.sh/pmesh/xlog"
	"github.com/samber/lo"
)

const (
	lowercaseRunes    = "abcdefghijklmnopqrstuvwxyz"
	uppercaseRunes    = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitRunes        = "0123456789"
	alphanumericRunes = lowercaseRunes + uppercaseRunes + digitRunes
	serverNameRunes   = alphanumericRunes + "-_"
	clusterNameRunes  = alphanumericRunes
)

func validateName(runes string) func(name string) error {
	return func(name string) error {
		if name == "" {
			return fmt.Errorf("name cannot be empty")
		}
		for _, r := range name {
			if !strings.ContainsRune(runes, r) {
				return fmt.Errorf("name contains invalid character: %q", r)
			}
		}
		return nil
	}
}

func ParseSeed(seed string) (hosts []string, secret string, err error) {
	url, err := pmtp.ParseURL(strings.TrimSpace(seed))
	if err != nil {
		return nil, "", err
	}
	hosts = strings.Split(url.Host, ",")
	if len(hosts) == 0 {
		return nil, "", fmt.Errorf("no hosts in the URL")
	}
	if url.Secret == "" {
		return nil, "", fmt.Errorf("no secret in the URL")
	}
	return hosts, url.Secret, nil
}

func RunSetup(mc *config.Config, interactive bool) error {
	if *config.Dumb {
		interactive = false
	}
	mc.SetDefaults()

	// Determine the server role
	if mc.Role == config.RoleNotSet {
		if interactive {
			mc.Role = ui.PromptSelectValueDesc("Server role:", map[config.Role]string{
				config.RoleSeed:    "Create a new mesh from scratch",
				config.RoleNode:    "Full member of a mesh, accepts connections and participates in elections",
				config.RoleReplica: "Light replica of a mesh, holds a copy of the state to serve locally, but does not participate in elections",
				config.RoleClient:  "Connects to a mesh or a regular NATS server and acts as a client",
			})
		} else if len(mc.Topology) == 0 {
			mc.Role = config.RoleSeed
		} else {
			mc.Role = config.RoleNode
		}
	}

	// Determine the server name
	if interactive {
		mc.Host = ui.PromptString("Server name:", mc.Host, "", validateName(serverNameRunes))
		mc.Host = strings.ToLower(mc.Host)
	}

	// If this is a client, ask for a NATS URL and we're done
	if mc.Role == config.RoleClient {
		if mc.Remote == "" {
			mc.Remote = os.Getenv("PM3_NATS")
			if mc.Remote == "" {
				mc.Remote = "nats://localhost:4222"
			}
		}

		if interactive {
			mc.Remote = ui.PromptString("NATS URL:", mc.Remote, "", nil)
		}
		if strings.HasPrefix(mc.Remote, "pmtp://") {
			_, secret, err := ParseSeed(mc.Remote)
			if err != nil {
				return err
			}
			if secret != "" {
				mc.Secret = secret
			}
		}
		mc.Cluster = ""
		mc.Topology = nil
		mc.Advertised = ""
		return nil
	}

	// Determine the cluster placement and advertised address
	if mc.Role == config.RoleReplica || mc.Role == config.RoleClient {
		mc.Advertised = ""
		mc.Cluster = mc.Host
	} else {
		pubip := netx.GetPublicIPInfo(context.Background())
		coord := geo.LatLon(pubip.Lat, pubip.Lon)

		if mc.Cluster == "" {
			mc.Cluster = coord.Region().Code
		}
		if interactive {
			mc.Cluster = ui.PromptString("Cluster name:", mc.Cluster, "", validateName(clusterNameRunes))
		}
		mc.Cluster = strings.ToUpper(mc.Cluster)

		if mc.Advertised == "" {
			mc.Advertised = pubip.IP.String()
		}
		if interactive {
			mc.Advertised = ui.PromptString("Advertised Host:", mc.Advertised, "", nil)
		}
	}

	// Determine the secret
	if mc.Role == config.RoleSeed {
		if interactive {
			mc.Secret = ui.PromptString("Secret:", mc.Secret, "", nil)
		}
		mc.Topology = nil
	} else {
		seed := mc.Topology[""]
		delete(mc.Topology, "")

		if len(mc.Topology) == 0 {
			if len(seed) == 0 || mc.Secret == "" {
				if !interactive {
					return errors.New("can't setup without a seed topology and secret")
				}
				seedstr := ui.PromptString("Provide a seed:", "", "pmtp://... (Hint: Run 'pmesh get-seed' on another machine)", func(s string) error {
					_, _, err := ParseSeed(s)
					return err
				})
				hosts, secret, err := ParseSeed(seedstr)
				if err != nil {
					return err
				}
				if secret != "" {
					mc.Secret = secret
				}
				seed = hosts
			}

			top := ui.SpinnyWait("Discovering topology", func() (autonats.Topology, error) {
				return autonats.ExpandSeedTopology(seed, mc.Secret, time.Second*10)
			})
			if len(top) == 0 {
				return errors.New("topology yielded no servers")
			}
			mc.Topology = top
		}
		mc.Topology[""] = append(mc.Topology[""], seed...)

		for k := range mc.Topology {
			slice := lo.Compact(lo.Uniq(mc.Topology[k]))
			slices.Sort(slice)
			mc.Topology[k] = slice
			fmt.Printf("Topology[%q]: %v\n", k, slice)
		}
	}
	return nil
}

func RunSetupIf(interactive bool) *config.Config {
	if cfg := config.Get(); cfg.Role != config.RoleNotSet {
		return cfg
	}
	err := config.Update(func(ss *config.Config) error {
		return RunSetup(ss, interactive)
	})
	if err != nil {
		if !interactive {
			xlog.Fatal().Err(err).Msg("setup failed")
		}
		ui.ExitWithError(err.Error())
	}
	return config.Get()
}
