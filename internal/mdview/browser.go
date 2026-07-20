package mdview

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

const (
	osDarwin  = "darwin"
	osLinux   = "linux"
	osWindows = "windows"

	cmdDarwin  = "open"
	cmdLinux   = "xdg-open"
	cmdWindows = "cmd"
)

// OpenBrowser는 OS에 맞는 기본 브라우저로 URL을 연다.
// Orchestrator가 호출하는 Gateway이다.
func OpenBrowser(url string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case osDarwin:
		cmd = exec.Command(cmdDarwin, url)
	case osLinux:
		cmd = exec.Command(cmdLinux, url)
	case osWindows:
		cmd = exec.Command(cmdWindows, "/c", "start", url)
	default:
		fmt.Fprintf(os.Stderr, "unsupported OS: %s\n", runtime.GOOS)
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to open browser: %v\n", err)
	}
}
