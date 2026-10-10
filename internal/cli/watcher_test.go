package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

func waitForEvent(t *testing.T, events <-chan string, timeout time.Duration) (string, bool) {
	t.Helper()
	select {
	case ev, ok := <-events:
		return ev, ok
	case <-time.After(timeout):
		return "", false
	}
}

func noEvent(t *testing.T, events <-chan string, wait time.Duration) bool {
	t.Helper()
	select {
	case ev := <-events:
		t.Errorf("unexpected event: %s", ev)
		return false
	case <-time.After(wait):
		return true
	}
}

func TestFileWatcher(t *testing.T) {
	t.Run("detects file creation", func(t *testing.T) {
		dir := t.TempDir()

		w, err := cli.NewFileWatcher(dir, nil)
		require.NoError(t, err)
		defer w.Close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go w.Run(ctx)

		// Create a file
		err = os.WriteFile(filepath.Join(dir, "test.tex"), []byte("hello"), 0o600)
		require.NoError(t, err)

		ev, ok := waitForEvent(t, w.Events, 2*time.Second)
		assert.True(t, ok, "expected an event")
		assert.Equal(t, "test.tex", ev)
	})

	t.Run("filters editor noise files", func(t *testing.T) {
		dir := t.TempDir()

		w, err := cli.NewFileWatcher(dir, nil)
		require.NoError(t, err)
		defer w.Close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go w.Run(ctx)

		// Create editor noise files
		noiseFiles := []string{"file~", "4913", ".DS_Store", "file.swp", "file.swo", ".tx-download-abc.tmp"}
		for _, name := range noiseFiles {
			err = os.WriteFile(filepath.Join(dir, name), []byte("noise"), 0o600)
			require.NoError(t, err)
		}

		// None of these should produce events
		noEvent(t, w.Events, 500*time.Millisecond)

		// Now create a real file to confirm the watcher is working
		err = os.WriteFile(filepath.Join(dir, "real.tex"), []byte("content"), 0o600)
		require.NoError(t, err)

		ev, ok := waitForEvent(t, w.Events, 2*time.Second)
		assert.True(t, ok, "expected event for real file")
		assert.Equal(t, "real.tex", ev)
	})

	t.Run("excludes specified files", func(t *testing.T) {
		dir := t.TempDir()

		w, err := cli.NewFileWatcher(dir, []string{"output.pdf"})
		require.NoError(t, err)
		defer w.Close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go w.Run(ctx)

		// Write to an excluded file
		err = os.WriteFile(filepath.Join(dir, "output.pdf"), []byte("pdf"), 0o600)
		require.NoError(t, err)

		noEvent(t, w.Events, 500*time.Millisecond)

		// Write to a non-excluded file
		err = os.WriteFile(filepath.Join(dir, "main.tex"), []byte("content"), 0o600)
		require.NoError(t, err)

		ev, ok := waitForEvent(t, w.Events, 2*time.Second)
		assert.True(t, ok, "expected event for non-excluded file")
		assert.Equal(t, "main.tex", ev)
	})

	t.Run("ignores saved build logs under .texops", func(t *testing.T) {
		dir := t.TempDir()

		w, err := cli.NewFileWatcher(dir, nil)
		require.NoError(t, err)
		defer w.Close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go w.Run(ctx)

		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".texops", "logs"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops", ".gitignore"), []byte("*\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops", "logs", "paper.log"), []byte("log"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops", "logs", "paper.log"), []byte("log again"), 0o600))

		noEvent(t, w.Events, 500*time.Millisecond)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tex"), []byte("content"), 0o600))

		ev, ok := waitForEvent(t, w.Events, 2*time.Second)
		assert.True(t, ok, "expected event for main.tex")
		assert.Equal(t, "main.tex", ev)
	})

	t.Run("watches new subdirectories", func(t *testing.T) {
		dir := t.TempDir()

		w, err := cli.NewFileWatcher(dir, nil)
		require.NoError(t, err)
		defer w.Close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go w.Run(ctx)

		// Create a new subdirectory
		subdir := filepath.Join(dir, "chapters")
		err = os.MkdirAll(subdir, 0o750)
		require.NoError(t, err)

		// Give the watcher time to add the new directory
		time.Sleep(200 * time.Millisecond)

		// Create a file inside the new subdirectory
		err = os.WriteFile(filepath.Join(subdir, "ch1.tex"), []byte("chapter 1"), 0o600)
		require.NoError(t, err)

		ev, ok := waitForEvent(t, w.Events, 2*time.Second)
		assert.True(t, ok, "expected event for file in new subdirectory")
		assert.Equal(t, filepath.Join("chapters", "ch1.tex"), ev)
	})

	t.Run("ignores gitignored files", func(t *testing.T) {
		dir := t.TempDir()

		// Create .gitignore
		err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.aux\nbuild/\n"), 0o600)
		require.NoError(t, err)

		w, err := cli.NewFileWatcher(dir, nil)
		require.NoError(t, err)
		defer w.Close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go w.Run(ctx)

		// Create a gitignored file
		err = os.WriteFile(filepath.Join(dir, "output.aux"), []byte("aux"), 0o600)
		require.NoError(t, err)

		// Create a gitignored directory and file inside it
		err = os.MkdirAll(filepath.Join(dir, "build"), 0o750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, "build", "out.pdf"), []byte("pdf"), 0o600)
		require.NoError(t, err)

		noEvent(t, w.Events, 500*time.Millisecond)

		// Create a non-ignored file
		err = os.WriteFile(filepath.Join(dir, "main.tex"), []byte("content"), 0o600)
		require.NoError(t, err)

		ev, ok := waitForEvent(t, w.Events, 2*time.Second)
		assert.True(t, ok, "expected event for non-ignored file")
		assert.Equal(t, "main.tex", ev)
	})
}

