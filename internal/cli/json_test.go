package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

// decodeStdout requires stdout to hold exactly one JSON document and decodes it into v.
func decodeStdout(t *testing.T, r txRun, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	require.NoError(t, dec.Decode(v), r)
	var extra json.RawMessage
	require.ErrorIs(t, dec.Decode(&extra), io.EOF, "more than one JSON document on stdout\n%s", r)
}

// stdoutWithoutDurations returns stdout with every duration_ms field removed.
func stdoutWithoutDurations(t *testing.T, r txRun) string {
	t.Helper()
	var doc map[string]any
	decodeStdout(t, r, &doc)
	delete(doc, "duration_ms")
	docs, ok := doc["documents"].([]any)
	require.True(t, ok, r)
	for _, d := range docs {
		delete(d.(map[string]any), "duration_ms")
	}
	out, err := json.Marshal(doc)
	require.NoError(t, err, r)
	return string(out)
}

func durationsMS(t *testing.T, r txRun) []float64 {
	t.Helper()
	var doc struct {
		DurationMS *float64 `json:"duration_ms"`
		Documents  []struct {
			DurationMS *float64 `json:"duration_ms"`
		} `json:"documents"`
	}
	decodeStdout(t, r, &doc)
	require.NotNil(t, doc.DurationMS, r)
	out := make([]float64, 0, 1+len(doc.Documents))
	out = append(out, *doc.DurationMS)
	for _, d := range doc.Documents {
		require.NotNil(t, d.DurationMS, r)
		out = append(out, *d.DurationMS)
	}
	return out
}

