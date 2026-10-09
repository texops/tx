package cli_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

const summaryLine = `^Build complete: \d+ succeeded, \d+ failed \(\d+\.\ds\)\n`

func latexErrorDone() map[string]any {
	return map[string]any{
		"status":   "error",
		"reason":   "latex_error",
		"message":  "1 LaTeX error in sec/intro.tex",
		"build_id": "bld_1",
		"log_url":  "/projects/prj_test/builds/bld_1/log",
		"diagnostics": []map[string]any{
			{"severity": "error", "file": "sec/intro.tex", "line": 3, "message": "Undefined control sequence.", "context": "\\badmacro"},
			{"severity": "warning", "kind": "undefined_citation", "file": "sec/intro.tex", "line": 1, "message": "Citation `missing' undefined"},
			{"severity": "warning", "kind": "rerun", "message": "Label(s) may have changed. Rerun to get cross-references right."},
		},
		"truncated": false,
	}
}

func successWithWarningDone() map[string]any {
	return map[string]any{
		"status":   "success",
		"pdfUrl":   "/projects/prj_test/builds/bld_ok/output",
		"build_id": "bld_ok",
		"log_url":  "/projects/prj_test/builds/bld_ok/log",
		"diagnostics": []map[string]any{
			{"severity": "warning", "kind": "undefined_reference", "file": "paper.tex", "line": 7, "message": "Reference `fig:x' undefined"},
		},
		"truncated": false,
	}
}

func numberedLog(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "log line %d\n", i)
	}
	return b.String()
}

func successDone(buildID string) map[string]any {
	return map[string]any{
		"status":   "success",
		"pdfUrl":   "/projects/prj_test/builds/" + buildID + "/output",
		"build_id": buildID,
		"log_url":  "/projects/prj_test/builds/" + buildID + "/log",
	}
}

func twoDocConfig(paper, slides string) string {
	return fmt.Sprintf("project_key: \"k7Gx9mR2pL4wN8qY5vBt3a\"\ntexlive: \"2025\"\ndocuments:\n  - name: %q\n    main: paper.tex\n  - name: %q\n    main: slides.tex\n", paper, slides)
}

var logPathPattern = regexp.MustCompile(`\(log: (\.texops/logs/[^)]+)\)`)

func summaryLogPaths(t *testing.T, r txRun) []string {
	t.Helper()
	matches := logPathPattern.FindAllStringSubmatch(r.stdout, -1)
	paths := make([]string, 0, len(matches))
	for _, m := range matches {
		paths = append(paths, m[1])
	}
	require.Len(t, paths, 2, r)
	return paths
}

func readFile(t *testing.T, path string, r txRun) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, r)
	return string(data)
}

func TestBuildDiagnostics(t *testing.T) {
	t.Run("failed build prints a diagnostics block", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Regexp(t, summaryLine+regexp.QuoteMeta(
			"      paper: FAILED, 1 error, 2 warnings\n"+
				"        sec/intro.tex:3: error: Undefined control sequence.\n"+
				"        sec/intro.tex:1: warning: Citation `missing' undefined\n"+
				"        warning: Label(s) may have changed. Rerun to get cross-references right.\n")+`$`,
			r.stdout, r)
		assert.Contains(t, r.stderr, "Build failed: 1 LaTeX error in sec/intro.tex\n", r)
	})

	t.Run("successful build with warnings shows them", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = successWithWarningDone()
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Regexp(t, summaryLine+regexp.QuoteMeta(
			"      paper => paper.pdf, 0 errors, 1 warning\n"+
				"        paper.tex:7: warning: Reference `fig:x' undefined\n")+`$`,
			r.stdout, r)
	})

	t.Run("truncated diagnostics are noted", func(t *testing.T) {
		f := newFakeTexOps(t)
		done := latexErrorDone()
		done["truncated"] = true
		f.done["paper.tex"] = done
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.True(t, strings.HasSuffix(r.stdout, "        (more diagnostics were found; see the log)\n"), r)
	})

	t.Run("diagnostic without a line keeps the file", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = map[string]any{
			"status":   "error",
			"reason":   "latex_error",
			"build_id": "bld_1",
			"diagnostics": []map[string]any{
				{"severity": "error", "file": "paper.tex", "message": "Emergency stop.\nl.12 \\end"},
			},
		}
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stdout, "      paper: FAILED, 1 error, 0 warnings\n        paper.tex: error: Emergency stop. l.12 \\end\n", r)
	})

	t.Run("diagnostic without a file renders severity and message", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = map[string]any{
			"status":   "error",
			"reason":   "latex_error",
			"build_id": "bld_1",
			"diagnostics": []map[string]any{
				{"severity": "error", "line": 12, "message": "Missing $ inserted."},
			},
		}
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stdout, "      paper: FAILED, 1 error, 0 warnings\n        error: Missing $ inserted.\n", r)
	})

	t.Run("service failure without diagnostics shows the reason", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("timeout", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitFailure, r.code, r)
		assert.Regexp(t, summaryLine+regexp.QuoteMeta("      paper: FAILED (timeout)\n")+`$`, r.stdout, r)
	})

	t.Run("old server without diagnostics still reports the failure", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("", "bld_1")
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Regexp(t, summaryLine+regexp.QuoteMeta("      paper: FAILED (latex_error)\n")+`$`, r.stdout, r)
		assert.Contains(t, r.stderr, "Last 1 line of build output:\n    latexmk output for paper.tex\nBuild failed: build failed with \n", r)
		assert.NoDirExists(t, ".texops", r)
	})
}

