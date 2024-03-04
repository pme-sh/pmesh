package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Global flags.
var Verbose = GBool("verbose", "V", false, "Enable verbose logging")
var Dumb = GBool("dumb", "D", IsTermDumb(), "Disable interactive prompts and complex ui")
var EnvName = GString("env", "E", "", "Environment name, used for running multiple instances of pmesh")
var BindAddr = GString("bind", "B", "0.0.0.0", "Bind address for public connections")
var LocalBindAddr = GString("local-bind", "L", "127.0.0.1", "Bind address for local connections")
var HttpPort = GInt("http", "H", 80, "Listen port for public HTTP")
var HttpsPort = GInt("https", "S", 443, "Listen port for public HTTPS")
var InternalPort = GInt("internal-port", "", 8443, "Internal port")
var ServiceSubnet = GString("subnet-service", "", "127.1.0.0/16", "Service subnet")
var DialerSubnet = GString("subnet-dialer", "", "127.2.0.0/16", "Dialer subnet")
var _ = GString("cwd", "C", "", "Sets the working directory before running the command")

var cache = sync.Map{}

func mkdironce(dir string) {
	if _, loaded := cache.LoadOrStore(dir, true); !loaded {
		if os.Mkdir(dir, 0755) == nil {
			cache.Store(dir, struct{}{})
		}
	}
}

// Home directory.
func Home() (home string) {
	userDir, _ := os.UserHomeDir()
	if *EnvName == "" {
		home = filepath.Join(userDir, ".pmesh")
	} else if filepath.IsAbs(*EnvName) {
		home = *EnvName
	} else {
		home = filepath.Join(userDir, ".pmesh-"+*EnvName)
	}
	mkdironce(home)
	return
}

// Subdirectories.
type Subdir string

const (
	LogDir   Subdir = "log"
	AsnDir   Subdir = "asn"
	StoreDir Subdir = "store"
	CertDir  Subdir = "certs"
)

func NatsDir(serverName string) string {
	return filepath.Join(StoreDir.Path(), serverName)
}

func (s Subdir) Path() string {
	path := filepath.Join(Home(), string(s))
	mkdironce(path)
	return path
}
func (s Subdir) File(name string) string {
	return filepath.Join(s.Path(), name)
}

// Global from either environment or command line.
var RootCommand = &cobra.Command{
	Use:   "pmesh",
	Short: "pme.sh is an all-in one service manager, reverse proxy, and enterprise service bus.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) (err error) {
		if cmd.Flag("cwd").Changed {
			err = os.Chdir(cmd.Flag("cwd").Value.String())
		}
		return
	},
}

func getenv(name string) (string, bool) {
	name = strings.ToUpper(name)
	name = strings.ReplaceAll(name, "-", "_")
	return os.LookupEnv("PM3_" + name)
}
func GInt(name, shorthand string, value int, usage string) *int {
	flags := RootCommand.PersistentFlags()
	if env, ok := getenv(name); ok {
		if v, e := strconv.Atoi(env); e == nil {
			value = v
		}
	}
	flags.IntVarP(&value, name, shorthand, value, usage)
	return &value
}
func GString(name, shorthand string, value string, usage string) *string {
	flags := RootCommand.PersistentFlags()
	if env, ok := getenv(name); ok {
		value = env
	}
	flags.StringVarP(&value, name, shorthand, value, usage)
	return &value
}
func GBool(name, shorthand string, value bool, usage string) *bool {
	flags := RootCommand.PersistentFlags()
	if env, ok := getenv(name); ok {
		if v, e := strconv.ParseBool(env); e == nil {
			value = v
		}
	}
	flags.BoolVarP(&value, name, shorthand, value, usage)
	return &value
}

// Utils.
func IsTermDumb() bool {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return true
	}

	isTrue := map[string]bool{
		"1": true, "t": true, "y": true,
		"true": true, "yes": true, "on": true,
		"0": false, "f": false, "n": false,
		"false": false, "no": false, "off": false,
	}
	envs := []string{"PM3_NON_INTERACTIVE", "CI", "DEBIAN_FRONTEND", "NON_INTERACTIVE"}
	for _, env := range envs {
		if v, ok := os.LookupEnv(env); ok {
			if b, ok := isTrue[strings.ToLower(v)]; ok {
				return b
			}
		}
	}
	return false
}
