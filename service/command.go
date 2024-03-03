package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pme-sh/pmesh/config"

	"github.com/google/shlex"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

type command struct {
	Dir  string            `yaml:"cwd,omitempty"`  // The working directory to run the script in.
	Exec string            `yaml:"exec,omitempty"` // The executable used to run the script.
	Args []string          `yaml:"args,omitempty"` // Arguments passed.
	Env  map[string]string `yaml:"env,omitempty"`  // The environment variables to set.
}
type Command struct {
	command
}

func CommandFromString(s string) (c Command, e error) {
	e = c.UnmarshalText([]byte(s))
	return
}
func NewCommand(exec string, args ...string) Command {
	return Command{command{"", exec, args, nil}}
}
func (c *Command) Clone() *Command {
	return &Command{
		command{
			c.Dir,
			c.Exec,
			append([]string{}, c.Args...),
			lo.Assign(c.Env),
		},
	}
}
func (c Command) IsZero() bool {
	return c.Exec == ""
}
func (c Command) String() string {
	builder := strings.Builder{}
	if c.Dir != "" {
		builder.WriteString("@")
		builder.WriteString(c.Dir)
		builder.WriteString(" ")
	}
	if c.Env != nil {
		for k, v := range c.Env {
			builder.WriteString(k)
			builder.WriteString("=")
			builder.WriteString(v)
			builder.WriteString(" ")
		}
	}
	builder.WriteString(c.Exec)
	for _, arg := range c.Args {
		builder.WriteString(" ")
		builder.WriteString(arg)
	}
	return builder.String()
}

func (c Command) MarshalYAML() (any, error) {
	return c.command, nil
}
func (c *Command) UnmarshalText(data []byte) error {
	parts, err := shlex.Split(string(data))
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return nil
	}

	i := 0
	if strings.HasPrefix(parts[0], "@") {
		c.Dir = parts[0][1:]
		i = 1
	}

	for i < len(parts) {
		before, after, ok := strings.Cut(parts[i], "=")
		i++
		if !ok {
			c.Exec = before
			break
		}
		if c.Env == nil {
			c.Env = map[string]string{before: after}
		} else {
			c.Env[before] = after
		}
	}
	c.Args = parts[i:]
	return nil
}
func (c *Command) MergeEnv(m map[string]string) {
	if c.Env == nil {
		c.Env = make(map[string]string)
	}
	for k, v := range m {
		if _, ok := c.Env[k]; !ok {
			c.Env[k] = v
		}
	}
}

func (c *Command) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return node.Decode(&c.command)
	}
	var res string
	if err := node.Decode(&res); err != nil {
		return err
	}
	return c.UnmarshalText([]byte(res))
}
func (c *Command) Create(root string, ctx context.Context) *exec.Cmd {
	executable := c.Exec
	if executable == "python3" || executable == "python2" {
		if _, err := exec.LookPath(executable); err != nil {
			executable = "python"
		}
	}
	cmd := exec.CommandContext(ctx, executable, c.Args...)
	if c.Dir == "" {
		cmd.Dir = root
	} else if filepath.IsAbs(c.Dir) {
		cmd.Dir = c.Dir
	} else {
		cmd.Dir = filepath.Join(root, c.Dir)
	}
	cmd.Env = os.Environ()
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cfg := config.Get()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PM3_NODE=%s", cfg.Host))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PM3_HOST=%s.pm3", cfg.Host))
	return cmd
}