func TestBuildLogMode(t *testing.T) {
	t.Run("stdout mode streams the log and writes no file", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(3)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stderr, "    latexmk output for paper.tex\n", r)
		assert.NoDirExists(t, ".texops", r)
		assert.NotContains(t, r.requests, "GET /projects/prj_test/builds/bld_1/log", r)
		assert.NotContains(t, r.stdout, "(log:", r)
	})

	t.Run("file mode saves the log and .texops/.gitignore and streams nothing", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = successWithWarningDone()
		f.logs["bld_ok"] = numberedLog(3)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log=file")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t, numberedLog(3), readFile(t, filepath.Join(".texops", "logs", "paper.log"), r), r)
		assert.Equal(t, "*\n", readFile(t, filepath.Join(".texops", ".gitignore"), r), r)
		assert.NotContains(t, r.stderr, "latexmk output", r)
		assert.NotContains(t, r.stderr, "log line", r)
		assert.Contains(t, r.stdout, "      paper => paper.pdf, 0 errors, 1 warning (log: .texops/logs/paper.log)\n", r)
	})

	t.Run("file mode prints the last 20 lines of the log on failure", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(25)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		var want strings.Builder
		want.WriteString("Last 20 lines of .texops/logs/paper.log:\n")
		for i := 6; i <= 25; i++ {
			fmt.Fprintf(&want, "    log line %d\n", i)
		}
		want.WriteString("Build failed: 1 LaTeX error in sec/intro.tex\n")
		assert.Contains(t, r.stderr, want.String(), r)
		assert.NotContains(t, r.stderr, "log line 5\n", r)
		assert.Contains(t, r.stdout, "      paper: FAILED, 1 error, 2 warnings (log: .texops/logs/paper.log)\n", r)
	})

	t.Run("file mode overwrites the log and keeps an existing .texops/.gitignore", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(2)
		dir := projectDir(t, projectConfig)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".texops", "logs"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops", ".gitignore"), []byte("logs/\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops", "logs", "paper.log"), []byte(numberedLog(40)), 0o600))

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Equal(t, numberedLog(2), readFile(t, filepath.Join(dir, ".texops", "logs", "paper.log"), r), r)
		assert.Equal(t, "logs/\n", readFile(t, filepath.Join(dir, ".texops", ".gitignore"), r), r)
	})

	t.Run("file mode keeps the build result when the log is gone", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stderr, "Could not save the build log: log download failed (410): build not found or expired\n", r)
		assert.Contains(t, r.stderr, "Last 1 line of build output:\n    latexmk output for paper.tex\n", r)
		assert.NoFileExists(t, filepath.Join(".texops", "logs", "paper.log"), r)
		assert.Contains(t, r.stdout, "      paper: FAILED, 1 error, 2 warnings\n", r)
	})

	t.Run("file mode does not request a log URL outside the build", func(t *testing.T) {
		f := newFakeTexOps(t)
		done := latexErrorDone()
		done["log_url"] = "https://example.com/projects/prj_test/builds/bld_1/log"
		f.done["paper.tex"] = done
		f.logs["bld_1"] = numberedLog(2)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stderr, `Could not save the build log: invalid log URL "https://example.com/projects/prj_test/builds/bld_1/log"`, r)
		assert.NotContains(t, r.requests, "GET /projects/prj_test/builds/bld_1/log", r)
	})

	t.Run("file mode names the log after a sanitized document name", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.logs["bld_ok"] = numberedLog(1)
		f.done["paper.tex"] = map[string]any{"status": "success", "pdfUrl": "/projects/prj_test/builds/bld_ok/output", "build_id": "bld_ok", "log_url": "/projects/prj_test/builds/bld_ok/log"}
		projectDir(t, "project_key: \"k7Gx9mR2pL4wN8qY5vBt3a\"\ntexlive: \"2025\"\ndocuments:\n  - name: \"../my paper\"\n    main: paper.tex\n")

		r := runTx(t, f, "build", "--log", "file")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t, numberedLog(1), readFile(t, filepath.Join(".texops", "logs", ".._my_paper.log"), r), r)
		assert.Contains(t, r.stdout, "(log: .texops/logs/.._my_paper.log)\n", r)
	})

	t.Run("file mode removes the previous log when the new one is gone", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		dir := projectDir(t, projectConfig)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".texops", "logs"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops", "logs", "paper.log"), []byte(numberedLog(5)), 0o600))

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.requests, "GET /projects/prj_test/builds/bld_1/log", r)
		assert.NoFileExists(t, filepath.Join(dir, ".texops", "logs", "paper.log"), r)
		assert.NotContains(t, r.stdout, "(log:", r)
	})

	t.Run("file mode removes the previous log when the server sends no log URL", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = failedBuild("latex_error", "bld_1")
		dir := projectDir(t, projectConfig)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".texops", "logs"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops", "logs", "paper.log"), []byte(numberedLog(5)), 0o600))

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.NoFileExists(t, filepath.Join(dir, ".texops", "logs", "paper.log"), r)
		assert.NotContains(t, r.stderr, "log line", r)
	})

	t.Run("file mode prints the last 20 whole lines of a log larger than 64 KiB", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(10000)
		require.Greater(t, len(f.logs["bld_1"]), 64*1024)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		var want strings.Builder
		want.WriteString("Last 20 lines of .texops/logs/paper.log:\n")
		for i := 9981; i <= 10000; i++ {
			fmt.Fprintf(&want, "    log line %d\n", i)
		}
		want.WriteString("Build failed: ")
		assert.Contains(t, r.stderr, want.String(), r)
	})

	t.Run("file mode falls back to the streamed output when the log is empty", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = ""
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stderr, "Last 1 line of build output:\n    latexmk output for paper.tex\n", r)
		assert.Contains(t, r.stdout, "(log: .texops/logs/paper.log)\n", r)
	})

	t.Run("file mode still shows that the build is queued", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.queued = true
		f.done["paper.tex"] = successDone("bld_ok")
		f.logs["bld_ok"] = numberedLog(1)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Contains(t, r.stderr, "build queued, waiting for previous build to finish\n", r)
		assert.NotContains(t, r.stderr, "latexmk output", r)
	})

	t.Run("file mode names the log of a document made of dots document.log", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = successDone("bld_ok")
		f.logs["bld_ok"] = numberedLog(1)
		projectDir(t, "project_key: \"k7Gx9mR2pL4wN8qY5vBt3a\"\ntexlive: \"2025\"\ndocuments:\n  - name: \"..\"\n    main: paper.tex\n")

		r := runTx(t, f, "build", "--log", "file")

		require.Equal(t, cli.ExitOK, r.code, r)
		assert.Equal(t, numberedLog(1), readFile(t, filepath.Join(".texops", "logs", "document.log"), r), r)
		assert.Contains(t, r.stdout, "(log: .texops/logs/document.log)\n", r)
	})

	t.Run("file mode keeps separate logs for names that clash after sanitizing", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = successDone("bld_1")
		f.done["slides.tex"] = successDone("bld_2")
		f.logs["bld_1"] = "first document\n"
		f.logs["bld_2"] = "second document\n"
		projectDir(t, twoDocConfig("my paper", "my_paper"))

		r := runTx(t, f, "build", "--log", "file")

		require.Equal(t, cli.ExitOK, r.code, r)
		paths := summaryLogPaths(t, r)
		assert.Regexp(t, `^\.texops/logs/my_paper-[0-9a-f]{8}\.log$`, paths[0], r)
		assert.Regexp(t, `^\.texops/logs/my_paper-[0-9a-f]{8}\.log$`, paths[1], r)
		assert.NotEqual(t, paths[0], paths[1], r)
		assert.Equal(t, "first document\n", readFile(t, paths[0], r), r)
		assert.Equal(t, "second document\n", readFile(t, paths[1], r), r)
	})

	t.Run("file mode keeps separate logs for names that differ only in case", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = successDone("bld_1")
		f.done["slides.tex"] = successDone("bld_2")
		f.logs["bld_1"] = "first document\n"
		f.logs["bld_2"] = "second document\n"
		projectDir(t, twoDocConfig("Paper", "paper"))

		r := runTx(t, f, "build", "--log", "file")

		require.Equal(t, cli.ExitOK, r.code, r)
		paths := summaryLogPaths(t, r)
		assert.Regexp(t, `^\.texops/logs/Paper-[0-9a-f]{8}\.log$`, paths[0], r)
		assert.Regexp(t, `^\.texops/logs/paper-[0-9a-f]{8}\.log$`, paths[1], r)
		assert.NotEqual(t, strings.ToLower(paths[0]), strings.ToLower(paths[1]), r)
		assert.Equal(t, "first document\n", readFile(t, paths[0], r), r)
		assert.Equal(t, "second document\n", readFile(t, paths[1], r), r)
	})

	t.Run("invalid --log value exits 2", func(t *testing.T) {
		f := newFakeTexOps(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "--log", "stderr")

		assert.Equal(t, cli.ExitUsage, r.code, r)
		assert.Equal(t, "Invalid value `stderr' for option `--log'. Allowed values are: stdout or file\n", r.stderr, r)
		assert.Empty(t, r.requests, r)
	})

	t.Run("a coding agent switches the default to file", func(t *testing.T) {
		t.Setenv("CLAUDECODE", "1")
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(3)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Equal(t, numberedLog(3), readFile(t, filepath.Join(".texops", "logs", "paper.log"), r), r)
		assert.NotContains(t, r.stderr, "latexmk output", r)
		assert.Contains(t, r.stderr, "Last 3 lines of .texops/logs/paper.log:\n", r)
	})

	t.Run("--log=stdout overrides agent detection", func(t *testing.T) {
		t.Setenv("CLAUDECODE", "1")
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(3)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--log", "stdout")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stderr, "    latexmk output for paper.tex\n", r)
		assert.NoDirExists(t, ".texops", r)
	})

	t.Run("TX_AGENT=none keeps the stdout default", func(t *testing.T) {
		t.Setenv("CLAUDECODE", "1")
		t.Setenv("TX_AGENT", "none")
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(3)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.Contains(t, r.stderr, "    latexmk output for paper.tex\n", r)
		assert.NoDirExists(t, ".texops", r)
	})

	t.Run("collected files skip .texops", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(3)
		projectDir(t, projectConfig)

		first := runTx(t, f, "build", "paper", "--log", "file")
		require.Equal(t, cli.ExitBuildFailed, first.code, first)
		require.FileExists(t, filepath.Join(".texops", "logs", "paper.log"), first)

		r := runTx(t, f, "build", "paper", "--log", "file")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.NotContains(t, r.requests, ".texops", r)
	})
}

