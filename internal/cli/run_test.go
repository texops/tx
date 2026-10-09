package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

type fakeTexOps struct {
	srv *httptest.Server

	projectStatus    int
	projectBody      string
	sessionStatus    int
	syncStatus       int
	whoamiStatus     int
	deviceCodeStatus int
	instanceURL      string
	tokens           []map[string]any
	createdToken     map[string]any
	distributions    string
	done             map[string]map[string]any
	logs             map[string]string
	queued           bool
	outputStatus     int

	mu         sync.Mutex
	exchange   []string
	userAgents []requestUA
}

type requestUA struct {
	request   string
	userAgent string
}

func newFakeTexOps(t *testing.T) *fakeTexOps {
	t.Helper()
	f := &fakeTexOps{done: map[string]map[string]any{}, logs: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	t.Setenv("TX_API_URL", f.srv.URL)
	t.Setenv("TX_API_TOKEN", "test-token")
	return f
}

type recordingWriter struct {
	http.ResponseWriter

	status int
	body   bytes.Buffer
}

func (w *recordingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.body.Write(p)
	return w.ResponseWriter.Write(p)
}

func (f *fakeTexOps) serve(w http.ResponseWriter, r *http.Request) {
	reqBody := new(bytes.Buffer)
	reqBody.ReadFrom(r.Body)
	rw := &recordingWriter{ResponseWriter: w, status: http.StatusOK}
	f.route(rw, r, reqBody.Bytes())
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchange = append(f.exchange, fmt.Sprintf("%s %s %s\n  -> %d %s", r.Method, r.URL.Path, reqBody.String(), rw.status, rw.body.String()))
	f.userAgents = append(f.userAgents, requestUA{request: r.Method + " " + r.URL.Path, userAgent: r.UserAgent()})
}

func (f *fakeTexOps) route(w http.ResponseWriter, r *http.Request, body []byte) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/auth/whoami":
		if f.whoamiStatus != 0 {
			w.WriteHeader(f.whoamiStatus)
			w.Write([]byte(`{"error":"invalid token"}`))
			return
		}
		writeJSON(w, map[string]any{"user_id": "usr_1", "email": "user@example.com", "auth_method": "api_token"})
	case r.Method == http.MethodGet && r.URL.Path == "/auth/tokens":
		if f.tokens != nil {
			writeJSON(w, f.tokens)
			return
		}
		writeJSON(w, []map[string]any{{"id": "tok_1", "name": "ci", "prefix": "tx_secr", "expires_at": "2027-01-01T00:00:00Z", "created_at": "2026-10-01T00:00:00Z"}})
	case r.Method == http.MethodPost && r.URL.Path == "/auth/device-code":
		if f.deviceCodeStatus != 0 {
			w.WriteHeader(f.deviceCodeStatus)
			w.Write([]byte(`{"error":"device flow unavailable"}`))
			return
		}
		writeJSON(w, map[string]any{"device_code": "dev_1", "user_code": "ABCD-EFGH", "verification_url": f.srv.URL + "/verify", "expires_in": 1})
	case r.Method == http.MethodPost && r.URL.Path == "/auth/token":
		writeJSON(w, map[string]any{"jwt": "header.payload.sig", "expires_at": "2027-01-01T00:00:00Z"})
	case r.Method == http.MethodGet && r.URL.Path == "/api/distributions":
		if f.distributions == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(f.distributions))
	case r.Method == http.MethodDelete && r.URL.Path == "/auth/tokens/tok_1":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/auth/tokens":
		w.WriteHeader(http.StatusCreated)
		if f.createdToken != nil {
			writeJSON(w, f.createdToken)
			return
		}
		writeJSON(w, map[string]any{"token": "tx_secret_value", "id": "tok_1", "name": "ci", "prefix": "tx_secr", "created_at": "2026-10-01T00:00:00Z"})
	case r.Method == http.MethodPost && r.URL.Path == "/api/projects":
		if f.projectStatus != 0 {
			w.WriteHeader(f.projectStatus)
			w.Write([]byte(f.projectBody))
			return
		}
		writeJSON(w, map[string]any{"id": "prj_test", "name": "test", "distribution_version": "2025"})
	case r.Method == http.MethodPost && r.URL.Path == "/api/projects/prj_test/session":
		if f.sessionStatus != 0 {
			w.WriteHeader(f.sessionStatus)
			w.Write([]byte(`{"error":"session unavailable"}`))
			return
		}
		instanceURL := f.srv.URL
		if f.instanceURL != "" {
			instanceURL = f.instanceURL
		}
		writeJSON(w, map[string]any{"instance_url": instanceURL, "jwt": "instance-jwt"})
	case r.Method == http.MethodPost && r.URL.Path == "/projects/prj_test/sync":
		if f.syncStatus != 0 {
			w.WriteHeader(f.syncStatus)
			w.Write([]byte(`{"error":"disk full"}`))
			return
		}
		writeJSON(w, map[string]any{"missing": []string{}})
	case r.Method == http.MethodPost && r.URL.Path == "/projects/prj_test/build":
		var req struct {
			Main string `json:"main"`
		}
		json.Unmarshal(body, &req)
		done, ok := f.done[req.Main]
		if !ok {
			done = map[string]any{"status": "success", "pdfUrl": "/projects/prj_test/builds/bld_ok/output", "build_id": "bld_ok"}
		}
		doneData, _ := json.Marshal(done)
		w.Header().Set("Content-Type", "text/event-stream")
		if f.queued {
			fmt.Fprint(w, "event: queued\ndata: {\"message\":\"build queued, waiting for previous build to finish\"}\n\n")
		}
		fmt.Fprintf(w, "event: log\ndata: {\"message\":\"latexmk output for %s\"}\n\nevent: done\ndata: %s\n\n", req.Main, doneData)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/projects/prj_test/builds/") && strings.HasSuffix(r.URL.Path, "/output"):
		if f.outputStatus != 0 {
			w.WriteHeader(f.outputStatus)
			w.Write([]byte(`{"error":"storage unavailable"}`))
			return
		}
		w.Write([]byte("%PDF-1.4 test"))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/projects/prj_test/builds/") && strings.HasSuffix(r.URL.Path, "/log"):
		buildID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/projects/prj_test/builds/"), "/log")
		log, ok := f.logs[buildID]
		if !ok {
			w.WriteHeader(http.StatusGone)
			w.Write([]byte(`{"error":"build not found or expired"}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(log))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (f *fakeTexOps) exchanges() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.exchange, "\n")
}

func (f *fakeTexOps) recordedUserAgents() []requestUA {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]requestUA(nil), f.userAgents...)
}

type txRun struct {
	code     int
	stdout   string
	stderr   string
	requests string
}

func (r txRun) String() string {
	return fmt.Sprintf("exit code: %d\n--- stdout ---\n%s\n--- stderr ---\n%s\n--- requests ---\n%s", r.code, r.stdout, r.stderr, r.requests)
}

func runTx(t *testing.T, f *fakeTexOps, args ...string) txRun {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run("test", args, strings.NewReader(""), &stdout, &stderr)
	r := txRun{code: code, stdout: stdout.String(), stderr: stderr.String()}
	if f != nil {
		r.requests = f.exchanges()
	}
	return r
}

const projectConfig = `project_key: "k7Gx9mR2pL4wN8qY5vBt3a"
texlive: "2025"
documents:
  - name: paper
    main: paper.tex
  - name: slides
    main: slides.tex
`

func projectDir(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	if config != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(config), 0o600))
	}
	for _, name := range []string{"paper.tex", "slides.tex"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("\\documentclass{article}\\begin{document}Hi\\end{document}"), 0o600))
	}
	t.Chdir(dir)
	return dir
}

func noCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("TX_API_TOKEN", "")
	origGet := cli.KeyringGet
	cli.KeyringGet = func(service, user string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	t.Cleanup(func() { cli.KeyringGet = origGet })
	origPath := cli.CredentialsFilePath
	cli.CredentialsFilePath = func() string { return filepath.Join(t.TempDir(), "credentials.yaml") }
	t.Cleanup(func() { cli.CredentialsFilePath = origPath })
}

func failedBuild(reason, buildID string) map[string]any {
	done := map[string]any{"status": "error", "message": "build failed with " + reason}
	if reason != "" {
		done["reason"] = reason
	}
	if buildID != "" {
		done["build_id"] = buildID
	}
	return done
}

func TestRunExitCodes(t *testing.T) {
	t.Run("successful build exits 0", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "Build complete: 2 succeeded, 0 failed", r)
	})

	t.Run("help exits 0 and prints to stdout", func(t *testing.T) {
		r := runTx(t, nil, "--help")

		assert.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "Usage:", r)
		assert.Empty(t, r.stderr, r)
	})

	t.Run("subcommand help exits 0 and prints to stdout", func(t *testing.T) {
		r := runTx(t, nil, "build", "--help")

		assert.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stdout, "--no-cache", r)
		assert.Empty(t, r.stderr, r)
	})

	t.Run("version exits 0", func(t *testing.T) {
		r := runTx(t, nil, "--version")

		assert.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t, "tx test\n", r.stdout, r)
	})

	t.Run("unknown command exits 2", func(t *testing.T) {
		r := runTx(t, nil, "bogus")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Contains(t, r.stderr, "Unknown command `bogus'", r)
		assert.Empty(t, r.stdout, r)
	})

	t.Run("missing subcommand exits 2", func(t *testing.T) {
		r := runTx(t, nil, "token")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Contains(t, r.stderr, "Please specify one command of: create, delete or list", r)
		assert.Empty(t, r.stdout, r)
	})

	t.Run("no command prints help to stderr and exits 2", func(t *testing.T) {
		r := runTx(t, nil)

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Contains(t, r.stderr, "Usage:", r)
		assert.Contains(t, r.stderr, "Please specify one command of:", r)
		assert.Empty(t, r.stdout, r)
	})

	t.Run("unknown flag exits 2", func(t *testing.T) {
		r := runTx(t, nil, "build", "--bogus")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Contains(t, r.stderr, "unknown flag `bogus'", r)
	})

	t.Run("bad flag value exits 2", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, "")

		r := runTx(t, f, "init", "--compiler", "tex")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "Invalid value `tex' for option `--compiler'. Allowed values are: pdflatex, xelatex, lualatex, latex, platex or uplatex\n", r.stderr, r)
		assert.NoFileExists(t, ".texops.yaml", r)
		assert.Empty(t, r.requests, r)
	})

	t.Run("unknown document name exits 2", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "thesis")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "unknown document \"thesis\"; available: paper, slides\n", r.stderr, r)
		assert.Empty(t, r.requests, r)
	})

	t.Run("missing required input without a terminal exits 2", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "create")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "specify the token name (tx token create <name>, or --name) in non-interactive mode\n", r.stderr, r)
		assert.Empty(t, r.requests, r)
	})

	t.Run("status with a rejected TX_API_TOKEN exits 3 and names the token", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.whoamiStatus = http.StatusUnauthorized

		r := runTx(t, f, "status")

		assert.Equal(t, cli.ExitAuth, r.code, r)
		assert.Contains(t, r.stderr, "TX_API_TOKEN was rejected (invalid, expired or deleted); set a valid token, or unset it to use your 'tx login' session\n", r)
		assert.NotContains(t, r.stderr, "tx login' to re-authenticate", r)
	})

	t.Run("status without credentials exits 3", func(t *testing.T) {
		noCredentials(t)

		r := runTx(t, nil, "status")

		assert.Equal(t, cli.ExitAuth, r.code, r)
		assert.Equal(t, "not authenticated: ask the user to run 'tx login' in a terminal, or set TX_API_TOKEN (create one with 'tx token create')\n", r.stderr, r)
		assert.Empty(t, r.stdout, r)
	})

	t.Run("build without credentials exits 3", func(t *testing.T) {
		f := newFakeTexOps(t)
		noCredentials(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitAuth, r.code, r)
		assert.Contains(t, r.stderr, "not authenticated", r)
		assert.Empty(t, r.requests, r)
	})

	t.Run("401 from the API exits 3", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.projectStatus = http.StatusUnauthorized
		f.projectBody = `{"error":"invalid API token"}`
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitAuth, r.code, r)
		assert.Contains(t, r.stderr, "create project failed (401): invalid API token", r)
	})

	t.Run("401 from the session endpoint exits 3", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.sessionStatus = http.StatusUnauthorized
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitAuth, r.code, r)
		assert.Equal(t, 1, strings.Count(r.stderr, "get session failed (401): session unavailable"), r)
		assert.True(t, strings.HasSuffix(r.stderr, "\none or more documents failed to build\n"), r)
	})

	t.Run("missing config exits 4", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, "")

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitConfig, r.code, r)
		assert.Contains(t, r.stderr, "no project config found", r)
	})

	t.Run("invalid config exits 4", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, "texlive: \"2025\"\n")

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitConfig, r.code, r)
		assert.Equal(t, "invalid config: missing required field 'documents'\n", r.stderr, r)
	})

	t.Run("unsupported TeX Live version exits 4", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.projectStatus = http.StatusBadRequest
		f.projectBody = `{"error":"unsupported distribution version \"2099\" (supported: 2025, 2024)"}`
		projectDir(t, strings.Replace(projectConfig, `"2025"`, `"2099"`, 1))

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitConfig, r.code, r)
		assert.Equal(t, 1, strings.Count(r.stderr, `unsupported distribution version "2099"`), r)
	})

	t.Run("init when the config exists exits 4", func(t *testing.T) {
		projectDir(t, projectConfig)

		r := runTx(t, nil, "init")

		assert.Equal(t, cli.ExitConfig, r.code, r)
		assert.Equal(t, ".texops.yaml already exists\n", r.stderr, r)
	})

	t.Run("latex_error exits 5", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("latex_error", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		assert.Equal(t, cli.ExitBuildFailed, r.code, r)
	})

	t.Run("no_pdf exits 5", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("no_pdf", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		assert.Equal(t, cli.ExitBuildFailed, r.code, r)
	})

	t.Run("old server failure with a build_id exits 5", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		assert.Equal(t, cli.ExitBuildFailed, r.code, r)
	})

	t.Run("old server failure without a build_id exits 1", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("", "")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		assert.Equal(t, cli.ExitFailure, r.code, r)
	})

	t.Run("timeout exits 1", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("timeout", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		assert.Equal(t, cli.ExitFailure, r.code, r)
	})

	t.Run("internal reason exits 1", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("internal", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		assert.Equal(t, cli.ExitFailure, r.code, r)
	})

	t.Run("server 5xx exits 1", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.sessionStatus = http.StatusServiceUnavailable
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitFailure, r.code, r)
		assert.Contains(t, r.stderr, "get session failed (503): session unavailable", r)
	})

	t.Run("sync failure exits 1", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.syncStatus = http.StatusInternalServerError
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitFailure, r.code, r)
		assert.Equal(t, 1, strings.Count(r.stderr, "sync failed (500): disk full"), r)
		assert.True(t, strings.HasSuffix(r.stderr, "\none or more documents failed to build\n"), r)
	})

	t.Run("network failure exits 1", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		t.Setenv("TX_API_URL", srv.URL)
		t.Setenv("TX_API_TOKEN", "test-token")
		projectDir(t, projectConfig)

		r := runTx(t, nil, "build")

		assert.Equal(t, cli.ExitFailure, r.code, r)
		assert.Contains(t, r.stderr, "connection refused", r)
	})

	t.Run("latex_error in every document exits 5", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("latex_error", "bld_1")
		f.done["slides.tex"] = failedBuild("no_pdf", "bld_2")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitBuildFailed, r.code, r)
	})

	t.Run("latex_error and timeout in different documents exit 1", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("latex_error", "bld_1")
		f.done["slides.tex"] = failedBuild("timeout", "bld_2")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitFailure, r.code, r)
	})
}

