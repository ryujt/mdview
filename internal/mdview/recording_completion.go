package mdview

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

const desktopCommandTimeout = 5 * time.Second

type recordingCompletion struct {
	copyText func([]byte) error
	notify   func(string, string) error
}

func newRecordingCompletion() *recordingCompletion {
	return &recordingCompletion{copyText: copyDesktopText, notify: notifyDesktop}
}

func (c *recordingCompletion) complete(job *TranscribeJob) {
	title := "mdview 녹음 저장 완료"
	message := "prompt.txt를 저장했습니다. 전사 문서는 생성되지 않았습니다."
	if job.Transcript != "" {
		data, err := os.ReadFile(job.Transcript)
		if err == nil {
			err = c.copyText(data)
		}
		if err != nil {
			job.ClipboardError = "transcript.json 자동 복사 실패: " + err.Error()
			logging.Warn("[Recording] %s", job.ClipboardError)
			message = "transcript.json을 저장했습니다. 자동 복사에 실패해 화면에서 다시 복사해주세요."
		} else {
			job.ClipboardCopied = true
			message = "transcript.json 내용을 클립보드에 복사했습니다. 붙여 넣어 사용하세요."
		}
	} else if job.State == TranscribeStateError {
		title = "mdview 녹음 후처리 실패"
		message = "저장된 녹음을 확인해주세요."
	}
	if err := c.notify(title, message); err != nil {
		job.NotificationError = "시스템 알림 실패: " + err.Error()
		logging.Warn("[Recording] %s", job.NotificationError)
	}
}

func runDesktopCommand(input []byte, command string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), desktopCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}

func copyDesktopText(data []byte) error {
	switch runtime.GOOS {
	case osDarwin:
		return runDesktopCommand(data, "pbcopy")
	case osLinux:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if err := runDesktopCommand(data, "wl-copy"); err == nil {
				return nil
			}
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return runDesktopCommand(data, "xclip", "-selection", "clipboard")
		}
		return runDesktopCommand(data, "xsel", "--clipboard", "--input")
	case osWindows:
		// UTF-8 JSON을 그대로 읽어 한글과 이모지를 보존한다.
		return runDesktopCommand(data, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			`[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false); Set-Clipboard -Value ([Console]::In.ReadToEnd())`)
	default:
		return fmt.Errorf("클립보드를 지원하지 않는 OS: %s", runtime.GOOS)
	}
}

func notifyDesktop(title, message string) error {
	switch runtime.GOOS {
	case osDarwin:
		// 메시지를 스크립트에 삽입하지 않고 argv로 전달한다.
		return runDesktopCommand(nil, "osascript", "-e", `on run argv
display notification (item 2 of argv) with title (item 1 of argv)
end run`, title, message)
	case osLinux:
		return runDesktopCommand(nil, "notify-send", title, message)
	default:
		return fmt.Errorf("시스템 알림을 지원하지 않는 OS: %s", runtime.GOOS)
	}
}