func TestBuildDiagnosticsJSON(t *testing.T) {
	t.Run("--json defaults to file mode and fills errors, warnings and log", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.done["slides.tex"] = map[string]any{
			"status":   "success",
			"pdfUrl":   "/projects/prj_test/builds/bld_2/output",
			"build_id": "bld_2",
			"log_url":  "/projects/prj_test/builds/bld_2/log",
			"diagnostics": []map[string]any{
				{"severity": "warning", "kind": "duplicate_label", "file": "slides.tex", "line": 9, "message": "Label `a' multiply defined"},
			},
			"truncated": true,
		}
		f.logs["bld_1"] = numberedLog(3)
		f.logs["bld_2"] = numberedLog(1)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "--json")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		assert.JSONEq(t, `{
			"ok": false,
			"documents": [
				{
					"name": "paper", "main": "paper.tex", "status": "failed", "reason": "latex_error",
					"output": null, "log": ".texops/logs/paper.log", "build_id": "bld_1",
					"errors": [
						{"severity": "error", "file": "sec/intro.tex", "line": 3, "message": "Undefined control sequence.", "context": "\\badmacro"}
					],
					"warnings": [
						{"severity": "warning", "kind": "undefined_citation", "file": "sec/intro.tex", "line": 1, "message": "Citation `+"`"+`missing' undefined"},
						{"severity": "warning", "kind": "rerun", "message": "Label(s) may have changed. Rerun to get cross-references right."}
					],
					"truncated": false
				},
				{
					"name": "slides", "main": "slides.tex", "status": "succeeded", "reason": null,
					"output": "slides.pdf", "log": ".texops/logs/slides.log", "build_id": "bld_2",
					"errors": [],
					"warnings": [
						{"severity": "warning", "kind": "duplicate_label", "file": "slides.tex", "line": 9, "message": "Label `+"`"+`a' multiply defined"}
					],
					"truncated": true
				}
			]
		}`, stdoutWithoutDurations(t, r), r)
		assert.Equal(t, numberedLog(3), readFile(t, filepath.Join(".texops", "logs", "paper.log"), r), r)
		assert.NotContains(t, r.stderr, "latexmk output", r)
		assert.Contains(t, r.stderr, "      paper: FAILED, 1 error, 2 warnings (log: .texops/logs/paper.log)\n        sec/intro.tex:3: error: Undefined control sequence.\n", r)
	})

	t.Run("--json keeps diagnostics and the log when the PDF download fails", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = successWithWarningDone()
		f.logs["bld_ok"] = numberedLog(2)
		f.outputStatus = http.StatusInternalServerError
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--json")

		require.Equal(t, cli.ExitFailure, r.code, r)
		var doc struct {
			Documents []struct {
				Status   string           `json:"status"`
				Reason   string           `json:"reason"`
				Log      *string          `json:"log"`
				BuildID  string           `json:"build_id"`
				Errors   []map[string]any `json:"errors"`
				Warnings []map[string]any `json:"warnings"`
			} `json:"documents"`
		}
		decodeStdout(t, r, &doc)
		require.Len(t, doc.Documents, 1, r)
		d := doc.Documents[0]
		assert.Equal(t, "failed", d.Status, r)
		assert.Equal(t, "internal", d.Reason, r)
		assert.Equal(t, "bld_ok", d.BuildID, r)
		require.NotNil(t, d.Log, r)
		assert.Equal(t, ".texops/logs/paper.log", *d.Log, r)
		assert.Empty(t, d.Errors, r)
		require.Len(t, d.Warnings, 1, r)
		assert.Equal(t, "Reference `fig:x' undefined", d.Warnings[0]["message"], r)
		assert.Equal(t, numberedLog(2), readFile(t, filepath.Join(".texops", "logs", "paper.log"), r), r)
	})

	t.Run("--json with --log=stdout writes no log file", func(t *testing.T) {
		f := newFakeTexOps(t)
		f.done["paper.tex"] = latexErrorDone()
		f.logs["bld_1"] = numberedLog(3)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build", "paper", "--json", "--log=stdout")

		require.Equal(t, cli.ExitBuildFailed, r.code, r)
		var doc struct {
			Documents []struct {
				Log    *string `json:"log"`
				Errors []any   `json:"errors"`
			} `json:"documents"`
		}
		decodeStdout(t, r, &doc)
		require.Len(t, doc.Documents, 1, r)
		assert.Nil(t, doc.Documents[0].Log, r)
		assert.Len(t, doc.Documents[0].Errors, 1, r)
		assert.Contains(t, r.stderr, "    latexmk output for paper.tex\n", r)
		assert.NoDirExists(t, ".texops", r)
	})
}
