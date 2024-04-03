package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"get.pme.sh/pmesh/npm"
	"get.pme.sh/pmesh/util"
)

type Advisor interface {
	Advise(path string) any
}

func exists(path ...string) bool {
	_, err := os.Stat(filepath.Join(path...))
	return err == nil
}

type JsApp struct {
	AppService `yaml:",inline"`
	Index      string `yaml:"index,omitempty"`
}

func (app *JsApp) Advise(path string) any {
	if !exists(path, "package.json") && exists(path, "index.js") {
		return map[string]any{
			"index": "index.js",
		}
	}
	return nil
}

func (app *JsApp) Prepare(opt Options) error {
	if err := app.AppService.Prepare(opt); err != nil {
		return err
	}
	if app.Index == "" {
		app.Index = "index.js"
	}

	if _, err := os.Stat(filepath.Join(app.Root, app.Index)); err != nil {
		return fmt.Errorf("index file %q not found", app.Index)
	}

	app.Run = NewCommand("node", app.Index)
	return nil
}

type NpmApp struct {
	AppService     `yaml:",inline"`
	StartScript    string `yaml:"start_script,omitempty"`
	BuildScript    string `yaml:"build_script,omitempty"`
	PackageManager string `yaml:"package_manager,omitempty"`
	NoInstall      bool   `yaml:"no_install,omitempty"`
}

func (app *NpmApp) Advise(path string) any {
	if exists(path, "package.json") {
		return struct{}{}
	}
	return nil
}

func (app *NpmApp) Prepare(opt Options) error {
	if err := app.AppService.Prepare(opt); err != nil {
		return err
	}

	// If package manager is not set, try to detect it
	if app.PackageManager == "" {
		app.PackageManager = "npm"
		if _, err := os.Stat(filepath.Join(app.Root, "package-lock.json")); err == nil {
			app.PackageManager = "npm"
		} else if _, err := os.Stat(filepath.Join(app.Root, "yarn.lock")); err == nil {
			app.PackageManager = "yarn"
		} else if _, err := os.Stat(filepath.Join(app.Root, "pnpm-lock.yaml")); err == nil {
			app.PackageManager = "pnpm"
		} else if _, err := exec.LookPath("pnpm"); err == nil {
			app.PackageManager = "pnpm"
		} else if _, err := exec.LookPath("yarn"); err == nil {
			app.PackageManager = "yarn"
		} else if _, err := exec.LookPath("bun"); err == nil {
			app.PackageManager = "bun"
		}
	}

	// Read the package.json
	pkg, err := npm.ParsePackage(app.Root)
	if err != nil {
		return fmt.Errorf("error parsing package.json: %w", err)
	}

	// Use the package.json file if any of the scripts are missing
	if app.StartScript == "" || app.BuildScript == "" {
		if app.StartScript == "" {
			app.StartScript = "start"
		}
		if app.StartScript != "none" && pkg.Scripts[app.StartScript] == "" {
			return fmt.Errorf("no start script %q in package.json", app.StartScript)
		}
		if app.BuildScript == "" {
			if pkg.Scripts["build"] != "" {
				app.BuildScript = "build"
			} else {
				app.BuildScript = "none"
			}
		}
	}

	if app.StartScript != "none" {
		cmd, args := pkg.TryEscapeScript(app.PackageManager, app.StartScript)
		run := NewCommand(cmd, args...)
		run.Dir = pkg.Root
		app.Run = run
	}
	if !app.NoInstall {
		if app.PackageManager == "bun" {
			app.Build = append(app.Build, NewCommand(app.PackageManager, "install"))
		} else {
			app.Build = append(app.Build, NewCommand(app.PackageManager, "install", "--production=false"))
		}
	}
	if app.BuildScript != "none" {
		cmd, args := pkg.TryEscapeScript(app.PackageManager, app.BuildScript)
		build := NewCommand(cmd, args...)
		build.Dir = pkg.Root
		app.Build = append(app.Build, build)
	}
	return nil
}

type PyApp struct {
	AppService   `yaml:",inline"`
	Requirements string `yaml:"requirements,omitempty"`
	Main         string `yaml:"main,omitempty"` // Main file to run
}

