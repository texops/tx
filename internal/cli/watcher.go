package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	ignore "github.com/sabhiram/go-gitignore"
)

type FileWatcher struct {
	fsw           *fsnotify.Watcher
	dir           string
	gitignore     *ignore.GitIgnore
	txignoreCache map[string]*txignoreEntry
	excludes      map[string]bool
	Events        chan string
	Errors        chan error
}

var editorNoiseNames = map[string]bool{
	"4913":      true,
	".DS_Store": true,
}

func isEditorNoise(name string) bool {
	base := filepath.Base(name)
	if editorNoiseNames[base] {
		return true
	}
	if strings.HasSuffix(base, "~") {
		return true
	}
	if strings.HasSuffix(base, ".swp") {
		return true
	}
	if strings.HasSuffix(base, ".swo") {
		return true
	}
	if strings.HasPrefix(base, ".tx-download-") && strings.HasSuffix(base, ".tmp") {
		return true
	}
	return false
}

func NewFileWatcher(dir string, excludes []string) (*FileWatcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ig := loadGitignore(dir)
	txignoreCache := make(map[string]*txignoreEntry)
	entry, loadErr := loadTxignore(dir)
	if loadErr != nil {
		_ = fsw.Close()
		return nil, loadErr
	}
	if entry != nil {
		txignoreCache["."] = entry
	}

	excludeMap := make(map[string]bool)
	for _, e := range excludes {
		excludeMap[e] = true
	}

	w := &FileWatcher{
		fsw:           fsw,
		dir:           dir,
		gitignore:     ig,
		txignoreCache: txignoreCache,
		excludes:      excludeMap,
		Events:        make(chan string, 1),
		Errors:        make(chan error, 1),
	}

	err = w.addRecursive(dir)
	if err != nil {
		_ = fsw.Close()
		return nil, err
	}

	return w, nil
}

func (w *FileWatcher) Run(ctx context.Context) {
	defer close(w.Events)
	defer close(w.Errors)

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.sendError(err)
		}
	}
}

func (w *FileWatcher) Close() error {
	return w.fsw.Close()
}

func (w *FileWatcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel(w.dir, path)
		if relErr != nil {
			return relErr
		}

		if rel != "." {
			if w.isIgnored(rel, true) {
				return filepath.SkipDir
			}
			entry, loadErr := loadTxignore(path)
			if loadErr != nil {
				return loadErr
			}
			if entry != nil {
				w.txignoreCache[rel] = entry
			}
		}

		return w.fsw.Add(path)
	})
}

func (w *FileWatcher) isIgnored(rel string, isDir bool) bool {
	checkPath := rel
	if isDir {
		checkPath = rel + "/"
	}
	if w.gitignore.MatchesPath(checkPath) {
		return true
	}
	if matchesTxignore(w.txignoreCache, rel, isDir) {
		return true
	}
	return false
}

func (w *FileWatcher) reloadIgnoreRules() {
	w.gitignore = loadGitignore(w.dir)
	newCache := make(map[string]*txignoreEntry)
	if entry, err := loadTxignore(w.dir); err == nil && entry != nil {
		newCache["."] = entry
	}
	w.txignoreCache = newCache
	if err := w.addRecursive(w.dir); err != nil {
		w.sendError(fmt.Errorf("reloading watched directories: %w", err))
	}
}