func TestRunJSONBuild(t *testing.T) {
	t.Run("successful build prints one document and progress on stderr", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{
			"ok": true,
			"documents": [
				{"name": "paper", "main": "paper.tex", "status": "succeeded", "reason": null, "output": "paper.pdf", "log": null, "build_id": "bld_ok", "errors": [], "warnings": [], "truncated": false},
				{"name": "slides", "main": "slides.tex", "status": "succeeded", "reason": null, "output": "slides.pdf", "log": null, "build_id": "bld_ok", "errors": [], "warnings": [], "truncated": false}
			]
		}`, stdoutWithoutDurations(t, r), r)
		for _, d := range durationsMS(t, r) {
			assert.GreaterOrEqual(t, d, float64(0), r)
		}
		assert.Contains(t, r.stderr, "Resolving project...\nProject ready\n", r)
		assert.Contains(t, r.stderr, "Building \"paper\" (paper.tex)...\n", r)
		assert.NotContains(t, r.stderr, "Build complete: ", r)
	})

	t.Run("--json before the subcommand", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "--json", "build", "paper")

		require.Equal(t, cli.ExitOK, r.code, r)
		var doc struct {
			OK        bool `json:"ok"`
			Documents []struct {
				Name string `json:"name"`
			} `json:"documents"`
		}
		decodeStdout(t, r, &doc)
		assert.True(t, doc.OK, r)
		require.Len(t, doc.Documents, 1, r)
		assert.Equal(t, "paper", doc.Documents[0].Name, r)
	})

	t.Run("main includes the document directory", func(t *testing.T) {
		f := newFakeTexOps(t)
		dir := projectDir(t, "project_key: \"k7Gx9mR2pL4wN8qY5vBt3a\"\ntexlive: \"2025\"\ndocuments:\n  - name: thesis\n    main: thesis.tex\n    directory: thesis\n")
		require.NoError(t, os.Mkdir(dir+"/thesis", 0o750))
		require.NoError(t, os.WriteFile(dir+"/thesis/thesis.tex", []byte("\\documentclass{article}"), 0o600))

		r := runTx(t, f, "build", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		var doc struct {
			Documents []struct {
				Main   string `json:"main"`
				Output string `json:"output"`
			} `json:"documents"`
		}
		decodeStdout(t, r, &doc)
		require.Len(t, doc.Documents, 1, r)
		assert.Equal(t, "thesis/thesis.tex", doc.Documents[0].Main, r)
		assert.Equal(t, "thesis/thesis.pdf", doc.Documents[0].Output, r)
	})

	t.Run("failed build still prints the build document", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("latex_error", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "--json")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.JSONEq(t, `{
			"ok": false,
			"documents": [
				{"name": "paper", "main": "paper.tex", "status": "failed", "reason": "latex_error", "output": null, "log": null, "build_id": "bld_1", "errors": [], "warnings": [], "truncated": false},
				{"name": "slides", "main": "slides.tex", "status": "succeeded", "reason": null, "output": "slides.pdf", "log": null, "build_id": "bld_ok", "errors": [], "warnings": [], "truncated": false}
			]
		}`, stdoutWithoutDurations(t, r), r)
		assert.Equal(t, 1, strings.Count(r.stderr, "Build failed: build failed with latex_error\n"), r)
		assert.True(t, strings.HasSuffix(r.stderr, "\none or more documents failed to build\n"), r)
	})

	t.Run("old server failure with a build_id is a latex_error", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--json")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, stdoutWithoutDurations(t, r), `"reason":"latex_error"`, r)
	})

	t.Run("timeout reason comes from the server", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("timeout", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--json")

		require.Equal(t, cli.ExitFailure, r.code, r)
		assert.Contains(t, stdoutWithoutDurations(t, r), `"reason":"timeout"`, r)
	})

	t.Run("sync failure reason is sync", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.syncStatus = http.StatusInternalServerError
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "--json")

		require.Equal(t, cli.ExitFailure, r.code, r)
		assert.JSONEq(t, `{
			"ok": false,
			"documents": [
				{"name": "paper", "main": "paper.tex", "status": "failed", "reason": "sync", "output": null, "log": null, "build_id": null, "errors": [], "warnings": [], "truncated": false},
				{"name": "slides", "main": "slides.tex", "status": "failed", "reason": "sync", "output": null, "log": null, "build_id": null, "errors": [], "warnings": [], "truncated": false}
			]
		}`, stdoutWithoutDurations(t, r), r)
	})

	t.Run("rejected session reason is auth", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.sessionStatus = http.StatusUnauthorized
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--json")

		require.Equal(t, cli.ExitAuth, r.code, r)
		assert.Contains(t, stdoutWithoutDurations(t, r), `"reason":"auth"`, r)
	})

	t.Run("unreachable instance reason is network", func(t *testing.T) {
		f := newFakeTexOps(t)
		instance := httptest.NewServer(http.NotFoundHandler())
		instance.Close()
		f.instanceURL = instance.URL
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "--json")

		require.Equal(t, cli.ExitFailure, r.code, r)
		var doc struct {
			OK        bool `json:"ok"`
			Documents []struct {
				Name   string  `json:"name"`
				Status string  `json:"status"`
				Reason *string `json:"reason"`
			} `json:"documents"`
		}
		decodeStdout(t, r, &doc)
		assert.False(t, doc.OK, r)
		require.Len(t, doc.Documents, 2, r)
		for _, d := range doc.Documents {
			assert.Equal(t, "failed", d.Status, r)
			require.NotNil(t, d.Reason, r)
			assert.Equal(t, "network", *d.Reason, r)
		}
	})

	t.Run("unsupported TeX Live version reason is config", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.sessionStatus = http.StatusBadRequest
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "--json")

		require.Equal(t, cli.ExitConfig, r.code, r)
		assert.JSONEq(t, `{
			"ok": false,
			"documents": [
				{"name": "paper", "main": "paper.tex", "status": "failed", "reason": "config", "output": null, "log": null, "build_id": null, "errors": [], "warnings": [], "truncated": false},
				{"name": "slides", "main": "slides.tex", "status": "failed", "reason": "config", "output": null, "log": null, "build_id": null, "errors": [], "warnings": [], "truncated": false}
			]
		}`, stdoutWithoutDurations(t, r), r)
	})

	t.Run("failure before any document prints an error document", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, "")

		r := runTx(t, f, "build", "--json")

		require.Equal(t, cli.ExitConfig, r.code, r)
		assert.JSONEq(t, `{"error": {"code": "config", "message": "no project config found; run `+"`tx init`"+` to set up your project"}}`, r.stdout, r)
		assert.Equal(t, "no project config found; run `tx init` to set up your project\n", r.stderr, r)
	})

	t.Run("unreachable API prints a network error document", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.srv.Close()
		projectDir(t, projectConfig)

		r := runTx(t, nil, "build", "--json")

		require.Equal(t, cli.ExitFailure, r.code, r)
		var doc struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeStdout(t, r, &doc)
		assert.Equal(t, "network", doc.Error.Code, r)
		assert.Contains(t, doc.Error.Message, "connection refused", r)
		assert.Equal(t, 1, strings.Count(r.stderr, doc.Error.Message), r)
	})

	t.Run("--json with --live exits 2", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "--json", "--live")

		require.Equal(t, cli.ExitUsage, r.code, r)
		assert.JSONEq(t, `{"error": {"code": "usage", "message": "--json cannot be used with --live"}}`, r.stdout, r)
		assert.Equal(t, "--json cannot be used with --live\n", r.stderr, r)
		assert.Empty(t, r.requests, r)
	})
}

func TestRunJSONStatus(t *testing.T) {
	t.Run("authenticated with an API token from the environment", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "status", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{"authenticated": true, "email": "user@example.com", "method": "api_token", "source": "env", "expires_at": null}`, r.stdout, r)
		assert.Equal(t, "Checking authentication...\nConnected\n", r.stderr, r)
	})

	t.Run("authenticated with a JWT from the keyring", func(t *testing.T) {
		f := newFakeTexOps(t)
		noCredentials(t)
		cli.KeyringGet = func(service, user string) (string, error) {
			return "header.payload.sig", nil
		}

		r := runTx(t, f, "status", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		var doc map[string]any
		decodeStdout(t, r, &doc)
		assert.Equal(t, "keyring", doc["source"], r)
	})

	t.Run("authenticated with a JWT from the credentials file", func(t *testing.T) {
		f := newFakeTexOps(t)
		noCredentials(t)
		credPath := withTempCredentialsDir(t)
		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0o700))
		require.NoError(t, os.WriteFile(credPath, []byte("jwt: "+makeTestJWT(time.Now().Add(time.Hour))+"\n"), 0o600))

		r := runTx(t, f, "status", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		var doc map[string]any
		decodeStdout(t, r, &doc)
		assert.Equal(t, true, doc["authenticated"], r)
		assert.Equal(t, "file", doc["source"], r)
	})

	t.Run("not authenticated exits 3", func(t *testing.T) {
		noCredentials(t)

		r := runTx(t, nil, "status", "--json")

		require.Equal(t, cli.ExitAuth, r.code, r)
		assert.JSONEq(t, `{"authenticated": false}`, r.stdout, r)
		assert.Equal(t, "Not authenticated. Run 'tx login' to log in to TexOps.\n", r.stderr, r)
	})

	t.Run("rejected token exits 3", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.whoamiStatus = http.StatusUnauthorized

		r := runTx(t, f, "status", "--json")

		require.Equal(t, cli.ExitAuth, r.code, r)
		assert.JSONEq(t, `{"authenticated": false}`, r.stdout, r)
		assert.Equal(t, "Checking authentication...\nAuthentication check failed\nSession expired. Run 'tx login' to re-authenticate.\n", r.stderr, r)
	})
}

