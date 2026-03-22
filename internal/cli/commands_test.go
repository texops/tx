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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

// testUI creates a non-TTY UI that writes to a buffer, for use in tests.
func testUI() (*cli.UI, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return cli.NewUIWithOptions(buf, false, nil), buf
}

// mockKeyringForAuth sets up KeyringGet to return a JWT when asked for "jwt"
// and fall through to api-key otherwise. This is the common mock for tests
// that use ResolveAuth.
func mockKeyringForAuth(t *testing.T, token string) {
	t.Helper()
	origGet := cli.KeyringGet
	cli.KeyringGet = func(service, user string) (string, error) {
		if user == "jwt" {
			return token, nil
		}
		return "", fmt.Errorf("not found")
	}
	t.Cleanup(func() { cli.KeyringGet = origGet })
}

func TestLoginCmd(t *testing.T) {
	t.Run("successful device code flow", func(t *testing.T) {
		// Track what was stored in keyring
		var storedService, storedUser, storedJWT string
		origSet := cli.KeyringSet
		cli.KeyringSet = func(service, user, key string) error {
			storedService = service
			storedUser = user
			storedJWT = key
			return nil
		}
		defer func() { cli.KeyringSet = origSet }()

		// Suppress browser opening
		origBrowser := cli.OpenBrowser
		cli.OpenBrowser = func(url string) error { return nil }
		defer func() { cli.OpenBrowser = origBrowser }()

		// Speed up polling
		origInterval := cli.PollInterval
		cli.PollInterval = 1 * time.Millisecond
		defer func() { cli.PollInterval = origInterval }()

		var pollCount int32

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/auth/device-code":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"device_code":      "dc_test123",
					"user_code":        "ABCD-1234",
					"verification_url": "https://texops.example.com/auth/verify",
					"expires_in":       900,
					"interval":         1,
				})
			case "/auth/token":
				count := atomic.AddInt32(&pollCount, 1)
				w.Header().Set("Content-Type", "application/json")
				if count < 3 {
					// First two polls: pending
					w.WriteHeader(http.StatusPreconditionRequired)
					json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				} else {
					// Third poll: authorized
					json.NewEncoder(w).Encode(map[string]any{
						"jwt":        "eyJhbGciOi.test-jwt-token.sig",
						"expires_at": "2026-03-30T00:00:00Z",
					})
				}
			default:
				w.WriteHeader(404)
			}
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.LoginCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		assert.Equal(t, "texops", storedService)
		assert.Equal(t, "jwt", storedUser)
		assert.Equal(t, "eyJhbGciOi.test-jwt-token.sig", storedJWT)
		assert.Contains(t, buf.String(), "ABCD-1234")
		assert.Contains(t, buf.String(), "Logged in successfully")
		assert.True(t, pollCount >= 3, "should have polled at least 3 times")
	})

	t.Run("device code expired", func(t *testing.T) {
		origBrowser := cli.OpenBrowser
		cli.OpenBrowser = func(url string) error { return nil }
		defer func() { cli.OpenBrowser = origBrowser }()

		origInterval := cli.PollInterval
		cli.PollInterval = 1 * time.Millisecond
		defer func() { cli.PollInterval = origInterval }()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/auth/device-code":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"device_code":      "dc_test_exp",
					"user_code":        "EXPD-5678",
					"verification_url": "https://texops.example.com/auth/verify",
					"expires_in":       900,
					"interval":         1,
				})
			case "/auth/token":
				w.WriteHeader(http.StatusGone)
				json.NewEncoder(w).Encode(map[string]string{"error": "expired"})
			default:
				w.WriteHeader(404)
			}
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, _ := testUI()
		cmd := &cli.LoginCmd{UI: ui}
		err := cmd.Execute(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
	})

	t.Run("device code request fails", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, _ := testUI()
		cmd := &cli.LoginCmd{UI: ui}
		err := cmd.Execute(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "device code request failed")
	})

	t.Run("stores JWT in file when keyring unavailable", func(t *testing.T) {
		origSet := cli.KeyringSet
		cli.KeyringSet = func(service, user, key string) error {
			return fmt.Errorf("D-Bus connection failed")
		}
		defer func() { cli.KeyringSet = origSet }()

		origBrowser := cli.OpenBrowser
		cli.OpenBrowser = func(url string) error { return nil }
		defer func() { cli.OpenBrowser = origBrowser }()

		origInterval := cli.PollInterval
		cli.PollInterval = 1 * time.Millisecond
		defer func() { cli.PollInterval = origInterval }()

		tmpDir := t.TempDir()
		credPath := filepath.Join(tmpDir, "credentials.yaml")
		origPath := cli.CredentialsFilePath
		cli.CredentialsFilePath = func() string { return credPath }
		defer func() { cli.CredentialsFilePath = origPath }()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/auth/device-code":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"device_code":      "dc_file",
					"user_code":        "FILE-1234",
					"verification_url": "https://texops.example.com/auth/verify",
					"expires_in":       900,
					"interval":         1,
				})
			case "/auth/token":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"jwt":        "file-stored-jwt-token",
					"expires_at": "2026-03-30T00:00:00Z",
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.LoginCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "Logged in successfully")

		// Verify JWT was stored in file
		data, err := os.ReadFile(credPath)
		require.NoError(t, err)
		assert.Contains(t, string(data), "file-stored-jwt-token")
	})
}