func TestRunOutputStreams(t *testing.T) {
	t.Run("build progress goes to stderr and the summary to stdout", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Regexp(t, `^Build complete: 2 succeeded, 0 failed \(\d+\.\ds\)\n      paper => paper\.pdf\n      slides => slides\.pdf\n$`, r.stdout, r)
		assert.Contains(t, r.stderr, "Resolving project...\nProject ready\n", r)
		assert.Contains(t, r.stderr, "Session acquired\n", r)
		assert.Contains(t, r.stderr, "Building \"paper\" (paper.tex)...\n    latexmk output for paper.tex\nBuild complete (", r)
	})

	t.Run("build failure message is printed once to stderr", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.projectStatus = http.StatusInternalServerError
		f.projectBody = `{"error":"database unavailable"}`
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		require.Equal(t, cli.ExitFailure, r.code, r)
		assert.Equal(t, 1, strings.Count(r.stderr, "database unavailable"), r)
		assert.Equal(t, "Resolving project...\nFailed to resolve project\ncreate project failed (500): database unavailable\n", r.stderr, r)
		assert.Empty(t, r.stdout, r)
	})

	t.Run("failed document summary goes to stdout and the summary error once to stderr", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("latex_error", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stdout, "      paper: FAILED (latex_error)\n", r)
		assert.Equal(t, 1, strings.Count(r.stderr, "one or more documents failed to build"), r)
		assert.Equal(t, 1, strings.Count(r.stderr, "build failed with latex_error"), r)
	})

	t.Run("status fields go to stdout", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "status")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t, "Authenticated\n    Email:   user@example.com\n    Method:  API token\n    Expires: never\n", r.stdout, r)
		assert.Contains(t, r.stderr, "Checking authentication...", r)
	})

	t.Run("token list table goes to stdout", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "list")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t,
			"    NAME  PREFIX   EXPIRES      LAST USED  CREATED\n"+
				"    ci    tx_secr  01 Jan 2027  never      01 Oct 2026\n",
			r.stdout, r)
		assert.Equal(t, "Loading tokens...\n1 token(s)\n", r.stderr, r)
	})

	t.Run("login failure message is printed once to stderr", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.deviceCodeStatus = http.StatusInternalServerError

		r := runTx(t, f, "login")

		require.Equal(t, cli.ExitFailure, r.code, r)
		assert.Equal(t, "Requesting login code...\nFailed to request login code\ndevice code request failed (500): device flow unavailable\n", r.stderr, r)
		assert.Empty(t, r.stdout, r)
	})

	t.Run("token create prints only the token on stdout", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "token", "create", "--name", "ci", "--no-expiry")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t, "tx_secret_value\n", r.stdout, r)
		assert.Contains(t, r.stderr, "This token won't be shown again.", r)
	})
}

