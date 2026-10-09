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

	_, err := parser.ParseArgs(args)
	if err == nil {
		return ExitOK
	}

	if flagsErr, ok := errors.AsType[*flags.Error](err); ok {
		switch {
		case errors.Is(flagsErr.Type, flags.ErrHelp):
			fmt.Fprintln(stdout, flagsErr.Message)
			return ExitOK
		case errors.Is(flagsErr.Type, flags.ErrCommandRequired) && len(args) == 0:
			parser.WriteHelp(stdout)
			return ExitOK
		case errors.Is(flagsErr.Type, flags.ErrCommandRequired):
			parser.WriteHelp(stderr)
		}
		err = usageError(err)
	}

	ui.Errorf("%s", err.Error())
	return AsExitError(err).Code
}
