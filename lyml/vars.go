package lyml

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/cpuhist"
	"get.pme.sh/pmesh/luae"
	"get.pme.sh/pmesh/util"

	"github.com/samber/lo"
	lua "github.com/yuin/gopher-lua"
	"gopkg.in/yaml.v3"
)

// Global variables.
var variables = sync.Map{} // string -> lua.LValue
func SetVar(key string, value any) error {
	v, err := luae.MarshalLua(value)
	if err != nil {
		return fmt.Errorf("failed to marshal var %q: %w", key, err)
	}
	variables.Store(key, v)
	return nil
}
func SetVars(vars map[string]any) error {
	for k, v := range vars {
		if err := SetVar(k, v); err != nil {
			return err
		}
	}
	return nil
}

// luae.DynTable for environment variables.
type envTable struct{}

func (e envTable) Get(L *lua.LState, key string) (value any, ok bool) {
	return os.LookupEnv(key)
}
func (e envTable) Set(L *lua.LState, key string, value lua.LValue) error {
	var str string
	if err := luae.UnmarshalLua(L, value, &str); err != nil {
		return err
	}
	return os.Setenv(key, str)
}

// Initializes global variables.
var initGlobals = sync.OnceFunc(func() {
	SetVars(map[string]any{
		"env": luae.DynTable(envTable{}),
		"fetch": func(l *lua.LState, url string) json.RawMessage {
			req, err := http.NewRequestWithContext(l.Context(), http.MethodGet, url, nil)
			if err != nil {
				e, _ := json.Marshal(map[string]any{"error": err.Error()})
				return e
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				e, _ := json.Marshal(map[string]any{"error": err.Error()})
				return e
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				e, _ := json.Marshal(map[string]any{
					"error": resp.Status,
					"code":  resp.StatusCode,
				})
				return e
			}
			var res json.RawMessage
			e := json.NewDecoder(resp.Body).Decode(&res)
			if e != nil {
				e, _ := json.Marshal(map[string]any{"error": e.Error()})
				return e
			}
			return res
		},
		"HOST":    config.Get().Host,
		"MACH":    config.GetMachineID().String(),
		"IMACH":   uint32(config.GetMachineID()),
		"OS":      runtime.GOOS,
		"ARCH":    runtime.GOARCH,
		"PID":     os.Getpid(),
		"PPID":    os.Getppid(),
		"USER":    os.Getuid(),
		"CPU":     cpuhist.NumCPU,
		"RAM":     util.GetTotalMemory(),
		"args":    os.Args,
		"ud":      config.Get().PeerUD,
		"localud": config.Get().LocalUD,
		"match": func(str, rgx string) (bool, error) {
			expr, err := regexp.Compile(rgx)
			if err != nil {
				return false, err
			}
			return expr.MatchString(str), nil
		},
		"glob": func(pattern string) ([]string, error) {
			return filepath.Glob(pattern)
		},
		"parse": func(vm *lua.LState, path string) (res any, err error) {
			// Read the file and parse it.
			//
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}

			// Evaluate the node.
			//
			withWd(filepath.Dir(path), func() {
				var node *yaml.Node
				node, err = unmarshal(vm, data)
				if err == nil {
					err = node.Decode(&res)
				}
			})
			return
		},
	})
})

// Adds a variable to the context.
type variableKey string

func WithVar(ctx context.Context, key string, value any) context.Context {
	lv := lo.Must(luae.MarshalLua(value))
	return context.WithValue(ctx, variableKey(key), lv)
}

// Gets a variable from the context.
func GetVar(ctx context.Context, key string) lua.LValue {
	v := ctx.Value(variableKey(key))
	if v == nil {
		v, _ = variables.Load(key)
	}
	if v == nil {
		return nil
	}
	return v.(lua.LValue)
}

// Creates a metatable that indexes globals and contextual variables.
var contextualLookup = luae.NewFunc(func(l *lua.LState) int {
	if v := GetVar(l.Context(), l.ToString(2)); v != nil {
		l.Push(v)
		return 1
	}
	return 0
})

func NewContextMetatable() *lua.LTable {
	initGlobals()
	meta := &lua.LTable{}
	meta.RawSetString("__index", contextualLookup)
	return meta
}

// Creates a new VM with a context.
func NewContextVM(ctx context.Context) *lua.LState {
	vm := lua.NewState(lua.Options{
		IncludeGoStackTrace: true,
	})
	vm.SetContext(ctx)
	vm.G.Global.Metatable = NewContextMetatable()
	return vm
}
