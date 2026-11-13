package mdview

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Policy constants for saving recordings (audio + event timeline)
const (
	recordingsDirName   = "mdview-recordings"
	recordingDirPrefix  = "rec-"
	recordingTimeFormat = "20060102-150405"
	audioFileBase       = "audio"
	eventsFileName      = "events.json"
	defaultAudioExt     = ".webm"

	recordingDirPerm  = 0o700
	recordingFilePerm = 0o600

	// Maximum attempts to avoid directory name collisions when saves land in the same second
	maxDirSuffixTries = 100
)

// audioExtByMime maps audio MIME types produced by MediaRecorder to file extensions.
var audioExtByMime = map[string]string{
	"audio/webm": ".webm",
	"video/webm": ".webm",
	"audio/ogg":  ".ogg",
	"audio/mp4":  ".m4a",
	"audio/mpeg": ".mp3",
}

// AudioExtFromMime determines the file extension from the uploaded audio part's Content-Type.
// Codec parameters (e.g. "audio/webm;codecs=opus") are ignored; unknown types are treated as webm.
func AudioExtFromMime(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return defaultAudioExt
	}
	if ext, ok := audioExtByMime[mediaType]; ok {
		return ext
	}
	return defaultAudioExt
}

// SaveRecording is a Worker that saves the audio stream and event metadata to disk.
// Layout: <base>/mdview-recordings/rec-<timestamp>[-N]/{audio.<ext>, events.json}
// The server-chosen audio file name (audio) and save time (savedAt) are injected into meta.
func SaveRecording(base string, audio io.Reader, contentType string, meta map[string]any, now time.Time) (dir string, audioName string, err error) {
	dir, err = createRecordingDir(base, now)
	if err != nil {
		return "", "", err
	}
	createdDir := dir
	defer func() {
		if err != nil {
			_ = os.RemoveAll(createdDir)
		}
	}()

	audioName = audioFileBase + AudioExtFromMime(contentType)
	if err := writeStream(filepath.Join(dir, audioName), audio); err != nil {
		return "", "", fmt.Errorf("audio write error: %w", err)
	}

	meta[jsonKeyAudio] = audioName
	meta[jsonKeySavedAt] = now.Format(time.RFC3339)
	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("meta encode error: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, eventsFileName), encoded, recordingFilePerm); err != nil {
		return "", "", fmt.Errorf("events write error: %w", err)
	}
	return dir, audioName, nil
}

const (
	jsonKeyAudio   = "audio"
	jsonKeySavedAt = "savedAt"
)

// createRecordingDir creates a timestamp-based recording directory.
// If it already exists, a -2, -3, ... suffix is appended to get a unique directory.
func createRecordingDir(base string, now time.Time) (string, error) {
	parent := filepath.Join(base, recordingsDirName)
	if err := os.MkdirAll(parent, recordingDirPerm); err != nil {
		return "", fmt.Errorf("recordings dir error: %w", err)
	}
	stamp := recordingDirPrefix + now.Format(recordingTimeFormat)
	for i := 1; i <= maxDirSuffixTries; i++ {
		name := stamp
		if i > 1 {
			name = fmt.Sprintf("%s-%d", stamp, i)
		}
		dir := filepath.Join(parent, name)
		err := os.Mkdir(dir, recordingDirPerm)
		if err == nil {
			return dir, nil
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("recording dir error: %w", err)
		}
	}
	return "", fmt.Errorf("recording dir error: too many recordings at %s", stamp)
}

// summarizeRecordingEvents turns the event list in the upload meta into one-line summaries for logging.
func summarizeRecordingEvents(meta map[string]any) (int, string) {
	const maxLines = 50
	events, ok := meta["events"].([]any)
	if !ok {
		return 0, ""
	}
	var lines []string
	for i, raw := range events {
		if i >= maxLines {
			lines = append(lines, fmt.Sprintf("... and %d more", len(events)-maxLines))
			break
		}
		e, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		line := fmt.Sprintf("t=%v %v %v", e["t"], e["type"], e["file"])
		if l, ok := e["line"]; ok {
			line += fmt.Sprintf(":%v", l)
		}
		lines = append(lines, line)
	}
	return len(events), strings.Join(lines, "\n")
}

func writeStream(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, recordingFilePerm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
