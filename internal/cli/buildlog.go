package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	LogStdout = "stdout"
	LogFile   = "file"
)

const logTailLines = 20

// resolveLogMode returns the --log value, defaulting to file under a coding
// agent or with --json and to stdout otherwise.
func resolveLogMode(flag string, jsonMode bool, getenv func(string) string) string {
	if flag != "" {
		return flag
	}
	if jsonMode || DetectAgent(getenv) != "" {
		return LogFile
	}
	return LogStdout
}

// logFilePaths maps each document name to the project-relative path of its
// saved build log. Names that clash after sanitizing, ignoring case, get a
// hash suffix so that every document keeps its own file.
func logFilePaths(docs []Document) map[string]string {
	bases := make(map[string]string, len(docs))
	clashes := map[string]int{}
	for _, doc := range docs {
		if _, ok := bases[doc.Name]; ok {
			continue
		}
		base := logFileName(doc.Name)
		bases[doc.Name] = base
		clashes[strings.ToLower(base)]++
	}
	paths := make(map[string]string, len(bases))
	for name, base := range bases {
		if clashes[strings.ToLower(base)] > 1 {
			sum := sha256.Sum256([]byte(name))
			base += "-" + hex.EncodeToString(sum[:4])
		}
		paths[name] = filepath.Join(".texops", "logs", base+".log")
	}
	return paths
}

func logFileName(docName string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("._-", r):
			return r
		default:
			return '_'
		}
	}, docName)
	if strings.Trim(name, ".") == "" {
		return "document"
	}
	return name
}

func removeBuildLog(dir, rel string) error {
	err := os.Remove(filepath.Join(dir, rel))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// saveBuildLog downloads the build log to rel, creating .texops/.gitignore
// on first use.
func saveBuildLog(ctx context.Context, inst *InstanceClient, dir, rel, logURL string) error {
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o750); err != nil {
		return err
	}
	if err := ensureTexopsGitignore(dir); err != nil {
		return err
	}
	return inst.DownloadLog(ctx, logURL, filepath.Join(dir, rel))
}

func ensureTexopsGitignore(dir string) error {
	path := filepath.Join(dir, ".texops", ".gitignore")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString("*\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

const logTailReadBytes = 64 * 1024

func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := max(info.Size()-logTailReadBytes, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	if offset > 0 && len(lines) > 1 {
		lines = lines[1:]
	}
	return lastLines(lines, n), nil
}

func lastLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

type lineTail struct {
	n     int
	lines []string
}

func (t *lineTail) add(line string) {
	t.lines = append(t.lines, line)
	if len(t.lines) > 2*t.n {
		t.lines = append(t.lines[:0], t.lines[len(t.lines)-t.n:]...)
	}
}

func (t *lineTail) last() []string {
	return lastLines(t.lines, t.n)
}

// printLogTail shows the end of a failed build's log on stderr: the saved
// log when there is one, otherwise the streamed output.
func printLogTail(ui *UI, dir, logPath string, streamed *lineTail) {
	lines := streamed.last()
	source := "build output"
	if logPath != "" {
		if fileLines, err := tailFile(filepath.Join(dir, logPath), logTailLines); err == nil && hasText(fileLines) {
			lines, source = fileLines, filepath.ToSlash(logPath)
		}
	}
	if !hasText(lines) {
		return
	}
	ui.DimInfo(fmt.Sprintf("Last %s of %s:", plural(len(lines), "line"), source))
	for _, line := range lines {
		ui.StreamLog(line)
	}
}

func hasText(lines []string) bool {
	return slices.ContainsFunc(lines, func(line string) bool { return line != "" })
}
