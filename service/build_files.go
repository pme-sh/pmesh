package service

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/pme-sh/pmesh/glob"
	"github.com/pme-sh/pmesh/snowflake"
)

const (
	BuilderRunDir        = ".run"     // -> links to .build-%s
	BuilderIdFile        = ".buildid" // Contains the current build id
	BuilderBuildDir      = ".build"   // Used while building, renamed to .build-%s when done
	BuilderArchivePrefix = ".build-"  // The build directory format
)

// Lexicographically comparable build timestamp.
func buildTimestamp() string {
	res := snowflake.New().String()
	if len(res) < 20 {
		res = strings.Repeat("0", 20-len(res)) + res
	}
	return res
}

type BuildFS struct {
	Root string
}

func (bfs BuildFS) rm(p string) error {
	abs := filepath.Clean(p)
	ok1 := filepath.Join(bfs.Root, ".run")
	ok2 := filepath.Join(bfs.Root, ".build")
	if !strings.HasPrefix(abs, ok1) && !strings.HasPrefix(abs, ok2) {
		log.Fatalln("illegal rm", p, abs, bfs.Root)
		return nil
	}
	err := os.Remove(abs)
	if err != nil {
		err = os.RemoveAll(abs)
	}
	return err
}

// Gets all relevant folders.
func (bfs BuildFS) Folders() (r map[string]string) {
	r = make(map[string]string) // name -> link || ""

	entries, e := os.ReadDir(bfs.Root)
	if e != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != BuilderRunDir {
			if name != BuilderBuildDir && !strings.HasPrefix(name, BuilderArchivePrefix) {
				continue
			}
		}

		if entry.IsDir() {
			r[name] = ""
		} else if entry.Type() == os.ModeSymlink {
			link, e := os.Readlink(filepath.Join(bfs.Root, name))
			if e == nil {
				base := filepath.Base(link)
				abs1 := filepath.Join(bfs.Root, base)
				abs2 := filepath.Join(bfs.Root, link)
				if abs1 == abs2 {
					link = base
				} else {
					rel, e := filepath.Rel(bfs.Root, link)
					if e == nil {
						link = rel
					}
				}
				r[name] = link
			}
		}
	}
	return
}

// Cleans the directory removing past builds.
func (bfs BuildFS) Clean() {
	f := bfs.Folders()

	// Do not delete the current run directory
	if run := f[BuilderRunDir]; run != "" {
		delete(f, run)
		delete(f, BuilderRunDir)
	}

	// Delete the rest of the folders
	for k := range f {
		bfs.rm(filepath.Join(bfs.Root, k))
	}
}

// Unlinks the run directory.
func (bfs BuildFS) UnlinkRun() error {
	// If it doesn't exist, we're done
	rundir := filepath.Join(bfs.Root, BuilderRunDir)
	// Try to remove it
	removeErr := bfs.rm(rundir)

	// If it still exists, we failed
	if _, err := os.Stat(rundir); err == nil {
		return fmt.Errorf("failed to remove old run directory: %w", removeErr)
	}
	return nil
}

// Links the run directory.
func (bfs BuildFS) LinkRun(buildDir string) error {
	rundir := filepath.Join(bfs.Root, BuilderRunDir)
	if os.Symlink(buildDir, rundir) == nil {
		return nil
	}
	bfs.UnlinkRun()
	return os.Symlink(buildDir, rundir)
}

// Reads the build ID.
func (bfs BuildFS) ReadBuildId() (glob.Checksum, error) {
	id, err := os.ReadFile(filepath.Join(bfs.Root, BuilderRunDir, BuilderIdFile))
	if err != nil {
		return glob.Checksum{}, err
	}
	id, err = hex.DecodeString(string(id))
	if err != nil {
		return glob.Checksum{}, fmt.Errorf("invalid build id: %w", err)
	}
	if len(id) != len(glob.Checksum{}) {
		return glob.Checksum{}, fmt.Errorf("invalid build id length: %d", len(id))
	}
	return glob.Checksum(id), nil
}

// Writes the build ID.
func (bfs BuildFS) WriteBuildId(id glob.Checksum, buildDir string) error {
	ids := hex.EncodeToString(id[:])
	return os.WriteFile(filepath.Join(buildDir, BuilderIdFile), []byte(ids), 0644)
}

// Runs the pre-build steps.
func (bfs BuildFS) PreBuild() error {
	// Clean the directory
	bfs.Clean()

	// Create the build directory
	buildDir := filepath.Join(bfs.Root, BuilderBuildDir)
	if err := os.Mkdir(buildDir, 0755); err != nil {
		return fmt.Errorf("failed to create build directory: %w", err)
	}
	return nil
}

// Aborts the current build.
func (bfs BuildFS) AbortBuild() {
	bfs.rm(filepath.Join(bfs.Root, BuilderBuildDir))
}

// Runs the post-build steps.
func (bfs BuildFS) PostBuild(chk glob.Checksum) error {
	// Remove the old run directory
	bfs.UnlinkRun()

	// Move the active build into archive.
	buildDir := filepath.Join(bfs.Root, BuilderArchivePrefix+buildTimestamp())
	if err := os.Rename(filepath.Join(bfs.Root, BuilderBuildDir), buildDir); err != nil {
		return fmt.Errorf("failed to archive build: %w", err)
	}

	// Write the build ID
	if err := bfs.WriteBuildId(chk, buildDir); err != nil {
		return fmt.Errorf("failed to write build id: %w", err)
	}

	// Link the new run directory
	if err := bfs.LinkRun(buildDir); err != nil {
		return fmt.Errorf("failed to link run directory: %w", err)
	}
	return nil
}

// Runs the builder.
func (bfs BuildFS) RunBuild(chk glob.Checksum, cb func() error) error {
	if err := bfs.PreBuild(); err != nil {
		return err
	}
	err := cb()
	if err == nil {
		err = bfs.PostBuild(chk)
	}
	if err != nil {
		bfs.AbortBuild()
	}
	return err
}