func TestRunBuildErrorKind(t *testing.T) {
	t.Run("latex_error is build_failed", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("latex_error", "bld_1")
		dir := projectDir(t, projectConfig)
		ui, buf := testUI()

		err := cli.RunBuild(t.Context(), dir, []string{"paper"}, false, false, cli.LogTerminal, ui)

		require.Error(t, err, "%s\n%s", buf.String(), f.exchanges())
		assert.Equal(t, cli.KindBuildFailed, cli.AsExitError(err).Kind, "%s\n%s", buf.String(), f.exchanges())
	})

	t.Run("timeout is timeout", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("timeout", "bld_1")
		dir := projectDir(t, projectConfig)
		ui, buf := testUI()

		err := cli.RunBuild(t.Context(), dir, []string{"paper"}, false, false, cli.LogTerminal, ui)

		require.Error(t, err, "%s\n%s", buf.String(), f.exchanges())
		assert.Equal(t, cli.KindTimeout, cli.AsExitError(err).Kind, "%s\n%s", buf.String(), f.exchanges())
	})

	t.Run("unreachable API is network", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		t.Setenv("TX_API_URL", srv.URL)
		t.Setenv("TX_API_TOKEN", "test-token")
		dir := projectDir(t, projectConfig)
		ui, buf := testUI()

		err := cli.RunBuild(t.Context(), dir, nil, false, false, cli.LogTerminal, ui)

		require.Error(t, err, buf.String())
		assert.Equal(t, cli.KindNetwork, cli.AsExitError(err).Kind, buf.String())
	})

	t.Run("unsupported version is config", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.projectStatus = http.StatusBadRequest
		f.projectBody = `{"error":"unsupported distribution version"}`
		dir := projectDir(t, projectConfig)
		ui, buf := testUI()

		err := cli.RunBuild(t.Context(), dir, nil, false, false, cli.LogTerminal, ui)

		require.Error(t, err, "%s\n%s", buf.String(), f.exchanges())
		assert.Equal(t, cli.KindConfig, cli.AsExitError(err).Kind, "%s\n%s", buf.String(), f.exchanges())
	})
}

