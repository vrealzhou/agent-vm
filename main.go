package main

import (
	"fmt"
	"os"

	_ "embed"

	"github.com/vrealzhou/agent-vm/internal/cli"
	"github.com/vrealzhou/agent-vm/internal/container"
)

//go:embed Dockerfile
var kataDockerfile string

func main() {
	// The embedded Dockerfile lives in package main (//go:embed paths cannot
	// reach above a package directory), so wire it into the container package.
	container.Dockerfile = kataDockerfile

	if err := cli.Run(); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	_, _ = os.Stderr.WriteString("[agent-vm] ERROR: " + fmt.Sprintf(format, args...) + "\n")
	os.Exit(1)
}
