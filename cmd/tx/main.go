package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	flags "github.com/jessevdk/go-flags"
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
	var opts cli.Options
	parser := flags.NewParser(&opts, flags.Default)
	parser.Name = "tx"

	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("tx " + resolveVersion())
		os.Exit(0)
	}

	ui := cli.NewUI(os.Stdout)
	opts.Login.UI = ui
	opts.Init.UI = ui
	opts.Build.UI = ui
	opts.Status.UI = ui
	opts.Token.Create.UI = ui
	opts.Token.List.UI = ui
	opts.Token.Delete.UI = ui

	if _, err := parser.Parse(); err != nil {
		if flagsErr, ok := err.(*flags.Error); ok {
			switch flagsErr.Type {
			case flags.ErrHelp:
				os.Exit(0)
			case flags.ErrCommandRequired:
				parser.WriteHelp(os.Stdout)
				os.Exit(0)
			}
		}
		os.Exit(1)
	}
}