func TestDiscoverDocuments(t *testing.T) {
	t.Run("finds files with documentclass", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}
\begin{document}
Hello
\end{document}
`), 0o600)
		os.WriteFile(filepath.Join(dir, "helper.tex"), []byte(`\newcommand{\foo}{bar}
`), 0o600) // no \documentclass

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Equal(t, "paper", docs[0].Name)
		assert.Equal(t, "paper.tex", docs[0].Main)
	})

	t.Run("finds files in nested directories", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "slides"), 0o750)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "slides", "slides.tex"), []byte(`\documentclass{beamer}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 2)

		// Build name->doc mapping for assertions
		docByName := make(map[string]cli.Document)
		for _, doc := range docs {
			docByName[doc.Name] = doc
		}
		assert.Contains(t, docByName, "paper")
		assert.Contains(t, docByName, "slides")
		// Root-level file: Main is basename, Directory is empty
		assert.Equal(t, "paper.tex", docByName["paper"].Main)
		assert.Equal(t, "", docByName["paper"].Directory)
		// Nested file: Main is basename, Directory is parent
		assert.Equal(t, "slides.tex", docByName["slides"].Main)
		assert.Equal(t, "slides", docByName["slides"].Directory)
	})

	t.Run("handles duplicate names with suffix", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "v1"), 0o750)
		os.MkdirAll(filepath.Join(dir, "v2"), 0o750)
		os.WriteFile(filepath.Join(dir, "v1", "main.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "v2", "main.tex"), []byte(`\documentclass{article}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 2)

		names := []string{docs[0].Name, docs[1].Name}
		// One should be "main" and the other "main_2"
		assert.Contains(t, names, "main")
		assert.Contains(t, names, "main_2")

		// Both should have Main as basename
		for _, doc := range docs {
			assert.Equal(t, "main.tex", doc.Main)
			assert.NotEmpty(t, doc.Directory) // both are nested
		}
	})

	t.Run("avoids suffix collision with existing base name", func(t *testing.T) {
		// main.tex, main_2.tex, sub/main.tex should produce:
		//   main.tex     -> "main"   (unique-ish but first of duplicates)
		//   main_2.tex   -> "main_2" (unique stem, reserved in pass 1)
		//   sub/main.tex -> "main_3" (suffixed, skipping reserved "main_2")
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "main.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "main_2.tex"), []byte(`\documentclass{article}`), 0o600)
		os.MkdirAll(filepath.Join(dir, "sub"), 0o750)
		os.WriteFile(filepath.Join(dir, "sub", "main.tex"), []byte(`\documentclass{article}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 3)

		// Build name->doc mapping for exact assertions
		nameToDoc := make(map[string]cli.Document)
		for _, doc := range docs {
			nameToDoc[doc.Name] = doc
		}
		// All names must be unique
		assert.Len(t, nameToDoc, 3, "all document names should be unique, got: %v", nameToDoc)
		// main_2.tex must keep its natural stem "main_2"
		assert.Equal(t, "main_2.tex", nameToDoc["main_2"].Main, "main_2.tex should keep its natural name")
		assert.Equal(t, "", nameToDoc["main_2"].Directory, "main_2.tex is at root")
		// main.tex gets "main" (first of the duplicate "main" stems)
		assert.Equal(t, "main.tex", nameToDoc["main"].Main, "main.tex should get name 'main'")
		assert.Equal(t, "", nameToDoc["main"].Directory, "main.tex is at root")
		// sub/main.tex gets "main_3" (suffixed, skipping reserved "main_2")
		assert.Equal(t, "main.tex", nameToDoc["main_3"].Main, "sub/main.tex Main should be basename")
		assert.Equal(t, "sub", nameToDoc["main_3"].Directory, "sub/main.tex should have Directory='sub'")
	})

	t.Run("avoids cross-duplicate-stem collision", func(t *testing.T) {
		// When both "main" and "main_2" stems are duplicated, suffix generation
		// for "main" duplicates must skip "main_2" (a natural stem), even though
		// "main_2" is itself duplicated and not pre-reserved as unique.
		// a/main.tex, b/main.tex -> stems "main" (x2)
		// c/main_2.tex, d/main_2.tex -> stems "main_2" (x2)
		dir := t.TempDir()
		for _, sub := range []string{"a", "b", "c", "d"} {
			os.MkdirAll(filepath.Join(dir, sub), 0o750)
		}
		os.WriteFile(filepath.Join(dir, "a", "main.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "b", "main.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "c", "main_2.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "d", "main_2.tex"), []byte(`\documentclass{article}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 4)

		nameToDoc := make(map[string]cli.Document)
		for _, doc := range docs {
			nameToDoc[doc.Name] = doc
		}
		assert.Len(t, nameToDoc, 4, "all document names should be unique, got: %v", nameToDoc)

		// a/main.tex -> "main" (first of the "main" duplicates)
		assert.Equal(t, "main.tex", nameToDoc["main"].Main)
		assert.Equal(t, "a", nameToDoc["main"].Directory)
		// b/main.tex -> "main_3" (suffixed, skipping natural stem "main_2")
		assert.Equal(t, "main.tex", nameToDoc["main_3"].Main)
		assert.Equal(t, "b", nameToDoc["main_3"].Directory)
		// c/main_2.tex -> "main_2" (first of the "main_2" duplicates)
		assert.Equal(t, "main_2.tex", nameToDoc["main_2"].Main)
		assert.Equal(t, "c", nameToDoc["main_2"].Directory)
		// d/main_2.tex -> "main_2_2" (suffixed from "main_2" base)
		assert.Equal(t, "main_2.tex", nameToDoc["main_2_2"].Main)
		assert.Equal(t, "d", nameToDoc["main_2_2"].Directory)
	})

	t.Run("respects gitignore", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build/\n"), 0o600)
		os.MkdirAll(filepath.Join(dir, "build"), 0o750)
		os.WriteFile(filepath.Join(dir, "build", "output.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Equal(t, "paper", docs[0].Name)
	})

	t.Run("respects txignore", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".txignore"), []byte("draft.tex\n"), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "draft.tex"), []byte(`\documentclass{article}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Equal(t, "paper", docs[0].Name)
	})

	t.Run("returns empty slice when no tex files", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello"), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		assert.Empty(t, docs)
	})

	t.Run("skips tex files without documentclass", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "macros.tex"), []byte(`\newcommand{\foo}{bar}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		assert.Empty(t, docs)
	})

	t.Run("root-level file has empty directory", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "thesis.tex"), []byte(`\documentclass{report}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Equal(t, "thesis.tex", docs[0].Main)
		assert.Equal(t, "", docs[0].Directory)
	})

	t.Run("nested file has correct directory and short main", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "chapters", "intro"), 0o750)
		os.WriteFile(filepath.Join(dir, "chapters", "intro", "intro.tex"), []byte(`\documentclass{article}`), 0o600)

		docs, err := cli.DiscoverDocuments(dir)
		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Equal(t, "intro.tex", docs[0].Main)
		assert.Equal(t, filepath.Join("chapters", "intro"), docs[0].Directory)
	})
}

func TestInitCmd(t *testing.T) {
	t.Run("auto-discovers tex files in non-TTY mode", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}`), 0o600)
		os.WriteFile(filepath.Join(dir, "helper.tex"), []byte(`\newcommand{\foo}{bar}`), 0o600)

		ui, buf := testUI()
		cmd := &cli.InitCmd{Texlive: "texlive:2021", Compiler: "pdflatex", Main: "main.tex", UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, `texlive: "texlive:2021"`)
		assert.Contains(t, content, `compiler: "pdflatex"`)
		assert.Contains(t, content, "documents:")
		assert.Contains(t, content, `main: "paper.tex"`)
		assert.Contains(t, content, `name: "paper"`)
		assert.NotContains(t, content, "helper.tex")
		assert.NotContains(t, content, "directory:", "root-level files should not emit directory")
		assert.Contains(t, buf.String(), "Created .texops.yaml with 1 document(s)")
	})

	t.Run("discovers multiple tex files", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}`), 0o600)
		os.MkdirAll(filepath.Join(dir, "slides"), 0o750)
		os.WriteFile(filepath.Join(dir, "slides", "slides.tex"), []byte(`\documentclass{beamer}`), 0o600)

		ui, buf := testUI()
		cmd := &cli.InitCmd{Texlive: "texlive:2021", Compiler: "pdflatex", Main: "main.tex", UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, `name: "paper"`)
		assert.Contains(t, content, `main: "paper.tex"`)
		assert.Contains(t, content, `name: "slides"`)
		assert.Contains(t, content, `main: "slides.tex"`)
		assert.Contains(t, content, `directory: "slides"`)
		assert.Contains(t, buf.String(), "2 document(s)")
	})

	t.Run("falls back to --main when no tex files found", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		ui, buf := testUI()
		cmd := &cli.InitCmd{Texlive: "texlive:2021", Compiler: "pdflatex", Main: "main.tex", UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, `texlive: "texlive:2021"`)
		assert.Contains(t, content, `compiler: "pdflatex"`)
		assert.Contains(t, content, "documents:")
		assert.Contains(t, content, `main: "main.tex"`)
		assert.Contains(t, content, `name: "main"`)
		assert.Contains(t, buf.String(), "Created .texops.yaml")
	})

	t.Run("falls back to custom --main when no tex files found", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		ui, buf := testUI()
		cmd := &cli.InitCmd{Texlive: "texlive:2021", Compiler: "pdflatex", Main: "thesis.tex", UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, `main: "thesis.tex"`)
		assert.Contains(t, content, `name: "thesis"`)
		assert.Contains(t, buf.String(), "Created .texops.yaml")
	})

	t.Run("fails if config already exists", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte("existing"), 0o600)

		t.Chdir(dir)

		ui, _ := testUI()
		cmd := &cli.InitCmd{Texlive: "texlive:2021", Compiler: "pdflatex", UI: ui}
		err := cmd.Execute(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("generated config is parseable", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}`), 0o600)
		os.MkdirAll(filepath.Join(dir, "slides"), 0o750)
		os.WriteFile(filepath.Join(dir, "slides", "slides.tex"), []byte(`\documentclass{beamer}`), 0o600)

		ui, _ := testUI()
		cmd := &cli.InitCmd{Texlive: "texlive:2021", Compiler: "pdflatex", Main: "main.tex", UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		// Verify the generated config is valid and parseable
		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		config, err := cli.ParseConfig(string(data))
		require.NoError(t, err)
		assert.Equal(t, "texlive:2021", config.Texlive)
		assert.Equal(t, "pdflatex", config.Compiler)
		assert.Len(t, config.Documents, 2)
		assert.Len(t, config.ProjectKey, 22, "tx init should generate a 22-char project_key")

		// Verify directory round-trips correctly
		docByName := make(map[string]cli.Document)
		for _, doc := range config.Documents {
			docByName[doc.Name] = doc
		}
		assert.Equal(t, "paper.tex", docByName["paper"].Main)
		assert.Equal(t, "", docByName["paper"].Directory)
		assert.Equal(t, "slides.tex", docByName["slides"].Main)
		assert.Equal(t, "slides", docByName["slides"].Directory)
	})

	t.Run("--compiler xelatex writes top-level compiler field", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}`), 0o600)

		ui, _ := testUI()
		cmd := &cli.InitCmd{Texlive: "texlive:2021", Compiler: "xelatex", Main: "main.tex", UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, `compiler: "xelatex"`)

		config, err := cli.ParseConfig(content)
		require.NoError(t, err)
		assert.Equal(t, "xelatex", config.Compiler)
	})

	t.Run("invalid --compiler value returns error", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		ui, _ := testUI()
		cmd := &cli.InitCmd{Texlive: "texlive:2021", Compiler: "badcompiler", Main: "main.tex", UI: ui}
		err := cmd.Execute(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid compiler")
		assert.Contains(t, err.Error(), "badcompiler")
		assert.Contains(t, err.Error(), "pdflatex")

		_, statErr := os.Stat(filepath.Join(dir, ".texops.yaml"))
		assert.True(t, os.IsNotExist(statErr), ".texops.yaml should not be created for invalid compiler")
	})

	t.Run("non-TTY with empty texlive and compiler uses defaults", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		ui, buf := testUI()
		cmd := &cli.InitCmd{Main: "main.tex", UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, `texlive: "texlive:2021"`)
		assert.Contains(t, content, `compiler: "pdflatex"`)
		assert.Contains(t, content, `main: "main.tex"`)
		assert.Contains(t, buf.String(), "Created .texops.yaml")
	})

	t.Run("explicit flags skip interactive selection", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, strings.NewReader(""))
		cmd := &cli.InitCmd{Texlive: "texlive:2023", Compiler: "xelatex", Main: "main.tex", UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, `texlive: "texlive:2023"`)
		assert.Contains(t, content, `compiler: "xelatex"`)

		config, err := cli.ParseConfig(content)
		require.NoError(t, err)
		assert.Equal(t, "texlive:2023", config.Texlive)
		assert.Equal(t, "xelatex", config.Compiler)
	})
}

func TestBuildCmd(t *testing.T) {
	t.Run("runs full build flow with project_key", func(t *testing.T) {
		pdfContent := []byte("%PDF-1.4 test")
		var receivedProjectKey string

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/projects/prj_build/sync" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})

			case r.URL.Path == "/projects/prj_build/build" && r.Method == "POST":
				doneData, _ := json.Marshal(map[string]any{
					"status":   "success",
					"pdfUrl":   "/projects/prj_build/builds/bld_001/output",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: log\ndata: {\"message\":\"Compiling...\"}\n\nevent: done\ndata: " + string(doneData) + "\n\n"))

			case r.URL.Path == "/projects/prj_build/builds/bld_001/output" && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		defer instSrv.Close()

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				receivedProjectKey = body["project_key"]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_build",
					"name":                 body["name"],
					"distribution_version": body["distribution_version"],
				})
			case r.URL.Path == "/api/projects/prj_build/session" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt",
					"cache_cold":   false,
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		mockKeyringForAuth(t, "test-jwt-token")

		t.Setenv("TX_API_URL", apiSrv.URL)

		origNewIC := cli.NewInstanceClientFn
		cli.NewInstanceClientFn = func(instanceURL, jwt string) *cli.InstanceClient {
			ic := cli.NewInstanceClient(instanceURL, jwt)
			ic.SetHTTPClient(instSrv.Client())
			return ic
		}
		defer func() { cli.NewInstanceClientFn = origNewIC }()

		dir := t.TempDir()
		configContent := `project_key: "k7Gx9mR2pL4wN8qY5vBt3a"
texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("\\documentclass{article}\\begin{document}Hello\\end{document}"), 0o600)

		ui, buf := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		require.NoError(t, err)

		written, err := os.ReadFile(filepath.Join(dir, "paper.pdf"))
		require.NoError(t, err)
		assert.Equal(t, pdfContent, written)
		assert.Equal(t, "k7Gx9mR2pL4wN8qY5vBt3a", receivedProjectKey)
		assert.Contains(t, buf.String(), "Project ready")
		assert.Contains(t, buf.String(), "Session acquired")
		assert.Contains(t, buf.String(), "All files up to date")
	})

	t.Run("creates new project via API with project_key", func(t *testing.T) {
		pdfContent := []byte("%PDF-1.4 test")
		var createdProjectID string

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/sync") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})

			case strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST":
				projectID := strings.TrimPrefix(r.URL.Path, "/projects/")
				projectID = strings.TrimSuffix(projectID, "/build")
				doneData, _ := json.Marshal(map[string]any{
					"status":   "success",
					"pdfUrl":   fmt.Sprintf("/projects/%s/builds/bld_001/output", projectID),
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))

			case strings.HasSuffix(r.URL.Path, "/output") && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		defer instSrv.Close()

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				createdProjectID = "prj_auto123"
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   createdProjectID,
					"name":                 body["name"],
					"distribution_version": body["distribution_version"],
				})
			case strings.Contains(r.URL.Path, "/session") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt",
					"cache_cold":   false,
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", apiSrv.URL)

		origNewIC := cli.NewInstanceClientFn
		cli.NewInstanceClientFn = func(instanceURL, jwt string) *cli.InstanceClient {
			ic := cli.NewInstanceClient(instanceURL, jwt)
			ic.SetHTTPClient(instSrv.Client())
			return ic
		}
		defer func() { cli.NewInstanceClientFn = origNewIC }()

		dir := t.TempDir()
		configContent := `project_key: "aBcDeFgHiJkLmNoPqRsT12"
texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("\\documentclass{article}\\begin{document}Hello\\end{document}"), 0o600)

		ui, buf := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		require.NoError(t, err)

		assert.Equal(t, "prj_auto123", createdProjectID)
		assert.Contains(t, buf.String(), "Project ready")
		assert.Contains(t, buf.String(), "Session acquired")
	})

	t.Run("auto-generates project_key when missing", func(t *testing.T) {
		var receivedProjectKey string

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				receivedProjectKey = body["project_key"]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_autogen",
					"name":                 body["name"],
					"distribution_version": body["distribution_version"],
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		dir := t.TempDir()
		configContent := `texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("\\documentclass{article}\\begin{document}Hello\\end{document}"), 0o600)

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", apiSrv.URL)

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		// RunBuild will error after project creation (mock only handles /api/projects),
		// but the project_key generation side effect should have completed.
		require.Error(t, err)

		// Verify project_key was generated and written to config
		updatedConfig, err := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, err)
		assert.Contains(t, string(updatedConfig), "project_key:")

		// Verify the generated key was sent to the API
		assert.Len(t, receivedProjectKey, 22)
	})
}

