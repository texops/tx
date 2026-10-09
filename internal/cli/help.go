package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	flags "github.com/jessevdk/go-flags"
)

const rootHelp = `Compile LaTeX projects remotely with TexOps.

Typical use:
  tx init --texlive 2025 --compiler pdflatex   # writes .texops.yaml
  tx build                                     # builds every document
  tx build paper --json                        # one document, machine-readable

Authentication: 'tx login' (interactive) or TX_API_TOKEN (CI, agents).
Without a terminal, tx never prompts: it uses defaults or fails (exit 2, or 4
without .texops.yaml).
On failure the diagnostics name file:line; with --log=file the full log goes
to .texops/logs/.

Exit codes: 0 ok, 1 service error, 2 usage, 3 auth, 4 config, 5 LaTeX errors.
Run 'tx <command> --help' for details on a command.`

var commandHelp = []struct{ path, text string }{
	{"login", `Log in with a one-time code: tx prints a URL and a code, opens the URL in a
browser and waits until the login is approved there. The session is stored in
the system keyring, or in the credentials file when there is no keyring.

A person has to approve the login in a browser. CI and coding agents should
set TX_API_TOKEN instead, or ask the user to run 'tx login' in a terminal.
Exits 1 when the code expires or --timeout runs out.

Examples:
  tx login
  tx login --no-browser --timeout 2m`},

	{"init", `Write .texops.yaml in the current directory. Every .tex file that contains
\documentclass becomes a document.

On a terminal, tx asks for the TeX Live version, the compiler and the
documents. Without one it does not prompt: it uses the newest TeX Live
version, pdflatex and every document it found. Exits 4 when .texops.yaml
already exists.

Examples:
  tx init
  tx init --texlive 2025 --compiler xelatex`},

	{"build", `Upload the project, compile the documents on TexOps and download the PDFs
to the paths in .texops.yaml. Without names, every document is built.

Results go to stdout, progress to stderr. Each LaTeX error and warning is
printed as file:line: severity: message. With --log=file, the default under a
coding agent or with --json, the full LaTeX log is saved to
.texops/logs/<doc>.log instead of being streamed. Without a terminal, a missing
.texops.yaml is an error (exit 4). Exits 5 when a document fails to compile.

Examples:
  tx build
  tx build paper --json
  tx build --live`},

	{"status", `Show whether tx is logged in, as whom, with a session or an API token, and
when the credentials expire.

Exits 3 when there are no credentials or they have expired or were rejected,
so scripts can use it as a login check.

Examples:
  tx status
  tx status --json`},

	{"token", `Create, list and delete API tokens. Set a token as TX_API_TOKEN to use tx in
CI or from a coding agent without 'tx login'.

Examples:
  tx token create --name ci --expires-in 90d
  tx token list
  tx token delete ci --yes`},

	{"token create", `Create an API token and print it to stdout. The value is shown only once.

On a terminal, tx asks for a missing name or expiry. Without one, --name and
either --expires-in or --no-expiry are required (exit 2 otherwise).

Examples:
  tx token create --name ci --expires-in 90d
  tx token create --name laptop --no-expiry --json`},

	{"token list", `List API tokens with their name, prefix, expiry, last use and creation date.

Examples:
  tx token list
  tx token list --json`},

	{"token delete", `Delete an API token by name. Without a name, tx offers a list to pick from
(terminal only).

On a terminal tx asks for confirmation. Without one, or with --json, it
refuses (exit 2) and deletes nothing unless --yes is given.

Examples:
  tx token delete ci
  tx token delete ci --yes`},
}

func setupHelp(parser *flags.Parser) {
	parser.LongDescription = rootHelp
	for _, h := range commandHelp {
		cmd := parser.Command
		for name := range strings.FieldsSeq(h.path) {
			cmd = cmd.Find(name)
		}
		cmd.LongDescription = h.text
	}
	parser.Find("init").FindOptionByLongName("texlive").Description = fmt.Sprintf(
		"TeX Live version, e.g. %s (default: the newest the service supports; an unsupported version lists the valid ones)",
		TexliveVersions[0])
}

// writeHelp prints the help of the active command. go-flags would trim the
// indentation of the long description and rewrap it, so it is inserted as is.
func writeHelp(parser *flags.Parser, w io.Writer) {
	cmd := parser.Command
	for cmd.Active != nil {
		cmd = cmd.Active
	}
	desc := cmd.LongDescription
	cmd.LongDescription = ""
	moveChoicesToDescription(parser.Command)
	var buf bytes.Buffer
	parser.WriteHelp(&buf)

	help := buf.String()
	if desc == "" {
		_, _ = io.WriteString(w, help)
		return
	}
	_, afterUsage, _ := strings.Cut(help, "Usage:\n")
	usage, rest, _ := strings.Cut(afterUsage, "\n")
	_, _ = fmt.Fprintf(w, "Usage:\n%s\n\n%s\n%s", usage, desc, rest)
}

// moveChoicesToDescription lists long choice lists in the option description
// instead of after the flag, where go-flags would widen the whole flag column.
// It changes the parser for good, so it is only called right before exiting.
func moveChoicesToDescription(root *flags.Command) {
	var visit func(g *flags.Group)
	visit = func(g *flags.Group) {
		for _, opt := range g.Options() {
			if len(opt.Choices) <= 2 {
				continue
			}
			opt.Description = fmt.Sprintf("%s; one of: %s", opt.Description, strings.Join(opt.Choices, ", "))
			opt.Choices = nil
		}
		for _, sub := range g.Groups() {
			visit(sub)
		}
	}
	for c := root; c != nil; c = c.Active {
		visit(c.Group)
	}
}
