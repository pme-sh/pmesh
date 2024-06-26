package npm

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/shlex"
)

// Handles:
// - "a"
// - {"a":"b"}
// - {"a":{"b":"c"}}
type ExportTable struct {
	s string
	v map[string]ExportTable
}

func (t *ExportTable) Resolve(str string) (out string, ok bool) {
	if t.s != "" {
		return t.s, true
	}
	if t.v != nil {
		if v, ok := t.v[str]; ok {
			return v.Resolve(".")
		}
		for k, v := range t.v {
			k = strings.TrimPrefix(k, ".")
			k = strings.TrimPrefix(k, "/")
			if rest, ok := strings.CutSuffix(k, "/*"); ok {
				if str == rest {
					return v.Resolve(".")
				}
				rest = k[:len(k)-1]
				if sr, ok := strings.CutPrefix(str, rest); ok {
					return v.Resolve(sr)
				}
			}
		}
	}
	return "", false
}

func (t *ExportTable) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || data[0] == 'n' {
		*t = ExportTable{}
		return nil
	}
	if data[0] == '{' {
		return json.Unmarshal(data, &t.v)
	}
	return json.Unmarshal(data, &t.s)
}

type Package struct {
	Root            string            `json:"-"`
	Name            string            `json:"name,omitempty"`
	Version         string            `json:"version,omitempty"`
	Type            string            `json:"type,omitempty"`
	Main            string            `json:"main,omitempty"`
	Dependencies    map[string]string `json:"dependencies,omitempty"`
	DevDependencies map[string]string `json:"devDependencies,omitempty"`
	Scripts         map[string]string `json:"scripts,omitempty"`
	Exports         ExportTable       `json:"exports,omitempty"`
	Bin             ExportTable       `json:"bin,omitempty"`
}

func (p *Package) String() string {
	return fmt.Sprintf("%s@%s", p.Name, p.Version)
}

type resolvedDependency struct {
	From      string `json:"from,omitempty"`
	Version   string `json:"version,omitempty"`
	Resolved  string `json:"resolved,omitempty"`
	Path      string `json:"path,omitempty"`
	Overriden bool   `json:"overriden,omitempty"`
}
type packageResolution struct {
	Name            string                        `json:"name,omitempty"`
	Path            string                        `json:"path,omitempty"`
	Resolved        string                        `json:"resolved,omitempty"`
	Dependencies    map[string]resolvedDependency `json:"dependencies,omitempty"`
	DevDependencies map[string]resolvedDependency `json:"devDependencies,omitempty"`
}

func resolvePackageManuallyInContext(cwd string, packageName string) (dep string) {
	path := filepath.Join(cwd, "node_modules", packageName)
	if _, err := os.Stat(filepath.Join(path, "package.json")); err == nil {
		return path
	}
	return ""
}
func resolvePackageInContext(mgr string, cwd string, packageName string, global bool) (pkg *Package, err error) {
	if !global {
		if dep := resolvePackageManuallyInContext(cwd, packageName); dep != "" {
			if pkg, err := ParsePackage(dep); err == nil {
				return pkg, nil
			}
		}
	}

	if mgr == "bun" {
		return nil, fmt.Errorf("bun does not support package resolution")
	}

	stdout := bytes.NewBuffer(nil)
	var cmd *exec.Cmd
	if global {
		cmd = exec.Command(mgr, "ls", packageName, "-g", "-json", "-long")
	} else {
		cmd = exec.Command(mgr, "ls", packageName, "-json", "-long")
	}
	stderr := strings.Builder{}
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	if cwd != "" {
		cmd.Dir = cwd
	}
	err = cmd.Run()
	if err != nil {
		err = fmt.Errorf("failed resolving package '%s' with NPM: %w %s", packageName, err, stderr.String())
		return
	}

	parsepkg := func(rel string, p string) *Package {
		if p == "" {
			return nil
		}
		p = strings.TrimPrefix(p, "file:///")
		p = strings.TrimPrefix(p, "file://")
		p = strings.TrimPrefix(p, "file:/")
		p = strings.TrimPrefix(p, "file:")
		if !filepath.IsAbs(p) {
			if rel == "" {
				rel = cwd
			} else if _, err := os.Stat(filepath.Join(cwd, p)); err == nil {
				rel = cwd
			}
			p = filepath.Join(rel, p)
		}
		pkg, _ := ParsePackage(p)
		return pkg
	}

	var resol []packageResolution
	if err = json.Unmarshal(stdout.Bytes(), &resol); err != nil {
		var npmStyle packageResolution
		if err = json.Unmarshal(stdout.Bytes(), &npmStyle); err != nil {
			return
		}
		resol = []packageResolution{npmStyle}
	}
	for _, e := range resol {
		if p := parsepkg("", e.Resolved); p != nil && p.Name == packageName {
			return p, nil
		}
		if e.Name == packageName {
			if pkg = parsepkg("", e.Path); pkg != nil && pkg.Name == packageName {
				return pkg, nil
			}
		}
		if e.Dependencies != nil {
			if d, ok := e.Dependencies[packageName]; ok {
				if p := parsepkg(e.Path, d.Path); p != nil {
					return p, nil
				}
			}
		}
		if e.DevDependencies != nil {
			if d, ok := e.DevDependencies[packageName]; ok {
				if p := parsepkg(e.Path, d.Path); p != nil {
					return p, nil
				}
			}
		}
	}
	err = fmt.Errorf("failed resolving package '%s' with NPM", packageName)
	return
}

