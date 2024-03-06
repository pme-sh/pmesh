package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/service"
	"get.pme.sh/pmesh/ui"
	"get.pme.sh/pmesh/util"
	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type GeneratedManifest struct {
	ServiceRoot string                       `yaml:"service_root,omitempty"` // Service root directory
	Services    util.OrderedMap[string, any] `yaml:"services,omitempty"`     // Services
	Server      map[string]any               `yaml:"server,omitempty"`       // Virtual hosts
	Hosts       []string                     `yaml:"hosts,omitempty"`        // Hostname to IP mapping
}

func exists(path ...string) bool {
	_, err := os.Stat(filepath.Join(path...))
	return err == nil
}

func autoGenerateManifest(cwd string) []byte {
	gm := GeneratedManifest{
		ServiceRoot: ".",
	}
	for _, delimiter := range []string{"packages", "services", "apps", "pkg"} {
		if exists(cwd, delimiter) {
			gm.ServiceRoot = delimiter
			break
		}
	}

	validator := func(path string) error {
		res, err := os.ReadDir(filepath.Join(cwd, path))
		if err == nil {
			for _, file := range res {
				if file.IsDir() {
					return nil
				}
			}
			return fmt.Errorf("no directories found in %q", path)
		}
		return err
	}

	gm.ServiceRoot = ui.PromptString("Where do your services live?", gm.ServiceRoot, "", validator)
	gm.ServiceRoot = filepath.Clean(gm.ServiceRoot)
	if gm.ServiceRoot == "." {
		gm.ServiceRoot = ""
	}

	sroot := filepath.Join(cwd, gm.ServiceRoot)
	files, err := os.ReadDir(sroot)
	if err != nil {
		ui.ExitWithError(err)
	}

	svcNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	for _, file := range files {
		if file.IsDir() {
			var options []yaml.Node
			for tag, value := range service.Registry.Tags {
				if advisor, ok := value.Instance.(service.Advisor); ok {
					if advice := advisor.Advise(filepath.Join(sroot, file.Name())); advice != nil {
						node := yaml.Node{}
						if err := node.Encode(advice); err == nil {
							node.Tag = "!" + tag
							options = append(options, node)
						}
					}
				}
			}

			{
				node := yaml.Node{}
				node.Encode(struct{}{})
				node.Tag = "!FS"
				options = append(options, node)
				node = yaml.Node{}
				node.Encode(map[string]any{
					"run": "start --arg1 --arg2",
					"build": []string{
						"build --arg1 --arg2",
						"then --another",
					},
				})
				node.Tag = "!App"
				options = append(options, node)
			}

			var valueSelect []string
			for _, node := range options {
				valueSelect = append(valueSelect, node.Tag[1:])
			}
			valueSelect = append(valueSelect, "Skip")

			query := fmt.Sprintf("What type of service is %s?", svcNameStyle.Render(file.Name()))
			result := ui.PromptSelect(query, valueSelect)

			for _, node := range options {
				if node.Tag[1:] == result {
					gm.Services.Set(file.Name(), node)
					break
				}
			}
		}
	}

	domain := ui.PromptString("What is your domain?", "example.com", "", nil)
	devDomain := ui.PromptString("What is your development domain?", "example.local", "", nil)

	router := []map[string]string{}
	gm.Services.ForEach(func(k string, _ any) {
		route := map[string]string{}
		route[k+"."+domain+"/"] = k
		router = append(router, route)
		gm.Hosts = append(gm.Hosts, k+"."+devDomain)
	})

	gm.Server = map[string]any{
		domain + ", " + devDomain: map[string]any{
			"router": router,
		},
	}

	buf := &strings.Builder{}
	enc := yaml.NewEncoder(buf)
	enc.SetIndent(2)
	err = enc.Encode(gm)
	if err != nil {
		ui.ExitWithError(err)
	}

	if err := quick.Highlight(os.Stdout, buf.String(), "yaml", "terminal256", "monokai"); err != nil {
		fmt.Println(buf.String())
	}

	return []byte(buf.String())
}

func init() {
	config.RootCommand.AddCommand(&cobra.Command{
		Use:   "create",
		Short: "Create a new manifest",
		Run: func(cmd *cobra.Command, args []string) {
			manifest := autoGenerateManifest(".")
			manifestPath := filepath.Join(".", "pm3.yml")

			if ui.PromptSelect("Save to "+manifestPath+"?", []string{"Yes", "No"}) == "Yes" {
				_ = os.WriteFile(manifestPath, manifest, 0644)
			}
		},
	})
}