func (w *FileWatcher) handleEvent(ev fsnotify.Event) {
	rel, err := filepath.Rel(w.dir, ev.Name)
	if err != nil {
		return
	}

	if ev.Has(fsnotify.Chmod) && !ev.Has(fsnotify.Write) && !ev.Has(fsnotify.Create) {
		return
	}

	if isEditorNoise(rel) {
		return
	}

	if w.excludes[rel] {
		return
	}

	if base := filepath.Base(rel); base == ".gitignore" || base == ".txignore" {
		w.reloadIgnoreRules()
		w.sendEvent(rel)
		return
	}

	if ev.Has(fsnotify.Create) {
		info, statErr := os.Stat(ev.Name)
		if statErr == nil && info.IsDir() {
			if !w.isIgnored(rel, true) {
				if addErr := w.addRecursive(ev.Name); addErr != nil {
					w.sendError(fmt.Errorf("watching new directory %s: %w", rel, addErr))
				}
			}
			return
		}
	}

	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		info, statErr := os.Stat(ev.Name)
		if statErr != nil {
			if !w.isIgnored(rel, false) {
				w.sendEvent(rel)
			}
			return
		}
		if info.IsDir() {
			return
		}
	}

	isDir := false
	if info, statErr := os.Stat(ev.Name); statErr == nil {
		isDir = info.IsDir()
	}
	if isDir {
		return
	}

	if w.isIgnored(rel, false) {
		return
	}

	w.sendEvent(rel)
}

// sendEvent coalesces rapid file events: buffer-1 channel with non-blocking send
// means multiple events between consumer reads collapse into one rebuild trigger.
func (w *FileWatcher) sendEvent(rel string) {
	select {
	case w.Events <- rel:
	default:
	}
}

func (w *FileWatcher) sendError(err error) {
	select {
	case w.Errors <- err:
	default:
	}
}

const debounceDuration = 500 * time.Millisecond

func watchAndBuild(ctx context.Context, dir string, p buildParams) error {
	excludes := make([]string, 0, len(p.docs))
	for _, doc := range p.docs {
		excludes = append(excludes, doc.Output)
	}
	return watchAndBuildWith(ctx, dir, p.ui, func(ctx context.Context) ([]docResult, error) {
		return buildOnce(ctx, p)
	}, excludes)
}

func watchAndBuildWith(ctx context.Context, dir string, ui *UI, build func(context.Context) ([]docResult, error), excludes []string) error {
	fw, err := NewFileWatcher(dir, excludes)
	if err != nil {
		return fmt.Errorf("failed to start file watcher: %w", err)
	}
	defer fw.Close()

	ui.Log("")
	ui.Status("Watching for changes... (Ctrl+C to stop)")

	go fw.Run(ctx)

	buildSignal := make(chan struct{}, 1)

	go func() {
		var debounceTimer *time.Timer
		var debounceC <-chan time.Time
		for {
			select {
			case <-ctx.Done():
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				return
			case _, ok := <-fw.Events:
				if !ok {
					return
				}
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.NewTimer(debounceDuration)
				debounceC = debounceTimer.C
			case err, ok := <-fw.Errors:
				if !ok {
					return
				}
				ui.Errorf("Watcher: %s", err)
			case <-debounceC:
				debounceC = nil
				select {
				case buildSignal <- struct{}{}:
				default:
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-buildSignal:
			runBuildCycle(ctx, ui, build, buildSignal)
		}
	}
}

func runBuildCycle(ctx context.Context, ui *UI, build func(context.Context) ([]docResult, error), buildSignal chan struct{}) {
	for {
		results, err := build(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			ui.Errorf("Build error: %s", err)
		} else {
			printBuildResult(ui, results)
		}

		select {
		case <-buildSignal:
			if ctx.Err() != nil {
				return
			}
			continue
		default:
			return
		}
	}
}

func printBuildResult(ui *UI, results []docResult) {
	now := time.Now().Format("15:04:05")
	var succeeded []string
	anyFailed := false
	for _, r := range results {
		if r.Success {
			succeeded = append(succeeded, filepath.Base(r.Output))
		} else {
			anyFailed = true
		}
	}
	if anyFailed {
		for _, r := range results {
			if !r.Success {
				ui.Errorf("[%s] Build failed: %s", now, r.Name)
			}
		}
	}
	if len(succeeded) > 0 {
		ui.Status(fmt.Sprintf("[%s] Built %s", now, strings.Join(succeeded, ", ")))
	}
}
