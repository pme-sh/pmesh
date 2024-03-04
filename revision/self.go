package revision

import (
	"fmt"
	"runtime/debug"
	"sync"
)

var (
	VersionString = "v0.2" // Only updated for major/minor releases.
)

func getCommit() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				if len(setting.Value) > 8 {
					return setting.Value[:8]
				}
			}
		}
	}
	return "00000000"
}

var GetVersion = sync.OnceValue(func() string {
	return fmt.Sprintf("%s-%s", VersionString, getCommit())
})