func TestBuildCmd_AutoInit(t *testing.T) {
	t.Run("non-TTY returns friendly error when config missing", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}\begin{document}Hello\end{document}`), 0o600)

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run `tx init` to set up your project")
	})

	t.Run("TTY prompts and runs init on confirm", func(t *testing.T) {
		dir := t.TempDir()
		// No .tex files with \documentclass — init falls back to main.tex default.
		// outIsTTY=true so Confirm prompt works; stdinIsTTY=false so selectors use defaults.

		buf := &bytes.Buffer{}
		in := strings.NewReader("y\n")
		ui := cli.NewUIWithTTYOptions(buf, true, false, in)

		// Build will init then fail on auth — that's fine, we just check init happened.
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)

		configData, readErr := os.ReadFile(filepath.Join(dir, ".texops.yaml"))
		require.NoError(t, readErr)
		assert.Contains(t, string(configData), "project_key:")
		assert.Contains(t, string(configData), "main.tex")

		// Should fail after init (no auth configured), not on missing config.
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "tx init")
	})

	t.Run("TTY declined returns friendly error", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}\begin{document}Hello\end{document}`), 0o600)

		buf := &bytes.Buffer{}
		in := strings.NewReader("n\n")
		ui := cli.NewUIWithOptions(buf, true, in)

		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run `tx init` to set up your project")

		_, statErr := os.Stat(filepath.Join(dir, ".texops.yaml"))
		assert.True(t, os.IsNotExist(statErr))
	})
}

