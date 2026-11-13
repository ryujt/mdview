package mdview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

const (
	recordingRetention       = 7 * 24 * time.Hour
	recordingCleanupInterval = time.Hour
)

var recordingDirPattern = regexp.MustCompile(`^rec-[0-9]{8}-[0-9]{6}(-[0-9]+)?$`)

// recordingBaseDir is the per-user app data location, independent of the working folder.
func recordingBaseDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine recording storage location: %w", err)
	}
	return filepath.Join(base, configDirName), nil
}

func (s *Server) runRecordingRetention(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			s.pruneRecordings(now)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) pruneRecordings(now time.Time) {
	base, err := recordingBaseDir()
	if err != nil {
		logging.Warn("[Recording] expired recording cleanup failed: %v", err)
		return
	}
	// Recordings still being post-processed are not pruned. State updates use the same lock.
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	active := make(map[string]bool)
	for id, job := range s.transcribeJobs {
		active[id] = job.State == TranscribeStateRunning
	}
	removed, err := pruneExpiredRecordings(base, now, active)
	if err != nil {
		logging.Warn("[Recording] expired recording cleanup failed: %v", err)
	}
	if removed > 0 {
		logging.Info("[Recording] deleted %d recordings older than 7 days", removed)
	}
}

// pruneExpiredRecordings prunes only recordings in the dedicated store, based on savedAt.
// Recordings that expired while the app was not in use are deleted on the next run.
func pruneExpiredRecordings(base string, now time.Time, active map[string]bool) (int, error) {
	if now.IsZero() {
		return 0, errors.New("recording retention requires a nonzero current time")
	}
	dir := filepath.Join(base, recordingsDirName)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("recordings root must be a directory, not a symbolic link")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-recordingRetention)
	var failures []error
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !recordingDirPattern.MatchString(name) || active[name] {
			continue
		}
		owned, err := root.OpenRoot(name)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		savedAt, err := recordingSavedAt(owned)
		owned.Close()
		// Never delete unidentifiable directories or incomplete saves.
		if err != nil || savedAt.IsZero() || savedAt.After(cutoff) {
			continue
		}
		if err := root.RemoveAll(name); err != nil {
			failures = append(failures, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(failures...)
}

func recordingSavedAt(root *os.Root) (time.Time, error) {
	info, err := root.Lstat(eventsFileName)
	if err != nil {
		return time.Time{}, err
	}
	if !info.Mode().IsRegular() {
		return time.Time{}, errors.New("invalid recording metadata")
	}
	file, err := root.Open(eventsFileName)
	if err != nil {
		return time.Time{}, err
	}
	defer file.Close()
	var meta struct {
		SavedAt string `json:"savedAt"`
		Audio   string `json:"audio"`
	}
	if err := json.NewDecoder(io.LimitReader(file, maxRecordingBytes)).Decode(&meta); err != nil {
		return time.Time{}, err
	}
	switch meta.Audio {
	case "audio.webm", "audio.ogg", "audio.m4a", "audio.mp3":
	default:
		return time.Time{}, errors.New("invalid recording audio name")
	}
	return time.Parse(time.RFC3339, meta.SavedAt)
}
