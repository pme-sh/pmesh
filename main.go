package main

import (
	"fmt"
	"os"

	_ "embed"

	"get.pme.sh/pmesh/cmd"
	"get.pme.sh/pmesh/revision"
)

func main() {
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "--version", "-v", "version", "v", "ver":
			fmt.Println(revision.GetVersion())
			os.Exit(0)
		}
	}
	cmd.Execute()
}
