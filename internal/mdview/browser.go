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

// OpenBrowser opens the URL in the OS default browser.
// It is a Gateway called by the Orchestrator.
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
