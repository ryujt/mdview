package mdview

import (
	"path/filepath"
	"testing"
)

// Isolate both Unix and Windows user directories, including recording storage.
func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
}