func TestRunJSONToken(t *testing.T) {
	t.Run("token list", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "list", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `[{"name": "ci", "prefix": "tx_secr", "expires_at": "2027-01-01T00:00:00Z", "last_used_at": null, "created_at": "2026-10-01T00:00:00Z"}]`, r.stdout, r)
		assert.Equal(t, "Loading tokens...\n1 token(s)\n", r.stderr, r)
	})

	t.Run("empty token list is an empty array", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.tokens = []map[string]any{}

		r := runTx(t, f, "token", "list", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `[]`, r.stdout, r)
	})

	t.Run("token create", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "create", "--name", "ci", "--no-expiry", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{"name": "ci", "token": "tx_secret_value", "expires_at": null}`, r.stdout, r)
		assert.Equal(t, "Creating token...\nToken created\n", r.stderr, r)
	})

	t.Run("token create falls back to the requested name", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.createdToken = map[string]any{"token": "tx_secret_value", "id": "tok_2", "prefix": "tx_secr", "created_at": "2026-10-01T00:00:00Z"}

		r := runTx(t, f, "token", "create", "--name", "deploy", "--no-expiry", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{"name": "deploy", "token": "tx_secret_value", "expires_at": null}`, r.stdout, r)
	})

	t.Run("token create without a name prints an error document", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "--json", "token", "create")

		require.Equal(t, cli.ExitUsage, r.code, r)
		assert.JSONEq(t, `{"error": {"code": "usage", "message": "specify --name in non-interactive mode"}}`, r.stdout, r)
		assert.Equal(t, "specify --name in non-interactive mode\n", r.stderr, r)
	})

	t.Run("token delete", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "delete", "ci", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{"deleted": "ci"}`, r.stdout, r)
		assert.Equal(t, "Loading tokens...\n1 token(s)\nDeleting token...\nToken deleted\n", r.stderr, r)
		assert.Contains(t, r.requests, "DELETE /auth/tokens/tok_1", r)
	})
}

func TestRunJSONInit(t *testing.T) {
	t.Run("discovered documents", func(t *testing.T) {
		dir := projectDir(t, "")

		r := runTx(t, nil, "init", "--texlive", "2024", "--compiler", "xelatex", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{"config": ".texops.yaml", "texlive": "2024", "compiler": "xelatex", "documents": [{"name": "paper", "main": "paper.tex"}, {"name": "slides", "main": "slides.tex"}]}`, r.stdout, r)
		assert.Empty(t, r.stderr, r)
		assert.FileExists(t, dir+"/.texops.yaml", r)
	})

	t.Run("documents in a directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(dir+"/thesis", 0o750))
		require.NoError(t, os.WriteFile(dir+"/thesis/thesis.tex", []byte("\\documentclass{article}"), 0o600))
		t.Chdir(dir)

		r := runTx(t, nil, "--json", "init")

		require.Equal(t, cli.ExitOK, r.code, r)
		var doc map[string]any
		decodeStdout(t, r, &doc)
		assert.Equal(t, []any{map[string]any{"name": "thesis", "main": "thesis.tex", "directory": "thesis"}}, doc["documents"], r)
	})

	t.Run("existing config prints an error document", func(t *testing.T) {
		projectDir(t, projectConfig)

		r := runTx(t, nil, "init", "--json")

		require.Equal(t, cli.ExitConfig, r.code, r)
		assert.JSONEq(t, `{"error": {"code": "config", "message": ".texops.yaml already exists"}}`, r.stdout, r)
		assert.Equal(t, ".texops.yaml already exists\n", r.stderr, r)
	})
}

