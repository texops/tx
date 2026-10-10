package cli_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

func assertGolden(t *testing.T, name string, r txRun) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, []byte(r.stdout), 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run 'go test ./internal/cli -run TestHelp -update' to create %s", path)
	assert.Equal(t, string(want), r.stdout,
		"%s\nthe golden files assume an 80-column help: go-flags reads the width from fd 0, which must not be a terminal (plain 'go test' ensures that)", r)
}

func TestHelp(t *testing.T) {
	t.Run("tx --help matches the golden file", func(t *testing.T) {
		r := runTx(t, nil, "--help")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Empty(t, r.stderr, r)
		assertGolden(t, "help-root.golden", r)
	})

	t.Run("tx build --help matches the golden file", func(t *testing.T) {
		r := runTx(t, nil, "build", "--help")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Empty(t, r.stderr, r)
		assertGolden(t, "help-build.golden", r)
	})

	t.Run("bare tx prints the root help to stderr and exits 2", func(t *testing.T) {
		help := runTx(t, nil, "--help")
		r := runTx(t, nil)

		require.Equal(t, cli.ExitUsage, r.code, r)
		assert.Empty(t, r.stdout, r)
		assert.Contains(t, r.stderr, help.stdout, r)
	})

	t.Run("missing subcommand prints the command help to stderr", func(t *testing.T) {
		r := runTx(t, nil, "token")

		require.Equal(t, cli.ExitUsage, r.code, r)
		assert.Empty(t, r.stdout, r)
		assert.Contains(t, r.stderr, "Create, list and delete API tokens.", r)
		assert.Contains(t, r.stderr, "\n  tx token delete ci --yes\n", r)
	})

	t.Run("login help names the non-interactive alternative", func(t *testing.T) {
		r := runTx(t, nil, "login", "--help")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "set TX_API_TOKEN instead", r)
		assert.Contains(t, r.stdout, "Examples:\n  tx login\n  tx login --no-browser --timeout 2m\n", r)
	})

	t.Run("init help describes the TeX Live version and lists the compilers", func(t *testing.T) {
		r := runTx(t, nil, "init", "--help")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "Examples:\n  tx init\n  tx init --texlive 2025 --compiler xelatex\n", r)
		assert.Contains(t, r.stdout, "--texlive=version    TeX Live version, e.g. 2025 (default: the newest", r)
		assert.Contains(t, r.stdout, "--compiler=name      LaTeX compiler (default: pdflatex); one of:", r)
	})

	t.Run("status help explains exit 3", func(t *testing.T) {
		r := runTx(t, nil, "status", "--help")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "Exits 3 when there are no credentials", r)
		assert.Contains(t, r.stdout, "Examples:\n  tx status\n  tx status --json\n", r)
	})

	t.Run("token create help names the required flags", func(t *testing.T) {
		r := runTx(t, nil, "token", "create", "--help")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "Without one, the name\n(as an argument or --name) and either --expires-in or --no-expiry are\nrequired", r)
		assert.Contains(t, r.stdout, "(max 10y)", r)
		assert.Contains(t, r.stdout, "Examples:\n  tx token create ci --expires-in 90d\n", r)
	})

	t.Run("token list help has examples", func(t *testing.T) {
		r := runTx(t, nil, "token", "list", "--help")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "Examples:\n  tx token list\n  tx token list --json\n", r)
	})

	t.Run("token delete help explains --yes", func(t *testing.T) {
		r := runTx(t, nil, "token", "delete", "--help")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "tx [OPTIONS] token delete [delete-OPTIONS] [name]\n", r)
		assert.Contains(t, r.stdout, "refuses (exit 2) and deletes nothing unless --yes is given", r)
		assert.Contains(t, r.stdout, "Examples:\n  tx token delete ci\n  tx token delete ci --yes\n", r)
	})
}
