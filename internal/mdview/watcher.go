package mdview

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

const (
	debounceDelay = 100 * time.Millisecond
)

// OnChange is the callback invoked when a given file (absolute path) changes.
// The Orchestrator injects it to decide the broadcast protocol.
type OnChange func(absPath string, content []byte)

// StartFileWatcher watches a single file and calls onChange when it changes.
func StartFileWatcher(filePath string, onChange OnChange) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs path error: %v\n", err)
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watcher error: %v\n", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(absPath); err != nil {
		fmt.Fprintf(os.Stderr, "watch add error: %v\n", err)
		return
	}

	var timer *time.Timer
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !(event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			path := absPath
			timer = time.AfterFunc(debounceDelay, func() {
				content, err := os.ReadFile(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "read file error: %v\n", err)
					logging.Warn("[Watcher] failed to read changed file: %v", err)
					return
				}
				logging.Debug("[Watcher] file change detected: %s (%d bytes)", path, len(content))
				onChange(path, content)
			})
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "watcher event error: %v\n", err)
		}
	}
}

// FolderWatcher is the Worker that watches only "the current active file" in folder mode.
//
// It used to register the whole root recursively with the watcher, but in directories with
// many files/subfolders the OS watch handles (macOS kqueue uses a file descriptor per directory)
// ran out and hung the process/system. Since only the active file is ever broadcast,
// it is narrowed to watch just the active file's parent directory.
// (The parent directory, not the file, is watched and events are filtered by file name,
// so editors' atomic saves that replace the file via rename are not missed.)
type FolderWatcher struct {
	onChange OnChange
	watcher  *fsnotify.Watcher

	mu          sync.Mutex
	activeFile  string // absolute path of the currently watched file
	watchedDir  string // parent directory of activeFile (the path currently passed to watcher.Add)
	debounceTmr *time.Timer
}

// NewFolderWatcher creates a FolderWatcher. It returns nil on failure.
func NewFolderWatcher(onChange OnChange) *FolderWatcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watcher error: %v\n", err)
		return nil
	}
	return &FolderWatcher{onChange: onChange, watcher: watcher}
}

// SetActive switches the watch target to a new active file. An empty string stops watching.
// At most one directory is registered with the watcher at a time.
func (fw *FolderWatcher) SetActive(absPath string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if absPath == fw.activeFile {
		return
	}
	fw.activeFile = absPath

	newDir := ""
	if absPath != "" {
		newDir = filepath.Dir(absPath)
	}
	if newDir == fw.watchedDir {
		return
	}
	if fw.watchedDir != "" {
		_ = fw.watcher.Remove(fw.watchedDir)
	}
	fw.watchedDir = newDir
	if newDir != "" {
		if err := fw.watcher.Add(newDir); err != nil {
			fmt.Fprintf(os.Stderr, "watch add %s: %v\n", newDir, err)
			logging.Warn("[Watcher] failed to watch %s: %v", newDir, err)
			fw.watchedDir = ""
		}
	}
	logging.Debug("[Watcher] watch target switched: %s", absPath)
}

// Run runs the event loop (blocking).
func (fw *FolderWatcher) Run() {
	defer fw.watcher.Close()
	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			fw.handleEvent(event)
		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "watcher event error: %v\n", err)
		}
	}
}

func (fw *FolderWatcher) handleEvent(event fsnotify.Event) {
	if !(event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
		return
	}
	if !IsViewablePath(event.Name) {
		return
	}

	fw.mu.Lock()
	active := fw.activeFile
	if active == "" || event.Name != active {
		fw.mu.Unlock()
		return
	}
	if fw.debounceTmr != nil {
		fw.debounceTmr.Stop()
	}
	fw.debounceTmr = time.AfterFunc(debounceDelay, func() {
		content, err := os.ReadFile(active)
		if err != nil {
			// The file may have just been deleted. Ignore quietly.
			logging.Debug("[Watcher] skipped reading changed file (likely deleted): %s", active)
			return
		}
		logging.Debug("[Watcher] file change detected: %s (%d bytes)", active, len(content))
		fw.onChange(active, content)
	})
	fw.mu.Unlock()
}
