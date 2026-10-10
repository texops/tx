package cli_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	flags "github.com/jessevdk/go-flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

const builtinVersions = "2025, 2024, 2023, 2022, 2021, 2020, 2019, 2018, 2017, 2016, 2015, 2014, 2013"

const apiDistributions = `{"versions": ["2026", "2025", "2024"], "default": "2026"}`

// serveDistributions points TX_API_URL at a server that answers only
// unauthenticated GET /api/distributions with body.
func serveDistributions(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/distributions" && r.Header.Get("Authorization") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TX_API_URL", srv.URL)
}

// unreachableAPI points TX_API_URL at a closed server.
func unreachableAPI(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	t.Setenv("TX_API_URL", srv.URL)
}

// enterReader returns first on the first Read and an Enter key press on every
// later Read, so each interactive prompt picks its preselected option.
type enterReader struct {
	first string
	sent  atomic.Bool
}

func (r *enterReader) Read(p []byte) (int, error) {
	if r.first != "" && r.sent.CompareAndSwap(false, true) {
		return copy(p, r.first), nil
	}
	return copy(p, "\r"), nil
}

func TestTokenDeleteConfirmation(t *testing.T) {
	t.Run("without a terminal and without --yes exits 2 and sends no DELETE", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "delete", "ci")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "refusing to delete token \"ci\" without confirmation; pass --yes\n", r.stderr, r)
		assert.Empty(t, r.stdout, r)
		assert.NotContains(t, r.requests, "DELETE", r)
	})

	t.Run("--json without --yes prints an error document", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "delete", "ci", "--json")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.JSONEq(t, `{"error": {"code": "usage", "message": "refusing to delete token \"ci\" without confirmation; pass --yes"}}`, r.stdout, r)
		assert.NotContains(t, r.requests, "DELETE", r)
	})

	t.Run("--yes deletes without a terminal", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "delete", "--yes", "ci")

		assert.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t, "Loading tokens...\n1 token(s)\nDeleting token...\nToken deleted\n", r.stderr, r)
		assert.Contains(t, r.requests, "DELETE /auth/tokens/tok_1", r)
	})

	t.Run("-y deletes without a terminal", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "delete", "-y", "ci")

		assert.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.requests, "DELETE /auth/tokens/tok_1", r)
	})

	t.Run("unknown token with --yes exits 2", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "delete", "--yes", "nope")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Contains(t, r.stderr, "token \"nope\" not found\n", r)
		assert.NotContains(t, r.requests, "DELETE", r)
	})

	t.Run("--yes skips the prompt on a terminal", func(t *testing.T) {
		f := newFakeTexOps(t)
		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, strings.NewReader("n\n"))

		err := (&cli.TokenDeleteCmd{Yes: true, UI: ui}).Execute([]string{"ci"})

		require.NoError(t, err, "%s\n%s", buf.String(), f.exchanges())
		assert.NotContains(t, buf.String(), "[Y/n]", f.exchanges())
		assert.Contains(t, f.exchanges(), "DELETE /auth/tokens/tok_1", buf.String())
	})

	t.Run("a terminal without --yes prompts and deletes on yes", func(t *testing.T) {
		f := newFakeTexOps(t)
		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, strings.NewReader("y\n"))

		err := (&cli.TokenDeleteCmd{UI: ui}).Execute([]string{"ci"})

		require.NoError(t, err, "%s\n%s", buf.String(), f.exchanges())
		assert.Contains(t, buf.String(), "Delete token \"ci\"? [Y/n]", f.exchanges())
		assert.Contains(t, f.exchanges(), "DELETE /auth/tokens/tok_1", buf.String())
	})

	t.Run("no name with piped stdin and without --yes exits 2 and sends no request", func(t *testing.T) {
		f := newFakeTexOps(t)
		buf := &bytes.Buffer{}
		ui := cli.NewUIWithTTYOptions(buf, true, false, strings.NewReader(""))

		err := (&cli.TokenDeleteCmd{UI: ui}).Execute(nil)

		require.Error(t, err, buf.String())
		assert.Equal(t, cli.ExitUsage, cli.AsExitError(err).Code, buf.String())
		assert.Equal(t, "refusing to delete a token without confirmation; pass --yes", err.Error(), buf.String())
		assert.Empty(t, f.exchanges(), buf.String())
	})

	t.Run("without credentials, a terminal or --yes reports the usage error first", func(t *testing.T) {
		noCredentials(t)

		r := runTx(t, nil, "token", "delete", "ci")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "refusing to delete token \"ci\" without confirmation; pass --yes\n", r.stderr, r)
	})

	t.Run("a terminal without --yes keeps the token on no", func(t *testing.T) {
		f := newFakeTexOps(t)
		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, strings.NewReader("n\n"))

		err := (&cli.TokenDeleteCmd{UI: ui}).Execute([]string{"ci"})

		require.NoError(t, err, "%s\n%s", buf.String(), f.exchanges())
		assert.Contains(t, buf.String(), "Cancelled.", f.exchanges())
		assert.NotContains(t, f.exchanges(), "DELETE", buf.String())
	})
}