func TestBuildCmd_NoCache(t *testing.T) {
	t.Run("sends build_options with no_cache when --no-cache is set", func(t *testing.T) {
		var receivedBuildOptions map[string]string
		pdfContent := []byte("%PDF-1.4 test")

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/projects/prj_nc/sync" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})

			case r.URL.Path == "/projects/prj_nc/build" && r.Method == "POST":
				var body struct {
					Main         string            `json:"main"`
					Texlive      string            `json:"distribution_version"`
					BuildOptions map[string]string `json:"build_options"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				receivedBuildOptions = body.BuildOptions

				doneData, _ := json.Marshal(map[string]any{
					"status":   "success",
					"pdfUrl":   "/projects/prj_nc/builds/bld_001/output",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))

			case r.URL.Path == "/projects/prj_nc/builds/bld_001/output" && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		defer instSrv.Close()

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_nc",
					"name":                 "test",
					"distribution_version": "texlive:2021",
				})
			case r.URL.Path == "/api/projects/prj_nc/session" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt",
					"cache_cold":   false,
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		mockKeyringForAuth(t, "test-jwt-token")

		t.Setenv("TX_API_URL", apiSrv.URL)

		origNewIC := cli.NewInstanceClientFn
		cli.NewInstanceClientFn = func(instanceURL, jwt string) *cli.InstanceClient {
			ic := cli.NewInstanceClient(instanceURL, jwt)
			ic.SetHTTPClient(instSrv.Client())
			return ic
		}
		defer func() { cli.NewInstanceClientFn = origNewIC }()

		dir := t.TempDir()
		configContent := `project_key: "k7Gx9mR2pL4wN8qY5vBt3a"
texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("\\documentclass{article}\\begin{document}Hello\\end{document}"), 0o600)

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, true, false, ui)
		require.NoError(t, err)
		require.NotNil(t, receivedBuildOptions, "build_options should be sent in request")
		assert.Equal(t, "true", receivedBuildOptions["no_cache"])
	})

	t.Run("does not send build_options when --no-cache is not set", func(t *testing.T) {
		var receivedBody map[string]any
		pdfContent := []byte("%PDF-1.4 test")

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/projects/prj_nc2/sync" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})

			case r.URL.Path == "/projects/prj_nc2/build" && r.Method == "POST":
				if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
					t.Errorf("failed to decode build request body: %v", err)
				}

				doneData, _ := json.Marshal(map[string]any{
					"status":   "success",
					"pdfUrl":   "/projects/prj_nc2/builds/bld_002/output",
					"build_id": "bld_002",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))

			case r.URL.Path == "/projects/prj_nc2/builds/bld_002/output" && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		defer instSrv.Close()

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_nc2",
					"name":                 "test",
					"distribution_version": "texlive:2021",
				})
			case r.URL.Path == "/api/projects/prj_nc2/session" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt",
					"cache_cold":   false,
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		mockKeyringForAuth(t, "test-jwt-token")

		t.Setenv("TX_API_URL", apiSrv.URL)

		origNewIC := cli.NewInstanceClientFn
		cli.NewInstanceClientFn = func(instanceURL, jwt string) *cli.InstanceClient {
			ic := cli.NewInstanceClient(instanceURL, jwt)
			ic.SetHTTPClient(instSrv.Client())
			return ic
		}
		defer func() { cli.NewInstanceClientFn = origNewIC }()

		dir := t.TempDir()
		configContent := `project_key: "k7Gx9mR2pL4wN8qY5vBt3a"
texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("\\documentclass{article}\\begin{document}Hello\\end{document}"), 0o600)

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		require.NoError(t, err)
		_, hasBuildOptions := receivedBody["build_options"]
		assert.False(t, hasBuildOptions, "build_options should not be sent when --no-cache is not set")
	})
}

func TestBuildCmd_MultiDocument(t *testing.T) {
	// multiDocSetup creates a common build environment for multi-document tests.
	// It returns the temp dir and tracks session/build requests.
	type buildReq struct {
		Main      string
		Directory string
		Version   string
	}
	type setupResult struct {
		dir           string
		sessionCount  *atomic.Int32
		syncCount     *atomic.Int32
		buildRequests *[]buildReq
	}

	multiDocSetup := func(t *testing.T, configContent string, failDoc string) setupResult {
		t.Helper()

		// Prepend project_key to config if not already present
		if !strings.Contains(configContent, "project_key:") {
			configContent = "project_key: \"k7Gx9mR2pL4wN8qY5vBt3a\"\n" + configContent
		}

		pdfContent := []byte("%PDF-1.4 test")
		var sessionCount atomic.Int32
		var syncCount atomic.Int32
		var buildRequests []buildReq
		var mu sync.Mutex

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/sync") && r.Method == "POST":
				syncCount.Add(1)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})

			case strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST":
				var body struct {
					Main      string `json:"main"`
					Directory string `json:"directory"`
					Texlive   string `json:"distribution_version"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				buildRequests = append(buildRequests, buildReq{Main: body.Main, Directory: body.Directory, Version: body.Texlive})
				mu.Unlock()

				projectID := strings.TrimPrefix(r.URL.Path, "/projects/")
				projectID = strings.TrimSuffix(projectID, "/build")

				if failDoc != "" && body.Main == failDoc {
					doneData, _ := json.Marshal(map[string]any{
						"status":  "error",
						"message": "compilation error in " + body.Main,
					})
					w.Header().Set("Content-Type", "text/event-stream")
					w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))
					return
				}

				doneData, _ := json.Marshal(map[string]any{
					"status":   "success",
					"pdfUrl":   fmt.Sprintf("/projects/%s/builds/bld_001/output", projectID),
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))

			case strings.HasSuffix(r.URL.Path, "/output") && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		t.Cleanup(instSrv.Close)

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_multi",
					"name":                 "test",
					"distribution_version": "texlive:2021",
				})
			case strings.Contains(r.URL.Path, "/session") && r.Method == "POST":
				sessionCount.Add(1)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt",
					"cache_cold":   false,
				})
			default:
				w.WriteHeader(404)
			}
		}))
		t.Cleanup(apiSrv.Close)

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", apiSrv.URL)

		origNewIC := cli.NewInstanceClientFn
		cli.NewInstanceClientFn = func(instanceURL, jwt string) *cli.InstanceClient {
			ic := cli.NewInstanceClient(instanceURL, jwt)
			ic.SetHTTPClient(instSrv.Client())
			return ic
		}
		t.Cleanup(func() { cli.NewInstanceClientFn = origNewIC })

		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}\begin{document}Paper\end{document}`), 0o600)
		os.WriteFile(filepath.Join(dir, "slides.tex"), []byte(`\documentclass{beamer}\begin{document}Slides\end{document}`), 0o600)

		return setupResult{
			dir:           dir,
			sessionCount:  &sessionCount,
			syncCount:     &syncCount,
			buildRequests: &buildRequests,
		}
	}

	t.Run("two documents same version share one session and one sync", func(t *testing.T) {
		config := `texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
  - name: slides
    main: slides.tex
`
		s := multiDocSetup(t, config, "")

		ui, buf := testUI()
		err := cli.RunBuild(t.Context(), s.dir, nil, false, false, ui)
		require.NoError(t, err)

		// Should get exactly one session and one sync for same-version docs
		assert.Equal(t, int32(1), s.sessionCount.Load(), "should have one session for same-version docs")
		assert.Equal(t, int32(1), s.syncCount.Load(), "should sync once for same-version docs")

		// Both documents should have been built
		assert.Len(t, *s.buildRequests, 2)
		assert.Equal(t, "paper.tex", (*s.buildRequests)[0].Main)
		assert.Equal(t, "slides.tex", (*s.buildRequests)[1].Main)

		// Both PDFs should exist
		assert.FileExists(t, filepath.Join(s.dir, "paper.pdf"))
		assert.FileExists(t, filepath.Join(s.dir, "slides.pdf"))

		// Summary should be printed
		output := buf.String()
		assert.Contains(t, output, "2 succeeded, 0 failed")
		assert.Contains(t, output, "paper => paper.pdf")
		assert.Contains(t, output, "slides => slides.pdf")
	})

	t.Run("two documents different versions get separate sessions", func(t *testing.T) {
		config := `texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
  - name: slides
    main: slides.tex
    texlive: "texlive:2019"
`
		s := multiDocSetup(t, config, "")

		ui, buf := testUI()
		err := cli.RunBuild(t.Context(), s.dir, nil, false, false, ui)
		require.NoError(t, err)

		// Should get two sessions (one per version) and two syncs
		assert.Equal(t, int32(2), s.sessionCount.Load(), "should have two sessions for different-version docs")
		assert.Equal(t, int32(2), s.syncCount.Load(), "should sync twice for different-version docs")

		// Both documents should have been built
		assert.Len(t, *s.buildRequests, 2)

		output := buf.String()
		assert.Contains(t, output, "2 succeeded, 0 failed")
	})

	t.Run("named document filter builds only specified document", func(t *testing.T) {
		config := `texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
  - name: slides
    main: slides.tex