func (app *PyApp) Advise(path string) any {
	if exists(path, "requirements.txt") && !exists(path, "app.py") {
		return struct{}{}
	}
	return nil
}

func (app *PyApp) Prepare(opt Options) error {
	if err := app.AppService.Prepare(opt); err != nil {
		return err
	}
	if app.Requirements == "" {
		app.Requirements = "requirements.txt"
	}
	if !filepath.IsAbs(app.Requirements) {
		app.Requirements = filepath.Join(app.Root, app.Requirements)
	}

	if _, err := os.Stat(app.Requirements); err != nil {
		return fmt.Errorf("requirements file %q not found", app.Requirements)
	}
	if app.Env == nil {
		app.Env = make(map[string]string)
	}
	app.Env["VIRTUAL_ENV"] = ".run/venv"
	if app.Env["PATH"] == "" {
		app.Env["PATH"] = os.ExpandEnv("$PATH")
	}
	if runtime.GOOS == "windows" {
		app.Env["PATH"] = filepath.Join(app.Root, ".run/venv/Scripts") + ";" + app.Env["PATH"]
	} else {
		app.Env["PATH"] = filepath.Join(app.Root, ".run/venv/bin") + ":" + app.Env["PATH"]
	}
	os.Unsetenv("PYTHONHOME") // Don't use the system python
	app.Build = append(app.Build,
		NewCommand("python", "-m", "venv", ".run/venv"),
		NewCommand("pip", "install", "-r", app.Requirements),
	)
	if app.Main != "none" {
		if app.Main == "" {
			app.Main = "app.py"
		}
		if app.Run.IsZero() {
			app.Run = NewCommand("python", app.Main)
		}
	}
	return nil
}

type FlaskApp struct {
	PyApp `yaml:",inline"`
}

func (app *FlaskApp) Advise(path string) any {
	if exists(path, "requirements.txt") && exists(path, "app.py") {
		return struct{}{}
	}
	return nil
}

func (app *FlaskApp) Prepare(opt Options) error {
	if err := app.PyApp.Prepare(opt); err != nil {
		return err
	}
	app.EnvHost = "FLASK_RUN_HOST"
	app.EnvPort = "FLASK_RUN_PORT"
	app.Env["FLASK_APP"] = app.Main
	app.Run = NewCommand("flask", "run")
	return nil
}

type GoApp struct {
	AppService `yaml:",inline"`
	Main       string `yaml:"main,omitempty"`
}

func (app *GoApp) Advise(path string) any {
	if exists(path, "main.go") {
		return struct{}{}
	}
	return nil
}

func (app *GoApp) Prepare(opt Options) error {
	if err := app.AppService.Prepare(opt); err != nil {
		return err
	}
	if app.Main == "" {
		app.Main = "main.go"
	}
	if _, err := os.Stat(filepath.Join(app.Root, app.Main)); err != nil {
		return fmt.Errorf("main file %q not found", app.Main)
	}

	executableName := opt.Name
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}

	filePath := ".run/" + executableName
	absPath := filepath.Join(app.Root, filePath)
	os.Mkdir(filepath.Join(app.Root, ".run"), 0755)

	// If it already exists, delete/rename to prevent conflicts
	if stat, err := os.Stat(absPath); err == nil && !stat.IsDir() {
		if err := os.Remove(absPath); err != nil {
			os.Rename(absPath, fmt.Sprintf("%s.%d", absPath, time.Now().UnixNano()))
		}
	}

	app.Build = util.One(NewCommand("go", "build", "-o", filePath))
	app.Run = NewCommand(filePath)
	app.NoBuildControl = true
	return nil
}

func init() {
	Registry.Define("Js", func() any { return &JsApp{} })
	Registry.Define("Npm", func() any { return &NpmApp{} })
	Registry.Define("Pnpm", func() any { return &NpmApp{PackageManager: "pnpm"} })
	Registry.Define("Yarn", func() any { return &NpmApp{PackageManager: "yarn"} })
	Registry.Define("Bun", func() any { return &NpmApp{PackageManager: "bun"} })
	Registry.Define("Go", func() any { return &GoApp{} })
	Registry.Define("Py", func() any { return &PyApp{} })
	Registry.Define("Flask", func() any { return &FlaskApp{} })
}