func TestCompilerChoices(t *testing.T) {
	t.Run("--compiler choices match AllowedCompilers", func(t *testing.T) {
		parser := flags.NewParser(&cli.Options{}, flags.None)

		opt := parser.Find("init").FindOptionByLongName("compiler")

		require.NotNil(t, opt)
		assert.Equal(t, cli.AllowedCompilers, opt.Choices)
	})

	t.Run("unknown compiler exits 2 without writing a config", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, "")

		r := runTx(t, f, "init", "--compiler", "pdftex")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Contains(t, r.stderr, "Invalid value `pdftex' for option `--compiler'", r)
		assert.NoFileExists(t, ".texops.yaml", r)
	})
}

func TestInitTexlive(t *testing.T) {
	t.Run("unsupported --texlive exits 2 and lists the API's versions", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.distributions = apiDistributions
		projectDir(t, "")

		r := runTx(t, f, "init", "--texlive", "2099")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "invalid --texlive \"2099\": supported versions are 2026, 2025, 2024\n", r.stderr, r)
		assert.Empty(t, r.stdout, r)
		assert.Contains(t, r.requests, "GET /api/distributions", r)
		assert.NoFileExists(t, ".texops.yaml", r)
	})

	t.Run("unsupported --texlive with --json prints an error document", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.distributions = apiDistributions
		projectDir(t, "")

		r := runTx(t, f, "init", "--texlive", "2099", "--json")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.JSONEq(t, `{"error": {"code": "usage", "message": "invalid --texlive \"2099\": supported versions are 2026, 2025, 2024"}}`, r.stdout, r)
		assert.NoFileExists(t, ".texops.yaml", r)
	})

	t.Run("unsupported --texlive lists the built-in versions when the API is unreachable", func(t *testing.T) {
		unreachableAPI(t)
		projectDir(t, "")

		r := runTx(t, nil, "init", "--texlive", "2099")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "invalid --texlive \"2099\": supported versions are "+builtinVersions+"\n", r.stderr, r)
		assert.NoFileExists(t, ".texops.yaml", r)
	})

	t.Run("unsupported --texlive lists the built-in versions when the endpoint is missing", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, "")

		r := runTx(t, f, "init", "--texlive", "2099")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "invalid --texlive \"2099\": supported versions are "+builtinVersions+"\n", r.stderr, r)
		assert.Contains(t, r.requests, "GET /api/distributions", r)
		assert.NoFileExists(t, ".texops.yaml", r)
	})

	t.Run("an empty version list falls back to the built-in versions", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.distributions = `{"versions": [], "default": ""}`
		projectDir(t, "")

		r := runTx(t, f, "init", "--texlive", "2099")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "invalid --texlive \"2099\": supported versions are "+builtinVersions+"\n", r.stderr, r)
	})

	t.Run("a version only the API knows is accepted", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.distributions = apiDistributions
		dir := projectDir(t, "")

		r := runTx(t, f, "init", "--texlive", "2026")

		require.Equal(t, cli.ExitOK, r.code, r)
		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err, r)
		assert.Contains(t, string(data), `texlive: "2026"`, r)
	})

	t.Run("the API default is used without a terminal", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.distributions = `{"versions": ["2026", "2025"], "default": "2025"}`
		dir := projectDir(t, "")

		r := runTx(t, f, "init")

		require.Equal(t, cli.ExitOK, r.code, r)
		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err, r)
		assert.Contains(t, string(data), `texlive: "2025"`, r)
	})

	t.Run("a default missing from the versions falls back to the newest version", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.distributions = `{"versions": ["2026", "2025"], "default": "1999"}`
		dir := projectDir(t, "")

		r := runTx(t, f, "init")

		require.Equal(t, cli.ExitOK, r.code, r)
		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err, r)
		assert.Contains(t, string(data), `texlive: "2026"`, r)
	})

	t.Run("the built-in default is used when the API is unreachable", func(t *testing.T) {
		unreachableAPI(t)
		dir := projectDir(t, "")

		r := runTx(t, nil, "init")

		require.Equal(t, cli.ExitOK, r.code, r)
		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err, r)
		assert.Contains(t, string(data), `texlive: "2025"`, r)
	})

	t.Run("the interactive prompt preselects the API's first version", func(t *testing.T) {
		serveDistributions(t, apiDistributions)
		dir := projectDir(t, "")
		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, &enterReader{})

		err := (&cli.InitCmd{Main: "main.tex", UI: ui}).Execute(nil)

		require.NoError(t, err, buf.String())
		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err, buf.String())
		assert.Contains(t, string(data), `texlive: "2026"`, buf.String())
		assert.Contains(t, buf.String(), "TexLive version:", buf.String())
	})
}

