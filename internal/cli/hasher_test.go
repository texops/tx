package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

// cancelAfterN wraps a context and returns context.Canceled after the
// underlying Err() has been called more than n times. This lets tests
// deterministically cancel between the walk phase and the hashing phase
// of CollectFiles.
type cancelAfterN struct {
	context.Context //nolint:containedctx

	calls atomic.Int32
	n     int32
}

func (c *cancelAfterN) Err() error {
	if c.calls.Add(1) > c.n {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestHashContent(t *testing.T) {
	t.Run("produces consistent SHA-256 hash for known content", func(t *testing.T) {
		hash := cli.HashContent([]byte("hello world"))
		assert.Equal(t, "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9", hash)
	})

	t.Run("produces different hashes for different content", func(t *testing.T) {
		hash1 := cli.HashContent([]byte("aaa"))
		hash2 := cli.HashContent([]byte("bbb"))
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("produces same hash for same content", func(t *testing.T) {
		hash1 := cli.HashContent([]byte("same"))
		hash2 := cli.HashContent([]byte("same"))
		assert.Equal(t, hash1, hash2)
	})
}

func TestCollectFiles(t *testing.T) {
	t.Run("collects files and produces hashes", func(t *testing.T) {
		dir := t.TempDir()
		paperContent := []byte("\\documentclass{article}")
		refsContent := []byte("@article{foo}")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), paperContent, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "refs.bib"), refsContent, 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		assert.Len(t, files, 2)
		assert.Equal(t, "paper.tex", files[0].Path)
		assert.Equal(t, "refs.bib", files[1].Path)
		assert.Len(t, files[0].Hash, 64)
		assert.Len(t, files[1].Hash, 64)
		assert.Equal(t, int64(len(paperContent)), files[0].Size)
		assert.Equal(t, int64(len(refsContent)), files[1].Size)
	})

	t.Run("respects .gitignore patterns", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.aux\nbuild/\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.aux"), []byte("aux content"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "build"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "build", "output.pdf"), []byte("pdf"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Contains(t, paths, "paper.tex")
		assert.NotContains(t, paths, "paper.aux")
		assert.NotContains(t, paths, "build/output.pdf")
	})

	t.Run("excludes .git and .texops.yaml", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte("version: 2021"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("git config"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Equal(t, []string{"paper.tex"}, paths)
	})

	t.Run("collects files from subdirectories", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "chapters"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tex"), []byte("main"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "chapters", "intro.tex"), []byte("intro"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Equal(t, []string{"chapters/intro.tex", "main.tex"}, paths)
	})

	t.Run("returns empty slice for empty directory", func(t *testing.T) {
		dir := t.TempDir()
		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		assert.Empty(t, files)
	})

	t.Run("root .txignore excludes files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".txignore"), []byte("*.log\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log data"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Contains(t, paths, "paper.tex")
		assert.NotContains(t, paths, "debug.log")
	})

	t.Run("txignore works alongside gitignore", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.aux\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".txignore"), []byte("*.log\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.aux"), []byte("aux data"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log data"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Contains(t, paths, "paper.tex")
		assert.NotContains(t, paths, "paper.aux", ".gitignore should still exclude .aux files")
		assert.NotContains(t, paths, "debug.log", ".txignore should exclude .log files")
	})

	t.Run("nested txignore in subdirectory excludes files in that subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "figures"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "figures", ".txignore"), []byte("*.psd\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.psd"), []byte("root psd"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "figures", "diagram.psd"), []byte("psd data"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "figures", "diagram.png"), []byte("png data"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Contains(t, paths, "paper.tex")
		assert.Contains(t, paths, "paper.psd", "root .psd should not be excluded by figures/.txignore")
		assert.NotContains(t, paths, "figures/diagram.psd", "figures/.txignore should exclude .psd in figures/")
		assert.Contains(t, paths, "figures/diagram.png", "figures/.txignore should not exclude .png files")
	})

	t.Run("nested txignore does not affect parent directories", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "figures"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "figures", ".txignore"), []byte("*.tmp\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "root.tmp"), []byte("tmp data"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "figures", "sketch.tmp"), []byte("tmp data"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Contains(t, paths, "root.tmp", "root .tmp should not be excluded by figures/.txignore")
		assert.Contains(t, paths, "paper.tex")
		assert.NotContains(t, paths, "figures/sketch.tmp", "figures/.txignore should exclude .tmp in figures/")
	})

	t.Run("txignore file itself is excluded from collection", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".txignore"), []byte("*.log\n"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "figures"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "figures", ".txignore"), []byte("*.psd\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.NotContains(t, paths, ".txignore", ".txignore itself should be excluded")
		assert.NotContains(t, paths, "figures/.txignore", "nested .txignore should also be excluded")
		assert.Contains(t, paths, "paper.tex")
	})

	t.Run("child txignore negation overrides parent txignore", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".txignore"), []byte("*.tmp\n"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "data"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data", ".txignore"), []byte("*.tmp\n!keep.tmp\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.tmp"), []byte("tmp data"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data", "scratch.tmp"), []byte("scratch"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data", "keep.tmp"), []byte("important"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Contains(t, paths, "paper.tex")
		assert.NotContains(t, paths, "notes.tmp", "root .tmp files should be excluded by root .txignore")
		assert.NotContains(t, paths, "data/scratch.tmp", "data/scratch.tmp should be excluded by data/.txignore")
		assert.Contains(t, paths, "data/keep.tmp", "data/keep.tmp should be included via negation in data/.txignore")
	})

	t.Run("child negation-only txignore overrides parent ignore", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".txignore"), []byte("*.tmp\n"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "data"), 0o750))
		// Child has ONLY a negation rule, no positive rules
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data", ".txignore"), []byte("!keep.tmp\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.tmp"), []byte("tmp data"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data", "scratch.tmp"), []byte("scratch"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data", "keep.tmp"), []byte("important"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Contains(t, paths, "paper.tex")
		assert.NotContains(t, paths, "notes.tmp", "root .tmp files should be excluded by root .txignore")
		assert.NotContains(t, paths, "data/scratch.tmp", "data/scratch.tmp should fall through to parent .txignore")
		assert.Contains(t, paths, "data/keep.tmp", "negation-only child .txignore should override parent for keep.tmp")
	})

	t.Run("txignore read permission error is propagated", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("chmod 0000 has no effect when running as root")
		}
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".txignore"), []byte("*.log\n"), 0o000))

		_, err := cli.CollectFiles(t.Context(), dir)
		assert.Error(t, err, "should return error when .txignore exists but cannot be read")
	})

	t.Run("txignore cannot un-ignore gitignore exclusions", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.aux\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".txignore"), []byte("!paper.aux\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.aux"), []byte("aux data"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.NotContains(t, paths, "paper.aux", ".txignore negation should not override .gitignore exclusions")
		assert.Contains(t, paths, "paper.tex")
	})

	t.Run("directory exclusion via txignore", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".txignore"), []byte("drafts/\n"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "drafts"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "drafts", "old.tex"), []byte("old content"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		assert.Contains(t, paths, "paper.tex")
		assert.NotContains(t, paths, "drafts/old.tex", "drafts/ directory should be skipped entirely by .txignore")
	})

	t.Run("returns error when context is already cancelled", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("content"), 0o600))

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := cli.CollectFiles(ctx, dir)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("returns error when context cancelled during hashing", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.tex"), []byte("content"), 0o600))

		// Walk visits root "." and "a.tex" (2 Err() calls).
		// The 3rd call happens in the hashing loop and should trigger cancellation.
		ctx := &cancelAfterN{Context: t.Context(), n: 2}

		_, err := cli.CollectFiles(ctx, dir)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("populates Size for all files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.tex"), []byte("hello"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.tex"), []byte("world!"), 0o600))

		files, err := cli.CollectFiles(t.Context(), dir)
		require.NoError(t, err)
		require.Len(t, files, 2)
		assert.Equal(t, int64(5), files[0].Size) // "hello" = 5 bytes
		assert.Equal(t, int64(6), files[1].Size) // "world!" = 6 bytes
	})
}

func TestTotalSize(t *testing.T) {
	t.Run("sums file sizes", func(t *testing.T) {
		files := []cli.FileEntry{
			{Path: "a.tex", Size: 100},
			{Path: "b.tex", Size: 200},
			{Path: "c.tex", Size: 300},
		}
		assert.Equal(t, int64(600), cli.TotalSize(files))
	})

	t.Run("returns zero for empty slice", func(t *testing.T) {
		assert.Equal(t, int64(0), cli.TotalSize(nil))
	})

	t.Run("returns zero for files with zero size", func(t *testing.T) {
		files := []cli.FileEntry{
			{Path: "a.tex", Size: 0},
		}
		assert.Equal(t, int64(0), cli.TotalSize(files))
	})
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{999, "999 B"},
		{1_000, "1.0 KB"},
		{1_500, "1.5 KB"},
		{999_999, "1.0 MB"},
		{1_000_000, "1.0 MB"},
		{4_200_000, "4.2 MB"},
		{52_000_000, "52.0 MB"},
		{1_000_000_000, "1.0 GB"},
		{2_500_000_000, "2.5 GB"},
	}
	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			assert.Equal(t, tc.expected, cli.FormatSize(tc.bytes))
		})
	}
}
