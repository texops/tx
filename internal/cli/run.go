package cli

import (
	"errors"
	"fmt"
	"io"

	flags "github.com/jessevdk/go-flags"
)

// Run executes tx with args (without the program name) and returns the
// process exit code. Every error is printed exactly once, to stderr.
func Run(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	SetVersion(version)

	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Fprintln(stdout, "tx "+version)
		return ExitOK
	}

	ui := NewUIWithStreams(stdout, stderr, stdin)

	var opts Options
	opts.Login.UI = ui
	opts.Init.UI = ui
	opts.Build.UI = ui
	opts.Status.UI = ui
	opts.Token.Create.UI = ui
	opts.Token.List.UI = ui
	opts.Token.Delete.UI = ui

	parser := flags.NewParser(&opts, flags.HelpFlag|flags.PassDoubleDash)
	parser.Name = "tx"
	setupHelp(parser)
	parser.CommandHandler = func(command flags.Commander, args []string) error {
		ui.SetJSON(opts.JSON)
		if command == nil {
			return nil
		}
		return command.Execute(args)
	}

	_, err := parser.ParseArgs(args)
	if err == nil {
		return ExitOK
	}

	if flagsErr, ok := errors.AsType[*flags.Error](err); ok {
		switch {
		case errors.Is(flagsErr.Type, flags.ErrHelp):
			writeHelp(parser, stdout)
			return ExitOK
		case errors.Is(flagsErr.Type, flags.ErrCommandRequired):
			writeHelp(parser, stderr)
		}
		err = usageError(err)
	}

	if opts.JSON || hasJSONFlag(args) {
		ui.SetJSON(true)
	}
	ui.Errorf("%s", err.Error())
	if ui.JSON() && !ui.wroteJSON {
		writeErrorJSON(ui, err)
	}
	return AsExitError(err).Code
}

// hasJSONFlag finds --json in arguments that failed to parse.
func hasJSONFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" {
			return true
		}
	}
	return false
}