func ParsePackage(root string) (*Package, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil, err
	}
	var pkg Package
	pkg.Root = root
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func (p *Package) ResolveDependency(mgr string, dep string) (pkg *Package, err error) {
	pkg, err = resolvePackageInContext(mgr, p.Root, dep, false)
	if err != nil {
		if pkg, e2 := ResolveGlobalPackage(mgr, dep); e2 == nil {
			return pkg, nil
		}
	}
	return
}

func (p *Package) ResolveExport(str string) (out string, err error) {
	if rest, ok := strings.CutPrefix(str, "$bin/"); ok {
		if bin, ok := p.Bin.Resolve(rest); ok {
			return filepath.Join(p.Root, bin), nil
		}
		return "", fmt.Errorf("bin-export not found: %s", str)
	}
	str = strings.TrimPrefix(path.Clean(str), "./")
	if str == "." {
		if imp, ok := p.Exports.Resolve("."); ok {
			return filepath.Join(p.Root, imp), nil
		}
		if p.Main != "" {
			return p.Main, nil
		}
	}
	if imp, ok := p.Exports.Resolve(str); ok {
		return filepath.Join(p.Root, imp), nil
	}
	return "", fmt.Errorf("export not found: %s", str)
}
func (p *Package) ResolveImport(mgr string, imp string) (path string, err error) {
	pkg, file, _ := strings.Cut(imp, "/")
	i, err := p.ResolveDependency(mgr, pkg)
	if err != nil {
		return "", err
	}
	return i.ResolveExport(file)
}
func (pkg *Package) TryEscapeScript(mgr string, scriptName string) (executable string, arguments []string) {
	script := pkg.Scripts[scriptName]

	// Apply fallbacks
	engine := "node"
	if mgr == "bun" {
		engine = "bun"
		executable = "bun"
		arguments = []string{"run", scriptName}
	} else {
		executable = mgr
		arguments = []string{"run", "-s", scriptName}
	}

	// If known script with no shell-like characters:
	if script != "" && !strings.Contains(script, "&") && !strings.Contains(script, "|") && !strings.Contains(script, ">") {
		// If we successfully split the script:
		parts, err := shlex.Split(script)
		if err == nil && len(parts) > 0 {
			cmd := parts[0]
			var importedScript string
			var executedScript string

			switch cmd {
			case "node", "nodejs", "deno", "bun":
				// enforce engine
				return engine, parts[1:]
			case "tsx":
				importedScript = "tsx"
				if mgr == "bun" {
					return // Not necessary as bun can run TypeScript directly
				}
			case "tsc":
				executedScript = "typescript"
			case "ts-node":
				importedScript = "ts-node"
				if mgr == "bun" {
					return // Not necessary as bun can run TypeScript directly
				}
			case "ts-node-esm":
				importedScript = "ts-node/esm"
				if mgr == "bun" {
					return // Not necessary as bun can run TypeScript directly
				}
			case "vite":
				executedScript = "vite/$bin/vite"
			}

			// If we have a known script, try to run it with node
			if importedScript != "" || executedScript != "" {
				// Bun does not support --import
				if importedScript != "" {
					return
				}
				_, err := exec.LookPath(engine)
				if err != nil {
					return
				}

				resolvedScriptName := cmp.Or(executedScript, importedScript)
				if p, err := pkg.ResolveImport(mgr, resolvedScriptName); err == nil {
					var args []string
					if importedScript != "" {
						if engine == "node" {
							args = []string{"--experimental-specifier-resolution=node"}
						}
						args = append(args, "--import", "file://"+p)
					} else {
						args = []string{p}
					}
					args = append(args, parts[1:]...)
					return engine, args
				}
			}

			// If simple script where we can resolve the executable, try to run it directly
			if _, err := exec.LookPath(cmd); err == nil {
				return cmd, parts[1:]
			}
		}
	}

	// Fallback to running the script with the package manager
	return
}

func ResolveGlobalPackage(mgr string, pkg string) (p *Package, err error) {
	p, err = resolvePackageInContext(mgr, "", pkg, true)
	if err != nil && mgr != "npm" {
		if p2, e2 := resolvePackageInContext("npm", "", pkg, true); e2 == nil {
			return p2, nil
		}
	}
	return
}
func ResolveGlobalImport(mgr string, imp string) (string, error) {
	pkg, file, _ := strings.Cut(imp, "/")
	p, err := ResolveGlobalPackage(mgr, pkg)
	if err != nil {
		return "", err
	}
	return p.ResolveExport(file)
}