func TestRunJSONLogin(t *testing.T) {
	t.Run("successful login", func(t *testing.T) {
		f := newFakeTexOps(t)
		origSet := cli.KeyringSet
		var stored string
		cli.KeyringSet = func(service, user, key string) error {
			stored = key
			return nil
		}
		t.Cleanup(func() { cli.KeyringSet = origSet })
		origBrowser := cli.OpenBrowser
		cli.OpenBrowser = func(string) error { return nil }
		t.Cleanup(func() { cli.OpenBrowser = origBrowser })
		origInterval := cli.PollInterval
		cli.PollInterval = time.Millisecond
		t.Cleanup(func() { cli.PollInterval = origInterval })

		r := runTx(t, f, "login", "--json")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.JSONEq(t, `{"authenticated": true}`, r.stdout, r)
		assert.Equal(t, "header.payload.sig", stored, r)
		assert.Contains(t, r.stderr, "Your login code: ABCD-EFGH\n", r)
	})

	t.Run("failed login prints an error document", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.deviceCodeStatus = http.StatusInternalServerError

		r := runTx(t, f, "login", "--json")

		require.Equal(t, cli.ExitFailure, r.code, r)
		assert.JSONEq(t, `{"error": {"code": "internal", "message": "device code request failed (500): device flow unavailable"}}`, r.stdout, r)
	})
}

func TestRunJSONUsage(t *testing.T) {
	t.Run("unknown command prints an error document", func(t *testing.T) {
		r := runTx(t, nil, "bogus", "--json")

		require.Equal(t, cli.ExitUsage, r.code, r)
		var doc struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeStdout(t, r, &doc)
		assert.Equal(t, "usage", doc.Error.Code, r)
		assert.Contains(t, doc.Error.Message, "Unknown command `bogus'", r)
	})

	t.Run("no command prints an error document", func(t *testing.T) {
		r := runTx(t, nil, "--json")

		require.Equal(t, cli.ExitUsage, r.code, r)
		var doc struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeStdout(t, r, &doc)
		assert.Equal(t, "usage", doc.Error.Code, r)
		assert.Contains(t, doc.Error.Message, "Please specify one command of", r)
	})

	t.Run("unknown flag prints an error document", func(t *testing.T) {
		r := runTx(t, nil, "--json", "build", "--bogus")

		require.Equal(t, cli.ExitUsage, r.code, r)
		assert.JSONEq(t, "{\"error\": {\"code\": \"usage\", \"message\": \"unknown flag `bogus'\"}}", r.stdout, r)
	})

	t.Run("--json after -- is not the flag", func(t *testing.T) {
		r := runTx(t, nil, "bogus", "--", "--json")

		require.Equal(t, cli.ExitUsage, r.code, r)
		assert.Empty(t, r.stdout, r)
	})
}

func TestUIJSON(t *testing.T) {
	t.Run("text results go to stderr in JSON mode", func(t *testing.T) {
		var stdout, stderr strings.Builder
		ui := cli.NewUIWithSplitOptions(&stdout, &stderr, true, nil)
		ui.SetJSON(true)

		ui.Result("result")
		ui.Success("done")
		sp := ui.Spin("Working...")
		sp.Stop("Worked")
		require.NoError(t, ui.WriteJSON(map[string]string{"k": "<v>"}))

		assert.Equal(t, "{\"k\":\"<v>\"}\n", stdout.String())
		assert.Equal(t, "result\ndone\nWorking...\nWorked\n", stderr.String())
		assert.False(t, ui.IsTTY())
		assert.False(t, ui.IsInteractive())
	})

	t.Run("JSON mode never prompts", func(t *testing.T) {
		var stdout, stderr strings.Builder
		ui := cli.NewUIWithSplitOptions(&stdout, &stderr, true, strings.NewReader("1\n"))
		ui.SetJSON(true)

		_, err := ui.Select("Pick", []string{"a"})

		require.Error(t, err)
		assert.Empty(t, stdout.String())
	})
}
