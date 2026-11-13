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
	title := "mdview recording saved"
	message := "Saved prompt.txt. No transcript document was created."
	if job.Transcript != "" {
		data, err := os.ReadFile(job.Transcript)
		if err == nil {
			err = c.copyText(data)
		}
		if err != nil {
			job.ClipboardError = "failed to auto-copy transcript.json: " + err.Error()
			logging.Warn("[Recording] %s", job.ClipboardError)
			message = "Saved transcript.json, but automatic copy failed. Please copy it again from the page."
		} else {
			job.ClipboardCopied = true
			message = "Copied transcript.json to the clipboard. Paste it where you need it."
		}
	} else if job.State == TranscribeStateError {
		title = "mdview recording post-processing failed"
		message = "Please check the saved recording."
	}
	if err := c.notify(title, message); err != nil {
		job.NotificationError = "system notification failed: " + err.Error()
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
		// Read the UTF-8 JSON as-is to preserve non-ASCII text such as Korean and emoji.
		return runDesktopCommand(data, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			`[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false); Set-Clipboard -Value ([Console]::In.ReadToEnd())`)
	default:
		return fmt.Errorf("clipboard not supported on OS: %s", runtime.GOOS)
	}
}

func notifyDesktop(title, message string) error {
	switch runtime.GOOS {
	case osDarwin:
		// Pass the message via argv instead of embedding it in the script.
		return runDesktopCommand(nil, "osascript", "-e", `on run argv
display notification (item 2 of argv) with title (item 1 of argv)
end run`, title, message)
	case osLinux:
		return runDesktopCommand(nil, "notify-send", title, message)
	default:
		return fmt.Errorf("system notifications not supported on OS: %s", runtime.GOOS)
	}
}