func TestRunLogin(t *testing.T) {
	t.Run("login --no-browser prints the code on stderr and the result on stdout", func(t *testing.T) {
		f := newFakeTexOps(t)
		opened := stubLogin(t)

		r := runTx(t, f, "login", "--no-browser", "--timeout", "30s")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Empty(t, *opened, r)
		assert.Equal(t, "Logged in successfully\n", r.stdout, r)
		assert.Equal(t,
			"Requesting login code...\nLogin code received\n"+
				"Open "+f.srv.URL+"/verify and enter code ABCD-EFGH\n"+
				"Waiting for authorization...\nAuthorized\n",
			r.stderr, r)
	})

	t.Run("invalid --timeout exits 2", func(t *testing.T) {
		f := newFakeTexOps(t)

		r := runTx(t, f, "login", "--timeout", "soon")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "invalid argument for flag `--timeout' (expected time.Duration): time: invalid duration \"soon\"\n", r.stderr, r)
		assert.Empty(t, r.requests, r)
	})

	t.Run("status with expired credentials exits 3 with the expiry date", func(t *testing.T) {
		noCredentials(t)
		cli.KeyringGet = func(service, user string) (string, error) {
			return makeTestJWT(time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)), nil
		}

		r := runTx(t, nil, "status")

		assert.Equal(t, cli.ExitAuth, r.code, r)
		assert.Equal(t, "session expired on 2026-04-02; run 'tx login'\n", r.stderr, r)
		assert.Empty(t, r.stdout, r)
	})

	t.Run("build with expired credentials exits 3 with the expiry date", func(t *testing.T) {
		f := newFakeTexOps(t)
		noCredentials(t)
		cli.KeyringGet = func(service, user string) (string, error) {
			return makeTestJWT(time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)), nil
		}
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitAuth, r.code, r)
		assert.Equal(t, 1, strings.Count(r.stderr, "session expired on 2026-04-02; run 'tx login'\n"), r)
		assert.Empty(t, r.requests, r)
	})

	t.Run("build without credentials asks for the user or a token", func(t *testing.T) {
		f := newFakeTexOps(t)
		noCredentials(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")

		assert.Equal(t, cli.ExitAuth, r.code, r)
		assert.Equal(t, 1, strings.Count(r.stderr, "not authenticated: ask the user to run 'tx login' in a terminal, or set TX_API_TOKEN (create one with 'tx token create')\n"), r)
		assert.Empty(t, r.requests, r)
	})
}