func TestWatchAndBuild(t *testing.T) {
	t.Run("rapid events within 500ms trigger only one build", func(t *testing.T) {
		dir := t.TempDir()

		ui := cli.NewUIWithOptions(&bytes.Buffer{}, false, nil)

		var buildCount atomic.Int32
		build := func(ctx context.Context) ([]cli.DocResult, error) {
			buildCount.Add(1)
			return []cli.DocResult{{Name: "doc", Output: "doc.pdf", Success: true}}, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- cli.WatchAndBuildWith(ctx, dir, ui, build)
		}()

		// Give the watcher time to start
		time.Sleep(100 * time.Millisecond)

		// Write multiple files rapidly (within 500ms debounce window)
		for i := range 5 {
			err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.tex", i)), []byte("content"), 0o600)
			require.NoError(t, err)
			time.Sleep(50 * time.Millisecond)
		}

		// Wait for debounce (500ms) + build time + margin
		time.Sleep(1000 * time.Millisecond)

		assert.Equal(t, int32(1), buildCount.Load(), "expected exactly one build from rapid events")

		cancel()
		<-done
	})

	t.Run("changes during build trigger exactly one follow-up rebuild", func(t *testing.T) {
		dir := t.TempDir()

		ui := cli.NewUIWithOptions(&bytes.Buffer{}, false, nil)

		var buildCount atomic.Int32
		buildStarted := make(chan struct{}, 5)
		buildRelease := make(chan struct{})

		build := func(ctx context.Context) ([]cli.DocResult, error) {
			n := buildCount.Add(1)
			buildStarted <- struct{}{}
			if n == 1 {
				// First build blocks until released
				select {
				case <-buildRelease:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return []cli.DocResult{{Name: "doc", Output: "doc.pdf", Success: true}}, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- cli.WatchAndBuildWith(ctx, dir, ui, build)
		}()

		// Give the watcher time to start
		time.Sleep(100 * time.Millisecond)

		// Trigger first build
		err := os.WriteFile(filepath.Join(dir, "a.tex"), []byte("v1"), 0o600)
		require.NoError(t, err)

		// Wait for first build to start
		select {
		case <-buildStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("first build did not start")
		}

		// While first build is running, make multiple changes
		// These should coalesce into exactly one follow-up build
		for i := range 3 {
			err = os.WriteFile(filepath.Join(dir, fmt.Sprintf("b%d.tex", i)), []byte("content"), 0o600)
			require.NoError(t, err)
			time.Sleep(50 * time.Millisecond)
		}

		// Wait for debounce to fire (signal gets queued)
		time.Sleep(700 * time.Millisecond)

		// Release the first build
		close(buildRelease)

		// Wait for the follow-up build to start and finish
		select {
		case <-buildStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("follow-up build did not start")
		}

		// Give it time to ensure no extra builds
		time.Sleep(500 * time.Millisecond)

		assert.Equal(t, int32(2), buildCount.Load(), "expected exactly two builds: initial + one follow-up")

		cancel()
		<-done
	})

	t.Run("live rebuild prints the diagnostics of failed documents", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		ui := cli.NewUIWithOptions(&out, false, nil)

		buildStarted := make(chan struct{}, 5)
		build := func(ctx context.Context) ([]cli.DocResult, error) {
			buildStarted <- struct{}{}
			return []cli.DocResult{
				{Name: "paper", Output: "paper.pdf", Log: ".texops/logs/paper.log", Diagnostics: []cli.Diagnostic{
					{Severity: "error", File: "paper.tex", Line: 3, Message: "Undefined control sequence."},
				}},
				{Name: "slides", Output: "slides.pdf", Success: true},
			}, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- cli.WatchAndBuildWith(ctx, dir, ui, build)
		}()
		time.Sleep(100 * time.Millisecond)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.tex"), []byte("v1"), 0o600))

		select {
		case <-buildStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("build did not start")
		}
		cancel()
		<-done

		output := out.String()
		assert.Contains(t, output, "  paper: FAILED, 1 error, 0 warnings (log: .texops/logs/paper.log)\n    paper.tex:3: error: Undefined control sequence.\n", output)
		assert.NotContains(t, output, "slides =>", output)
	})

	t.Run("live rebuild prints the warnings of successful documents", func(t *testing.T) {
		dir := t.TempDir()
		var out bytes.Buffer
		ui := cli.NewUIWithOptions(&out, false, nil)

		buildStarted := make(chan struct{}, 5)
		build := func(ctx context.Context) ([]cli.DocResult, error) {
			buildStarted <- struct{}{}
			return []cli.DocResult{
				{Name: "paper", Output: "paper.pdf", Success: true, Diagnostics: []cli.Diagnostic{
					{Severity: "warning", Kind: "undefined_reference", File: "paper.tex", Line: 7, Message: "Reference `fig:x' undefined"},
				}},
			}, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- cli.WatchAndBuildWith(ctx, dir, ui, build)
		}()
		time.Sleep(100 * time.Millisecond)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.tex"), []byte("v1"), 0o600))

		select {
		case <-buildStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("build did not start")
		}
		cancel()
		<-done

		output := out.String()
		assert.Contains(t, output, "Built paper.pdf\n", output)
		assert.Contains(t, output, "  paper => paper.pdf, 0 errors, 1 warning\n    paper.tex:7: warning: Reference `fig:x' undefined\n", output)
	})
}
