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

// OnChange는 특정 파일(절대 경로)이 변경되었을 때 호출되는 콜백이다.
// Orchestrator가 주입하여 브로드캐스트 규약을 결정한다.
type OnChange func(absPath string, content []byte)

// StartFileWatcher는 단일 파일을 감시하여 변경이 있을 때 onChange를 호출한다.
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
					logging.Warn("[Watcher] 변경 파일 읽기 실패: %v", err)
					return
				}
				logging.Debug("[Watcher] 파일 변경 감지: %s (%d bytes)", path, len(content))
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

// FolderWatcher는 folder 모드에서 "현재 활성 파일 하나"만 감시하는 Worker이다.
//
// 과거에는 root 전체를 재귀적으로 watcher에 등록했는데, 파일/하위 폴더가 많은
// 디렉토리에서는 OS 감시 핸들(macOS kqueue는 디렉토리마다 파일 디스크립터를 소비)이
// 고갈되어 프로세스/시스템이 멈추는 문제가 있었다. 어차피 브로드캐스트되는 것은
// 활성 파일 한 개뿐이므로, 활성 파일의 부모 디렉토리 하나만 감시하도록 좁힌다.
// (에디터의 atomic-save 가 파일을 rename 으로 교체해도 놓치지 않도록 파일이 아닌
// 부모 디렉토리를 감시하고, 파일명으로 이벤트를 필터링한다.)
type FolderWatcher struct {
	onChange OnChange
	watcher  *fsnotify.Watcher

	mu          sync.Mutex
	activeFile  string // 현재 감시 대상 파일의 절대 경로
	watchedDir  string // activeFile 의 부모 디렉토리(현재 watcher.Add 된 경로)
	debounceTmr *time.Timer
}

// NewFolderWatcher는 FolderWatcher를 생성한다. 생성에 실패하면 nil 을 반환한다.
func NewFolderWatcher(onChange OnChange) *FolderWatcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watcher error: %v\n", err)
		return nil
	}
	return &FolderWatcher{onChange: onChange, watcher: watcher}
}

// SetActive는 감시 대상을 새 활성 파일로 교체한다. 빈 문자열이면 감시를 중단한다.
// 한 번에 최대 하나의 디렉토리만 watcher 에 등록된다.
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
			logging.Warn("[Watcher] 감시 등록 실패 %s: %v", newDir, err)
			fw.watchedDir = ""
		}
	}
	logging.Debug("[Watcher] 감시 대상 교체: %s", absPath)
}

// Run은 이벤트 루프를 실행한다(블로킹).
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
			// 파일이 방금 삭제되었을 수 있음. 조용히 무시한다.
			logging.Debug("[Watcher] 변경 파일 읽기 생략(삭제 추정): %s", active)
			return
		}
		logging.Debug("[Watcher] 파일 변경 감지: %s (%d bytes)", active, len(content))
		fw.onChange(active, content)
	})
	fw.mu.Unlock()
}
