package mdview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func retentionRecording(t *testing.T, base string, saved time.Time) string {
	t.Helper()
	dir, _, err := SaveRecording(base, strings.NewReader("audio"), "audio/webm", map[string]any{"version": 1}, saved)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPruneExpiredRecordings(t *testing.T) {
	base := t.TempDir()
	now := time.Now().Truncate(time.Second)
	cutoff := now.Add(-recordingRetention)
	exact := retentionRecording(t, base, cutoff)
	older := retentionRecording(t, base, cutoff.Add(-time.Hour))
	fresh := retentionRecording(t, base, cutoff.Add(time.Second))
	active := retentionRecording(t, base, cutoff.Add(-2*time.Hour))
	// Reading or writing new files does not extend the 7-day retention from the initial save.
	writeFixture(t, older, promptFileName, "recently regenerated prompt")
	if err := os.Chtimes(exact, now, now); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(base, recordingsDirName, "personal-folder")
	if err := os.Mkdir(foreign, recordingDirPerm); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(base, recordingsDirName, "rec-20200101-000000")
	if err := os.Mkdir(invalid, recordingDirPerm); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, invalid, eventsFileName, "invalid JSON")
	removed, err := pruneExpiredRecordings(base, now, map[string]bool{filepath.Base(active): true})
	if err != nil || removed != 2 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	for _, dir := range []string{exact, older} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("expired recording still present: %s (%v)", dir, err)
		}
	}
	for _, dir := range []string{fresh, active, foreign, invalid} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("directory that should be kept was removed: %s (%v)", dir, err)
		}
	}
	if removed, err := pruneExpiredRecordings(base, now, nil); err != nil || removed != 1 {
		t.Fatalf("post-processed recording was not pruned: %d, %v", removed, err)
	}
}

func TestPruneExpiredRecordingsDoesNotFollowSymlinks(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	externalBase := t.TempDir()
	external := retentionRecording(t, externalBase, now.Add(-8*24*time.Hour))
	local := retentionRecording(t, base, now.Add(-9*24*time.Hour))
	if err := os.Symlink(external, filepath.Join(base, recordingsDirName, filepath.Base(external))); err != nil {
		t.Fatal(err)
	}
	// A symlink inside a recording must not cause its target directory to be deleted.
	if err := os.Symlink(external, filepath.Join(local, "linked")); err != nil {
		t.Fatal(err)
	}
	if removed, err := pruneExpiredRecordings(base, now, nil); err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatalf("external recording was deleted: %v", err)
	}
	linkedBase := t.TempDir()
	if err := os.Symlink(filepath.Join(externalBase, recordingsDirName), filepath.Join(linkedBase, recordingsDirName)); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneExpiredRecordings(linkedBase, now, nil); err == nil {
		t.Fatal("symlinked store was allowed as a pruning target")
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingRetentionTimer(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base, err := recordingBaseDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := retentionRecording(t, base, time.Now().Add(-8*24*time.Hour))
	s := newServer(ModeFolder, NewHub())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.runRecordingRetention(ctx, time.Millisecond); close(done) }()
	deadline := time.After(3 * time.Second)
	for {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("recording that expired while running was not pruned")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup timer did not stop")
	}
}