func TestTokenListColumns(t *testing.T) {
	t.Run("NAME column fits the longest name", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.tokens = []map[string]any{
			{"id": "tok_1", "name": "github-actions-deploy-prod", "prefix": "tx_aaaa", "expires_at": "2027-01-01T00:00:00Z", "created_at": "2026-10-01T00:00:00Z"},
			{"id": "tok_2", "name": "dev", "prefix": "tx_bbbb", "last_used_at": "2026-10-05T00:00:00Z", "created_at": "2026-10-02T00:00:00Z"},
		}

		r := runTx(t, f, "token", "list")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t,
			"    NAME                        PREFIX   EXPIRES      LAST USED    CREATED\n"+
				"    github-actions-deploy-prod  tx_aaaa  01 Jan 2027  never        01 Oct 2026\n"+
				"    dev                         tx_bbbb  never        05 Oct 2026  02 Oct 2026\n",
			r.stdout, r)
	})

	t.Run("PREFIX column fits real 17-character prefixes", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.tokens = []map[string]any{
			{"id": "tok_1", "name": "ci", "prefix": "texops_token_ISDR", "expires_at": "2027-01-01T00:00:00Z", "created_at": "2026-10-01T00:00:00Z"},
			{"id": "tok_2", "name": "dev", "prefix": "texops_token_abcd", "last_used_at": "2026-10-05T00:00:00Z", "created_at": "2026-10-02T00:00:00Z"},
		}

		r := runTx(t, f, "token", "list")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t,
			"    NAME  PREFIX             EXPIRES      LAST USED    CREATED\n"+
				"    ci    texops_token_ISDR  01 Jan 2027  never        01 Oct 2026\n"+
				"    dev   texops_token_abcd  never        05 Oct 2026  02 Oct 2026\n",
			r.stdout, r)
	})

	t.Run("NAME column counts characters, not bytes", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.tokens = []map[string]any{
			{"id": "tok_1", "name": "Déploiement", "prefix": "tx_aaaa", "last_used_at": "2026-10-03T00:00:00Z", "created_at": "2026-10-01T00:00:00Z"},
			{"id": "tok_2", "name": "dev", "prefix": "tx_bbbb", "expires_at": "2027-01-01T00:00:00Z", "created_at": "2026-10-02T00:00:00Z"},
		}

		r := runTx(t, f, "token", "list")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t,
			"    NAME         PREFIX   EXPIRES      LAST USED    CREATED\n"+
				"    Déploiement  tx_aaaa  never        03 Oct 2026  01 Oct 2026\n"+
				"    dev          tx_bbbb  01 Jan 2027  never        02 Oct 2026\n",
			r.stdout, r)
	})
}

func TestTokenCreateName(t *testing.T) {
	createdWithoutName := map[string]any{"token": "tx_secret_value", "id": "tok_2", "prefix": "tx_secr", "created_at": "2026-10-01T00:00:00Z"}

	t.Run("positional name", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.createdToken = createdWithoutName

		r := runTx(t, f, "token", "create", "deploy", "--no-expiry", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{"name": "deploy", "token": "tx_secret_value", "expires_at": null}`, r.stdout, r)
	})

	t.Run("positional name equal to --name", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.createdToken = createdWithoutName

		r := runTx(t, f, "token", "create", "deploy", "--name", "deploy", "--no-expiry", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{"name": "deploy", "token": "tx_secret_value", "expires_at": null}`, r.stdout, r)
	})

	t.Run("positional name different from --name exits 2", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "create", "deploy", "--name", "ci", "--no-expiry")

		require.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "token name given twice: \"deploy\" and --name \"ci\"\n", r.stderr, r)
		assert.NotContains(t, strings.Join(f.exchange, "\n"), "POST /auth/tokens", r)
	})

	t.Run("two positional names exit 2", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "create", "deploy", "ci", "--no-expiry")

		require.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "too many arguments: tx token create takes at most one name\n", r.stderr, r)
		assert.NotContains(t, strings.Join(f.exchange, "\n"), "POST /auth/tokens", r)
	})
}

func TestBuildProjectKey(t *testing.T) {
	configWithoutKey := "texlive: \"2025\"\ndocuments:\n  - name: paper\n    main: paper.tex\n"

	t.Run("failed project creation leaves the config untouched", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.projectStatus = http.StatusBadRequest
		f.projectBody = `{"error":"unsupported distribution version \"2099\" (supported: 2025, 2024)"}`
		dir := projectDir(t, configWithoutKey)

		r := runTx(t, f, "build")

		require.Equal(t, cli.ExitConfig, r.code, r)
		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		assert.Equal(t, configWithoutKey, string(data), r)
	})

	t.Run("successful project creation saves the key that was sent", func(t *testing.T) {
		f := newFakeTexOps(t)
		dir := projectDir(t, "---\n"+configWithoutKey)

		r := runTx(t, f, "build")

		require.Equal(t, cli.ExitOK, r.code, r)
		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		config, err := cli.ParseConfig(string(data))
		require.NoError(t, err, string(data))
		assert.Len(t, config.ProjectKey, 22, string(data))
		assert.Contains(t, f.exchanges(), config.ProjectKey, r)
		assert.True(t, strings.HasPrefix(string(data), "---\nproject_key: "), string(data))
	})
}
