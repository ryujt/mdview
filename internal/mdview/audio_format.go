package mdview

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

// audioSniffLen is the number of leading bytes read to detect the container.
// WAV ("RIFF....WAVE") needs 12 bytes, so this leaves some headroom.
const audioSniffLen = 16

// audioFormat is an audio container detected from the leading bytes.
type audioFormat struct {
	Name string // human-readable name (WebM, OGG …)
	Ext  string // extension to use when uploading to the transcription API
}

// sniffAudioFormat detects the audio container from a file's leading bytes.
// The transcription API (OpenAI) inspects the content regardless of extension, so data without
// a header is rejected with "Invalid file format". This detects that before uploading.
func sniffAudioFormat(head []byte) (audioFormat, bool) {
	switch {
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}): // EBML (WebM/Matroska)
		return audioFormat{"WebM", ".webm"}, true
	case bytes.HasPrefix(head, []byte("OggS")):
		return audioFormat{"OGG", ".ogg"}, true
	case len(head) >= 8 && string(head[4:8]) == "ftyp": // ISO BMFF (MP4/M4A)
		return audioFormat{"MP4", ".m4a"}, true
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return audioFormat{"WAV", ".wav"}, true
	case bytes.HasPrefix(head, []byte("fLaC")):
		return audioFormat{"FLAC", ".flac"}, true
	case bytes.HasPrefix(head, []byte("ID3")),
		len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0: // MPEG audio frame sync
		return audioFormat{"MP3", ".mp3"}, true
	}
	return audioFormat{}, false
}

// readAudioHead reads a file's leading bytes and total size.
func readAudioHead(path string) (head []byte, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	buf := make([]byte, audioSniffLen)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, 0, err
	}
	return buf[:n], fi.Size(), nil
}

// validateRecordingAudio checks whether the recorded audio is in a transcribable container,
// and if not, returns an error describing the cause along with the size and leading bytes.
// Users see this message instead of the transcription API's unhelpful "Invalid file format".
func validateRecordingAudio(path string) error {
	head, size, err := readAudioHead(path)
	if err != nil {
		return fmt.Errorf("cannot read audio file (%s): %w", filepath.Base(path), err)
	}
	name := filepath.Base(path)
	if size == 0 {
		return fmt.Errorf("audio file is empty (%s, 0 bytes). "+
			"There was most likely no microphone input at all — check the input device and the browser's microphone permission", name)
	}
	if _, ok := sniffAudioFormat(head); !ok {
		return fmt.Errorf("unrecognized audio file format (%s, %d bytes, leading bytes: %s). "+
			"The transcription API will reject it because it has no WebM/OGG/MP4/MP3/WAV header — try recording again with a different input device",
			name, size, hexDump(head))
	}
	return nil
}

// transcribeUploadName decides the file name sent to the transcription API.
// The saved extension comes from the MIME type the browser sent, so it may not match the actual content.
// An extension detected from the content takes precedence.
func transcribeUploadName(path string) string {
	name := filepath.Base(path)
	head, _, err := readAudioHead(path)
	if err != nil {
		return name
	}
	format, ok := sniffAudioFormat(head)
	if !ok || strings.EqualFold(filepath.Ext(name), format.Ext) {
		return name
	}
	return strings.TrimSuffix(name, filepath.Ext(name)) + format.Ext
}

func hexDump(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("%02x", c)
	}
	return strings.Join(parts, " ")
}

// warnIfAudioUnusable checks the audio container right after upload and only logs a warning on problems.
// The save itself succeeded, so the request is not failed. The goal is to surface the cause
// in the terminal immediately, even when automatic transcription is off.
func warnIfAudioUnusable(path string) {
	if err := validateRecordingAudio(path); err != nil {
		logging.Warn("[Recording] audio validation warning: %v", err)
		fmt.Fprintf(os.Stderr, "warning: the saved audio may not be transcribable — %v\n", err)
	}
}
