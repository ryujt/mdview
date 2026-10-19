package mdview

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryujt/mdview/internal/mdview/logging"
)

// audioSniffLen은 컨테이너 판별에 읽는 선두 바이트 수이다.
// WAV("RIFF....WAVE")가 12바이트를 요구하므로 여유 있게 잡는다.
const audioSniffLen = 16

// audioFormat은 선두 바이트로 판별한 오디오 컨테이너이다.
type audioFormat struct {
	Name string // 사람이 읽는 이름 (WebM, OGG …)
	Ext  string // 전사 API 업로드 시 사용할 확장자
}

// sniffAudioFormat은 파일 선두 바이트로 오디오 컨테이너를 판별한다.
// 전사 API(OpenAI)는 확장자와 무관하게 내용을 검사하므로, 헤더가 없는 데이터는
// "Invalid file format"으로 거부된다. 그 사실을 업로드 전에 미리 알아내는 용도다.
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
		len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0: // MPEG 오디오 프레임 sync
		return audioFormat{"MP3", ".mp3"}, true
	}
	return audioFormat{}, false
}

// readAudioHead는 파일 선두 바이트와 전체 크기를 읽는다.
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

// validateRecordingAudio는 녹화 오디오가 전사 가능한 컨테이너인지 검사하고,
// 아니면 원인과 크기·선두 바이트를 담은 한국어 오류를 반환한다.
// 전사 API의 불친절한 "Invalid file format" 대신 이 메시지가 사용자에게 보인다.
func validateRecordingAudio(path string) error {
	head, size, err := readAudioHead(path)
	if err != nil {
		return fmt.Errorf("오디오 파일을 읽을 수 없습니다 (%s): %w", filepath.Base(path), err)
	}
	name := filepath.Base(path)
	if size == 0 {
		return fmt.Errorf("오디오 파일이 비어 있습니다 (%s, 0 bytes). "+
			"마이크 입력이 전혀 없었을 가능성이 큽니다 — 입력 장치와 브라우저 마이크 권한을 확인하세요", name)
	}
	if _, ok := sniffAudioFormat(head); !ok {
		return fmt.Errorf("오디오 파일 형식을 인식할 수 없습니다 (%s, %d bytes, 선두 바이트: %s). "+
			"WebM/OGG/MP4/MP3/WAV 헤더가 없어 전사 API가 거부합니다 — 입력 장치를 바꿔 다시 녹음해 보세요",
			name, size, hexDump(head))
	}
	return nil
}

// transcribeUploadName은 전사 API에 보낼 파일 이름을 정한다.
// 저장 확장자는 브라우저가 보낸 MIME으로 정해지므로 실제 내용과 다를 수 있다.
// 내용으로 판별한 확장자가 있으면 그것을 우선한다.
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

// warnIfAudioUnusable은 업로드 직후 오디오 컨테이너를 검사해 문제가 있으면 경고만 남긴다.
// 저장 자체는 성공이므로 요청을 실패시키지 않는다. 자동 전사가 꺼져 있어도
// 터미널에서 즉시 원인을 볼 수 있게 하는 것이 목적이다.
func warnIfAudioUnusable(path string) {
	if err := validateRecordingAudio(path); err != nil {
		logging.Warn("[Recording] 오디오 검증 경고: %v", err)
		fmt.Fprintf(os.Stderr, "경고: 저장된 오디오를 전사할 수 없을 수 있습니다 — %v\n", err)
	}
}
