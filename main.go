package main

import (
	"runtime"

	"get.pme.sh/pmesh/cmd"

	"go.uber.org/automaxprocs/maxprocs"
)

func main() {
	if runtime.GOMAXPROCS(0) > 32 {
		runtime.GOMAXPROCS(32)
	}
	maxprocs.Set()
	cmd.Execute()
}
