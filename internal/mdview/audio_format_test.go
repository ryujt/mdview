package mdview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSniffAudioFormat(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		ext  string
		ok   bool
	}{
		{"webm", []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42}, ".webm", true},
		{"ogg", []byte("OggS\x00\x02"), ".ogg", true},
		{"mp4", []byte("\x00\x00\x00\x1cftypisom"), ".m4a", true},
		{"wav", []byte("RIFF\x24\x08\x00\x00WAVEfmt "), ".wav", true},
		{"flac", []byte("fLaC\x00"), ".flac", true},
		{"mp3-id3", []byte("ID3\x03"), ".mp3", true},
		{"mp3-sync", []byte{0xFF, 0xFB, 0x90}, ".mp3", true},
		{"empty", nil, "", false},
		{"garbage", []byte("FAKEAUDIO"), "", false},
		{"short-ftyp", []byte("\x00\x00ft"), "", false},
	}
	for _, c := range cases {
		format, ok := sniffAudioFormat(c.head)
		if ok != c.ok || format.Ext != c.ext {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, format.Ext, ok, c.ext, c.ok)
		}
	}
}

func TestValidateRecordingAudio(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if err := validateRecordingAudio(write("ok.webm", []byte{0x1A, 0x45, 0xDF, 0xA3, 1, 2, 3})); err != nil {
		t.Errorf("정상 WebM이 거부됨: %v", err)
	}
	err := validateRecordingAudio(write("empty.webm", nil))
	if err == nil || !strings.Contains(err.Error(), "비어 있습니다") {
		t.Errorf("빈 파일 오류가 아님: %v", err)
	}
	err = validateRecordingAudio(write("bad.webm", []byte("FAKEAUDIO")))
	if err == nil || !strings.Contains(err.Error(), "형식을 인식할 수 없습니다") ||
		!strings.Contains(err.Error(), "9 bytes") || !strings.Contains(err.Error(), "46 41 4b 45") {
		t.Errorf("인식 불가 오류에 크기·선두 바이트가 없음: %v", err)
	}
	if err := validateRecordingAudio(filepath.Join(dir, "missing.webm")); err == nil {
		t.Error("없는 파일이 통과됨")
	}
}

func TestTranscribeUploadName(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// 내용과 확장자가 맞으면 그대로
	if got := transcribeUploadName(write("audio.webm", []byte{0x1A, 0x45, 0xDF, 0xA3})); got != "audio.webm" {
		t.Errorf("got %q, want audio.webm", got)
	}
	// 내용이 OGG인데 .webm으로 저장됐으면 내용을 우선
	if got := transcribeUploadName(write("mismatch.webm", []byte("OggS\x00"))); got != "mismatch.ogg" {
		t.Errorf("got %q, want mismatch.ogg", got)
	}
	// 인식 불가면 원래 이름 유지
	if got := transcribeUploadName(write("unknown.webm", []byte("FAKE"))); got != "unknown.webm" {
		t.Errorf("got %q, want unknown.webm", got)
	}
}
