package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

type FileEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int64  `json:"-"`
}

func HashContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// txignoreEntry holds a compiled .txignore matcher and a secondary matcher
// that treats all patterns as positive (negation prefixes stripped). This
// allows us to detect when a negation-only match occurred: the real matcher
// returns (false, nil) while the any-pattern matcher returns true.
type txignoreEntry struct {
	matcher    *ignore.GitIgnore
	anyPattern *ignore.GitIgnore
}

func CollectFiles(ctx context.Context, dir string) ([]FileEntry, error) {
	ig := loadGitignore(dir)
	txignoreCache := make(map[string]*txignoreEntry)

	// Load root .txignore if present
	if entry, err := loadTxignore(dir); err != nil {
		return nil, err
	} else if entry != nil {
		txignoreCache["."] = entry
	}

	type fileInfo struct {
		path string
		size int64
	}
	var found []fileInfo
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		checkPath := rel
		if info.IsDir() {
			checkPath = rel + "/"
		}

		if ig.MatchesPath(checkPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if matchesTxignore(txignoreCache, rel, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			entry, loadErr := loadTxignore(filepath.Join(dir, rel))
			if loadErr != nil {
				return loadErr
			}
			if entry != nil {
				txignoreCache[rel] = entry
			}
		}

		if !info.IsDir() && info.Mode().IsRegular() {
			found = append(found, fileInfo{path: rel, size: info.Size()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(found, func(i, j int) bool {
		return found[i].path < found[j].path
	})

	entries := make([]FileEntry, 0, len(found))
	for _, f := range found {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		data, err := os.ReadFile(filepath.Join(dir, f.path))
		if err != nil {
			return nil, err
		}
		entries = append(entries, FileEntry{Path: f.path, Hash: HashContent(data), Size: f.size})
	}
	return entries, nil
}

func TotalSize(files []FileEntry) int64 {
	var total int64
	for _, f := range files {
		total += f.Size
	}
	return total
}

func FormatSize(bytes int64) string {
	switch {
	case bytes >= 1_000_000_000:
		return fmt.Sprintf("%.1f GB", float64(bytes)/1_000_000_000)
	case bytes >= 999_950:
		return fmt.Sprintf("%.1f MB", float64(bytes)/1_000_000)
	case bytes >= 1_000:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1_000)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func loadGitignore(dir string) *ignore.GitIgnore {
	patterns := []string{".git", ".texops.yaml", ".txignore"}

	gitignorePath := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err == nil {
		patterns = append(patterns, strings.Split(string(data), "\n")...)
	}

	return ignore.CompileIgnoreLines(patterns...)
}

func loadTxignore(dir string) (*txignoreEntry, error) {
	txignorePath := filepath.Join(dir, ".txignore")
	data, err := os.ReadFile(txignorePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", txignorePath, err)
	}
	patterns := strings.Split(string(data), "\n")

	// Build a secondary pattern list with negation prefixes stripped,
	// so we can detect whether ANY rule (positive or negated) matches a path.
	stripped := make([]string, len(patterns))
	for i, p := range patterns {
		stripped[i] = strings.TrimPrefix(p, "!")
	}

	return &txignoreEntry{
		matcher:    ignore.CompileIgnoreLines(patterns...),
		anyPattern: ignore.CompileIgnoreLines(stripped...),
	}, nil
}

func matchesTxignore(cache map[string]*txignoreEntry, relPath string, isDir bool) bool {
	dir := filepath.Dir(relPath)
	for {
		if entry, ok := cache[dir]; ok {
			localPath, err := filepath.Rel(dir, relPath)
			if err == nil {
				if isDir {
					localPath = localPath + "/"
				}
				matched, pattern := entry.matcher.MatchesPathHow(localPath)
				if pattern != nil {
					// A positive pattern matched (possibly then negated).
					// This level is authoritative.
					return matched
				}
				// MatchesPathHow returns (false, nil) for both "no rules matched"
				// and "only a negation rule matched". Use anyPattern to distinguish:
				// if any rule's base pattern matches, a negation actively un-ignored
				// this file, so this level is authoritative (returning false).
				if entry.anyPattern.MatchesPath(localPath) {
					return false
				}
			}
		}

		if dir == "." {
			break
		}
		dir = filepath.Dir(dir)
	}

	return false
}
