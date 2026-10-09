package main

import (
	"os"
	"runtime/debug"
	"strings"

	"github.com/texops/tx/internal/cli"
)

var version string

func resolveVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	return "dev"
}

func main() {
	os.Exit(cli.Run(resolveVersion(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