`
		s := multiDocSetup(t, config, "")

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), s.dir, []string{"paper"}, false, false, ui)
		require.NoError(t, err)

		// Only one document should be built
		assert.Len(t, *s.buildRequests, 1)
		assert.Equal(t, "paper.tex", (*s.buildRequests)[0].Main)
		assert.FileExists(t, filepath.Join(s.dir, "paper.pdf"))
	})

	t.Run("unknown document name returns error", func(t *testing.T) {
		config := `texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(config), 0o600)

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), dir, []string{"nonexistent"}, false, false, ui)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown document")
		assert.Contains(t, err.Error(), "nonexistent")
		assert.Contains(t, err.Error(), "paper")
	})

	t.Run("partial failure does not stop other documents", func(t *testing.T) {
		config := `texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
  - name: slides
    main: slides.tex
`
		s := multiDocSetup(t, config, "slides.tex") // slides will fail

		ui, buf := testUI()
		err := cli.RunBuild(t.Context(), s.dir, nil, false, false, ui)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "one or more documents failed to build")

		// Both documents should have been attempted
		assert.Len(t, *s.buildRequests, 2)

		// paper.pdf should exist, slides.pdf should not
		assert.FileExists(t, filepath.Join(s.dir, "paper.pdf"))
		assert.NoFileExists(t, filepath.Join(s.dir, "slides.pdf"))

		output := buf.String()
		assert.Contains(t, output, "1 succeeded, 1 failed")
		assert.Contains(t, output, "paper => paper.pdf")
		assert.Contains(t, output, "slides !! FAILED")
	})

	t.Run("single document prints summary", func(t *testing.T) {
		config := `texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		s := multiDocSetup(t, config, "")

		ui, buf := testUI()
		err := cli.RunBuild(t.Context(), s.dir, nil, false, false, ui)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "1 succeeded, 0 failed")
		assert.Contains(t, output, "paper => paper.pdf")
	})

	t.Run("document with directory passes directory in build request", func(t *testing.T) {
		config := `texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    directory: chapters/paper
  - name: slides
    main: slides.tex
`
		s := multiDocSetup(t, config, "")
		// Create subdirectory file so the project has the right structure
		os.MkdirAll(filepath.Join(s.dir, "chapters", "paper"), 0o750)
		os.WriteFile(filepath.Join(s.dir, "chapters", "paper", "paper.tex"), []byte(`\documentclass{article}\begin{document}Paper\end{document}`), 0o600)

		ui, buf := testUI()
		err := cli.RunBuild(t.Context(), s.dir, nil, false, false, ui)
		require.NoError(t, err)

		assert.Len(t, *s.buildRequests, 2)
		// Paper should have directory set
		assert.Equal(t, "paper.tex", (*s.buildRequests)[0].Main)
		assert.Equal(t, "chapters/paper", (*s.buildRequests)[0].Directory)
		// Slides should have empty directory
		assert.Equal(t, "slides.tex", (*s.buildRequests)[1].Main)
		assert.Equal(t, "", (*s.buildRequests)[1].Directory)

		output := buf.String()
		assert.Contains(t, output, "2 succeeded, 0 failed")
		// Status message should show full path for document with directory
		assert.Contains(t, output, "chapters/paper/paper.tex")
	})
}

func TestBuildCmd_Compiler(t *testing.T) {
	t.Run("passes compiler from config to build request", func(t *testing.T) {
		var receivedCompiler string
		pdfContent := []byte("%PDF-1.4 test")

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/sync") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})

			case strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST":
				var body struct {
					Main     string `json:"main"`
					Compiler string `json:"compiler"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				receivedCompiler = body.Compiler

				doneData, _ := json.Marshal(map[string]any{
					"status":   "success",
					"pdfUrl":   "/projects/prj_comp/builds/bld_001/output",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))

			case strings.HasSuffix(r.URL.Path, "/output") && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		defer instSrv.Close()

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_comp",
					"name":                 "test",
					"distribution_version": "texlive:2021",
				})
			case strings.Contains(r.URL.Path, "/session") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt",
					"cache_cold":   false,
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", apiSrv.URL)

		origNewIC := cli.NewInstanceClientFn
		cli.NewInstanceClientFn = func(instanceURL, jwt string) *cli.InstanceClient {
			ic := cli.NewInstanceClient(instanceURL, jwt)
			ic.SetHTTPClient(instSrv.Client())
			return ic
		}
		defer func() { cli.NewInstanceClientFn = origNewIC }()

		dir := t.TempDir()
		configContent := `project_key: "k7Gx9mR2pL4wN8qY5vBt3a"
texlive: "texlive:2021"
compiler: xelatex
documents:
  - name: paper
    main: paper.tex
`
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}\begin{document}Hello\end{document}`), 0o600)

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		require.NoError(t, err)

		assert.Equal(t, "xelatex", receivedCompiler, "compiler from config should be sent in build request")
	})

	t.Run("per-document compiler overrides top-level default", func(t *testing.T) {
		var receivedCompilers []string
		var mu sync.Mutex
		pdfContent := []byte("%PDF-1.4 test")

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/sync") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})

			case strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST":
				var body struct {
					Main     string `json:"main"`
					Compiler string `json:"compiler"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				receivedCompilers = append(receivedCompilers, body.Compiler)
				mu.Unlock()

				doneData, _ := json.Marshal(map[string]any{
					"status":   "success",
					"pdfUrl":   "/projects/prj_comp2/builds/bld_001/output",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))

			case strings.HasSuffix(r.URL.Path, "/output") && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		defer instSrv.Close()

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_comp2",
					"name":                 "test",
					"distribution_version": "texlive:2021",
				})
			case strings.Contains(r.URL.Path, "/session") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt",
					"cache_cold":   false,
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", apiSrv.URL)

		origNewIC := cli.NewInstanceClientFn
		cli.NewInstanceClientFn = func(instanceURL, jwt string) *cli.InstanceClient {
			ic := cli.NewInstanceClient(instanceURL, jwt)
			ic.SetHTTPClient(instSrv.Client())
			return ic
		}
		defer func() { cli.NewInstanceClientFn = origNewIC }()

		dir := t.TempDir()
		configContent := `project_key: "k7Gx9mR2pL4wN8qY5vBt3a"
texlive: "texlive:2021"
compiler: xelatex
documents:
  - name: paper
    main: paper.tex
  - name: slides
    main: slides.tex
    compiler: lualatex
`
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}\begin{document}Paper\end{document}`), 0o600)
		os.WriteFile(filepath.Join(dir, "slides.tex"), []byte(`\documentclass{beamer}\begin{document}Slides\end{document}`), 0o600)

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		require.NoError(t, err)

		require.Len(t, receivedCompilers, 2)
		assert.Equal(t, "xelatex", receivedCompilers[0], "paper should use top-level compiler")
		assert.Equal(t, "lualatex", receivedCompilers[1], "slides should use per-document compiler override")
	})

	t.Run("omitted compiler defaults to pdflatex and is sent in request", func(t *testing.T) {
		var receivedCompiler string
		pdfContent := []byte("%PDF-1.4 test")

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/sync") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"missing": []string{}})

			case strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST":
				var body struct {
					Compiler string `json:"compiler"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				receivedCompiler = body.Compiler

				doneData, _ := json.Marshal(map[string]any{
					"status":   "success",
					"pdfUrl":   "/projects/prj_comp3/builds/bld_001/output",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))

			case strings.HasSuffix(r.URL.Path, "/output") && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		defer instSrv.Close()

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_comp3",
					"name":                 "test",
					"distribution_version": "texlive:2021",
				})
			case strings.Contains(r.URL.Path, "/session") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt",
					"cache_cold":   false,
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", apiSrv.URL)

		origNewIC := cli.NewInstanceClientFn
		cli.NewInstanceClientFn = func(instanceURL, jwt string) *cli.InstanceClient {
			ic := cli.NewInstanceClient(instanceURL, jwt)
			ic.SetHTTPClient(instSrv.Client())
			return ic
		}
		defer func() { cli.NewInstanceClientFn = origNewIC }()

		dir := t.TempDir()
		configContent := `project_key: "k7Gx9mR2pL4wN8qY5vBt3a"
texlive: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(configContent), 0o600)
		os.WriteFile(filepath.Join(dir, "paper.tex"), []byte(`\documentclass{article}\begin{document}Hello\end{document}`), 0o600)

		ui, _ := testUI()
		err := cli.RunBuild(t.Context(), dir, nil, false, false, ui)
		require.NoError(t, err)

		assert.Equal(t, "pdflatex", receivedCompiler, "default compiler should be pdflatex")
	})
}

func TestStatusCmd(t *testing.T) {
	t.Run("authenticated with JWT shows email method and expiry", func(t *testing.T) {
		mockKeyringForAuth(t, "test-jwt-token")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/whoami" && r.Method == "GET" {
				assert.Equal(t, "Bearer test-jwt-token", r.Header.Get("Authorization"))
				expires := "2026-03-30T12:00:00Z"
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(cli.WhoamiResponse{
					UserID:     "usr_01ABC",
					Email:      "user@example.com",
					AuthMethod: "jwt",
					ExpiresAt:  &expires,
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.StatusCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "Authenticated")
		assert.Contains(t, output, "Email:   user@example.com")
		assert.Contains(t, output, "Method:  jwt")
		assert.Contains(t, output, "Expires: 30 Mar 2026")
	})

	t.Run("authenticated with API token shows no expiry", func(t *testing.T) {
		mockKeyringForAuth(t, "test-api-token")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/whoami" && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(cli.WhoamiResponse{
					UserID:     "usr_01ABC",
					Email:      "user@example.com",
					AuthMethod: "api_token",
					ExpiresAt:  nil,
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.StatusCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "Authenticated")
		assert.Contains(t, output, "Email:   user@example.com")
		assert.Contains(t, output, "Method:  API token")
		assert.Contains(t, output, "Expires: never")
	})

	t.Run("authenticated with API token shows expiry", func(t *testing.T) {
		mockKeyringForAuth(t, "test-api-token")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/whoami" && r.Method == "GET" {
				expires := "2026-06-15T00:00:00Z"
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(cli.WhoamiResponse{
					UserID:     "usr_01ABC",
					Email:      "user@example.com",
					AuthMethod: "api_token",
					ExpiresAt:  &expires,
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.StatusCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "Authenticated")
		assert.Contains(t, output, "Method:  API token")
		assert.Contains(t, output, "Expires: 15 Jun 2026")
	})

	t.Run("not authenticated shows guidance", func(t *testing.T) {
		origGet := cli.KeyringGet
		cli.KeyringGet = func(service, user string) (string, error) {
			return "", fmt.Errorf("not found")
		}
		t.Cleanup(func() { cli.KeyringGet = origGet })

		// Also ensure no env var or file fallback
		t.Setenv("TX_API_TOKEN", "")

		origPath := cli.CredentialsFilePath
		cli.CredentialsFilePath = func() string { return "/nonexistent/path/creds.yaml" }
		t.Cleanup(func() { cli.CredentialsFilePath = origPath })

		ui, buf := testUI()
		cmd := &cli.StatusCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "Not authenticated.")
		assert.Contains(t, output, "tx login")
	})

	t.Run("API returns 401 suggests re-login", func(t *testing.T) {
		mockKeyringForAuth(t, "expired-token")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("unauthorized"))
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.StatusCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "Session expired")
		assert.Contains(t, output, "tx login")
	})

	t.Run("API returns 500 propagates error", func(t *testing.T) {
		mockKeyringForAuth(t, "test-jwt-token")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal server error"))
		}))
		defer srv.Close()

		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.StatusCmd{UI: ui}
		err := cmd.Execute(nil)
		require.Error(t, err)

		output := buf.String()
		assert.Contains(t, output, "Error:")
	})
}

func TestFormatDate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"valid RFC3339", "2026-03-30T12:00:00Z", "30 Mar 2026"},
		{"valid with timezone offset", "2026-06-15T10:30:00+02:00", "15 Jun 2026"},
		{"invalid string returned as-is", "not-a-date", "not-a-date"},
		{"empty string returned as-is", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cli.FormatDate(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFormatDatePtr(t *testing.T) {
	t.Run("nil returns fallback", func(t *testing.T) {
		got := cli.FormatDatePtr(nil, "never")
		assert.Equal(t, "never", got)
	})
	t.Run("non-nil formats date", func(t *testing.T) {
		s := "2026-06-15T00:00:00Z"
		got := cli.FormatDatePtr(&s, "never")
		assert.Equal(t, "15 Jun 2026", got)
	})
	t.Run("non-nil invalid returns raw string", func(t *testing.T) {
		s := "bad-date"
		got := cli.FormatDatePtr(&s, "never")
		assert.Equal(t, "bad-date", got)
	})
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"30d", 30 * 86400, false},
		{"90d", 90 * 86400, false},
		{"1y", 365 * 86400, false},
		{"365d", 365 * 86400, false},
		{"", 0, true},
		{"abc", 0, true},
		{"0d", 0, true},
		{"-1d", 0, true},
		{"30x", 0, true},
		{"30", 0, true},
		{"3651d", 0, true},             // exceeds 10-year max
		{"11y", 0, true},               // exceeds 10-year max
		{"3650d", 3650 * 86400, false}, // exactly 10 years
		{"10y", 10 * 365 * 86400, false},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := cli.ParseDuration(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestTokenCreateCmd(t *testing.T) {
	t.Run("create with expires-in flag", func(t *testing.T) {
		var receivedName string
		var receivedExpiresIn *int64

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "POST" {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				receivedName = body["name"].(string)
				if v, ok := body["expires_in"].(float64); ok {
					i := int64(v)
					receivedExpiresIn = &i
				}
				expires := "2026-03-30T00:00:00Z"
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]any{
					"token":      "texops_token_abcdef1234567890abcdef1234567890abc",
					"id":         "tok_01ABC",
					"name":       receivedName,
					"prefix":     "texops_token_abcd",
					"expires_at": expires,
					"created_at": "2026-02-28T00:00:00Z",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.TokenCreateCmd{
			Name:      "CI prod",
			ExpiresIn: "30d",
			UI:        ui,
		}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		assert.Equal(t, "CI prod", receivedName)
		require.NotNil(t, receivedExpiresIn)
		assert.Equal(t, int64(30*86400), *receivedExpiresIn)

		output := buf.String()
		assert.Contains(t, output, "texops_token_abcdef1234567890abcdef1234567890abc")
		assert.Contains(t, output, "This token won't be shown again")
		assert.Contains(t, output, "Expires: 30 Mar 2026")
	})

	t.Run("create with no-expiry flag", func(t *testing.T) {
		var receivedBody map[string]any

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "POST" {
				json.NewDecoder(r.Body).Decode(&receivedBody)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]any{
					"token":      "texops_token_noexpiry123456789012345678901234",
					"id":         "tok_02DEF",
					"name":       "permanent",
					"prefix":     "texops_token_noex",
					"created_at": "2026-02-28T00:00:00Z",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.TokenCreateCmd{
			Name:     "permanent",
			NoExpiry: true,
			UI:       ui,
		}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		// Should NOT have expires_in in the request body
		_, hasExpiresIn := receivedBody["expires_in"]
		assert.False(t, hasExpiresIn)

		output := buf.String()
		assert.Contains(t, output, "texops_token_noexpiry123456789012345678901234")
		assert.Contains(t, output, "Expires: never")
	})

	t.Run("create duplicate name returns conflict error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "POST" {
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte(`{"error":"token name already exists"}`))
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.TokenCreateCmd{
			Name:      "duplicate",
			ExpiresIn: "30d",
			UI:        ui,
		}
		err := cmd.Execute(nil)
		require.Error(t, err)
		assert.Contains(t, buf.String(), "already exists")
	})

	t.Run("create with interactive expiry selection", func(t *testing.T) {
		var receivedExpiresIn *int64

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "POST" {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if v, ok := body["expires_in"].(float64); ok {
					i := int64(v)
					receivedExpiresIn = &i
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]any{
					"token":      "texops_token_interactive12345678901234567890",
					"id":         "tok_03GHI",
					"name":       "interactive",
					"prefix":     "texops_token_inte",
					"expires_at": "2026-05-29T00:00:00Z",
					"created_at": "2026-02-28T00:00:00Z",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		// Simulate selecting option 2 (90 days) via Bubble Tea:
		// one down-arrow (\x1b[B) + enter (\r)
		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, strings.NewReader("\x1b[B\r"))
		cmd := &cli.TokenCreateCmd{
			Name: "interactive",
			UI:   ui,
		}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		require.NotNil(t, receivedExpiresIn)
		assert.Equal(t, int64(90*86400), *receivedExpiresIn)
	})

	t.Run("non-TTY without name flag returns error", func(t *testing.T) {
		ui, _ := testUI() // testUI creates non-TTY UI
		cmd := &cli.TokenCreateCmd{
			ExpiresIn: "30d",
			UI:        ui,
		}
		err := cmd.Execute(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "specify --name in non-interactive mode")
	})

	t.Run("interactive name prompt via TTY", func(t *testing.T) {
		var receivedName string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "POST" {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				receivedName = body["name"].(string)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]any{
					"token":      "texops_token_interactive12345678901234567890",
					"id":         "tok_04JKL",
					"name":       receivedName,
					"prefix":     "texops_token_inte",
					"expires_at": "2026-05-29T00:00:00Z",
					"created_at": "2026-02-28T00:00:00Z",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		// Simulate typing "my-ci-token" + enter for name prompt;
		// use --expires-in flag to avoid a second interactive prompt
		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, strings.NewReader("my-ci-token\r"))
		cmd := &cli.TokenCreateCmd{
			ExpiresIn: "30d",
			UI:        ui,
		}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		assert.Equal(t, "my-ci-token", receivedName)
	})
}

func TestTokenListCmd(t *testing.T) {
	t.Run("list multiple tokens", func(t *testing.T) {
		expires := "2026-05-30T00:00:00Z"
		lastUsed := "2026-02-27T12:00:00Z"

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]map[string]any{
					{
						"id":           "tok_01ABC",
						"name":         "CI prod",
						"prefix":       "texops_token_abcd",
						"expires_at":   expires,
						"last_used_at": lastUsed,
						"created_at":   "2026-02-01T00:00:00Z",
					},
					{
						"id":         "tok_02DEF",
						"name":       "local dev",
						"prefix":     "texops_token_efgh",
						"created_at": "2026-02-15T00:00:00Z",
					},
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.TokenListCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "2 token(s)")
		assert.Contains(t, output, "NAME")
		assert.Contains(t, output, "PREFIX")
		assert.Contains(t, output, "CI prod")
		assert.Contains(t, output, "texops_token_abcd")
		assert.Contains(t, output, "30 May 2026")
		assert.Contains(t, output, "local dev")
		assert.Contains(t, output, "texops_token_efgh")
		assert.Contains(t, output, "never")
	})

	t.Run("list empty tokens", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		ui, buf := testUI()
		cmd := &cli.TokenListCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "No tokens found")
		assert.Contains(t, output, "tx token create")
	})
}

func TestTokenDeleteCmd(t *testing.T) {
	t.Run("delete by name argument", func(t *testing.T) {
		var deletedID string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/auth/tokens" && r.Method == "GET":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]map[string]any{
					{
						"id":         "tok_01ABC",
						"name":       "CI prod",
						"prefix":     "texops_token_abcd",
						"created_at": "2026-02-01T00:00:00Z",
					},
					{
						"id":         "tok_02DEF",
						"name":       "local dev",
						"prefix":     "texops_token_efgh",
						"created_at": "2026-02-15T00:00:00Z",
					},
				})
			case strings.HasPrefix(r.URL.Path, "/auth/tokens/") && r.Method == "DELETE":
				deletedID = strings.TrimPrefix(r.URL.Path, "/auth/tokens/")
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(404)
			}
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		// Non-TTY auto-confirms
		ui, buf := testUI()
		cmd := &cli.TokenDeleteCmd{UI: ui}
		err := cmd.Execute([]string{"CI prod"})
		require.NoError(t, err)

		assert.Equal(t, "tok_01ABC", deletedID)
		assert.Contains(t, buf.String(), "Token deleted")
	})

	t.Run("delete token not found by name", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]map[string]any{
					{
						"id":         "tok_01ABC",
						"name":       "CI prod",
						"prefix":     "texops_token_abcd",
						"created_at": "2026-02-01T00:00:00Z",
					},
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		ui, _ := testUI()
		cmd := &cli.TokenDeleteCmd{UI: ui}
		err := cmd.Execute([]string{"nonexistent"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("delete with user cancellation", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]map[string]any{
					{
						"id":         "tok_01ABC",
						"name":       "CI prod",
						"prefix":     "texops_token_abcd",
						"created_at": "2026-02-01T00:00:00Z",
					},
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, strings.NewReader("n\n"))
		cmd := &cli.TokenDeleteCmd{UI: ui}
		err := cmd.Execute([]string{"CI prod"})
		require.NoError(t, err)

		assert.Contains(t, buf.String(), "Cancelled")
	})

	t.Run("delete empty list shows message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/auth/tokens" && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		mockKeyringForAuth(t, "test-jwt-token")
		t.Setenv("TX_API_URL", srv.URL)

		// Interactive mode (no name arg) with empty list
		buf := &bytes.Buffer{}
		ui := cli.NewUIWithOptions(buf, true, strings.NewReader(""))
		cmd := &cli.TokenDeleteCmd{UI: ui}
		err := cmd.Execute(nil)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "No tokens to delete")
	})
}
