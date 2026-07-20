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

// 녹화(오디오 + 이벤트 타임라인) 저장 관련 정책 상수
const (
	recordingsDirName   = "mdview-recordings"
	recordingDirPrefix  = "rec-"
	recordingTimeFormat = "20060102-150405"
	audioFileBase       = "audio"
	eventsFileName      = "events.json"
	defaultAudioExt     = ".webm"

	recordingDirPerm  = 0o755
	recordingFilePerm = 0o644

	// 동일 초에 저장이 겹칠 때 디렉토리 이름 충돌을 피하기 위한 최대 시도 횟수
	maxDirSuffixTries = 100
)

// audioExtByMime는 MediaRecorder가 생성하는 오디오 MIME 타입별 저장 확장자이다.
var audioExtByMime = map[string]string{
	"audio/webm": ".webm",
	"video/webm": ".webm",
	"audio/ogg":  ".ogg",
	"audio/mp4":  ".m4a",
	"audio/mpeg": ".mp3",
}

// AudioExtFromMime는 업로드된 오디오 파트의 Content-Type에서 저장 확장자를 결정한다.
// 코덱 파라미터(예: "audio/webm;codecs=opus")는 무시하며, 알 수 없는 타입은 webm으로 취급한다.
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

// SaveRecording은 오디오 스트림과 이벤트 메타를 디스크에 저장하는 Worker이다.
// 저장 구조: <base>/mdview-recordings/rec-<timestamp>[-N]/{audio.<ext>, events.json}
// meta에는 서버가 결정한 오디오 파일명(audio)과 저장 시각(savedAt)을 주입한다.
func SaveRecording(base string, audio io.Reader, contentType string, meta map[string]any, now time.Time) (dir string, audioName string, err error) {
	dir, err = createRecordingDir(base, now)
	if err != nil {
		return "", "", err
	}

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

// createRecordingDir는 타임스탬프 기반 녹화 디렉토리를 생성한다.
// 이미 존재하면 -2, -3 … 접미사를 붙여 유일한 디렉토리를 확보한다.
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

// summarizeRecordingEvents는 업로드 메타의 이벤트 목록을 로그용 한 줄 요약으로 만든다.
func summarizeRecordingEvents(meta map[string]any) (int, string) {
	const maxLines = 50
	events, ok := meta["events"].([]any)
	if !ok {
		return 0, ""
	}
	var lines []string
	for i, raw := range events {
		if i >= maxLines {
			lines = append(lines, fmt.Sprintf("... 외 %d개", len(events)-maxLines))
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
