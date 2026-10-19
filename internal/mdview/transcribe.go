package mdview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ryujt/mdview/internal/mdview/logging"
)

// 전사(오디오 → 텍스트) 관련 정책 상수.
// 기본 백엔드는 OpenAI Audio Transcriptions API의 whisper-1 모델이며
// (설정 파일 또는 OPENAI_API_KEY의 API 키 필요), verbose_json 응답으로
// 세그먼트 타임스탬프를 받아 이벤트 타임라인과 병합한다.
// -model large-v3 등 다른 이름을 주면 로컬 openai-whisper CLI로 실행한다.
const (
	DefaultTranscribeModel = "whisper-1" // OpenAI API 모델
	localWhisperDefault    = "large-v3"  // 로컬 CLI 권장 모델
	whisperModelCanonical  = "openai/whisper-large-v3"
	whisperCmdName         = "whisper"
	transcriptFileName     = "transcript.json"

	// OpenAI Audio API 요청 정책
	openaiRequestTimeout = 10 * time.Minute
	openaiErrorBodyLimit = 500
	maxAPIAudioBytes     = 25 << 20 // OpenAI Audio API 업로드 상한 (25MB)
)

// openaiAPIBase는 OpenAI API 엔드포인트이다 (테스트에서 교체).
var openaiAPIBase = "https://api.openai.com/v1"

// whisperInstallGuide는 whisper CLI 부재 시 안내 문구이다.
const whisperInstallGuide = `whisper CLI를 찾을 수 없습니다.
설치 방법:
  pip install -U openai-whisper   # openai/whisper-large-v3 모델 사용
  brew install ffmpeg             # 오디오 디코딩에 필요 (macOS)`

// TranscriptSegment는 Whisper가 출력한 발화 구간이다 (초 단위).
type TranscriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// WhisperResult는 whisper --output_format json 결과에서 사용하는 필드이다.
type WhisperResult struct {
	Text     string              `json:"text"`
	Language string              `json:"language"`
	Segments []TranscriptSegment `json:"segments"`
}

// RecordingEvent는 events.json에 기록된 단일 이벤트이다.
type RecordingEvent struct {
	T         int64   `json:"t"`
	Type      string  `json:"type"`
	File      string  `json:"file,omitempty"`
	Line      int     `json:"line,omitempty"`
	LineEnd   int     `json:"lineEnd,omitempty"`
	Text      string  `json:"text,omitempty"`
	Selection string  `json:"selection,omitempty"`
	Approx    bool    `json:"approx,omitempty"`
	PDF       *PDFRef `json:"pdf,omitempty"`
	// 웹 페이지 조작(web-*) 전용 필드. 문서 이벤트에서는 비어 있다.
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	Selector string `json:"selector,omitempty"`
	Tag      string `json:"tag,omitempty"`
	ID       string `json:"id,omitempty"`
	Classes  string `json:"classes,omitempty"`
	Heading  string `json:"heading,omitempty"`
	Context  string `json:"context,omitempty"`
}

// PDFRef는 PDF 문서 내 참조 위치이다. 라인 번호가 없는 PDF는 페이지 번호와
// 페이지 좌상단 기준 0~1 정규화 좌표(x, y)로 위치를 표현한다.
// 좌표가 없는 이벤트(선택/복사)는 페이지 번호만 기록되므로 X/Y는 포인터로 구분한다.
type PDFRef struct {
	Page int      `json:"page"`
	X    *float64 `json:"x,omitempty"`
	Y    *float64 `json:"y,omitempty"`
}

// RecordingMeta는 events.json 전체 구조이다.
type RecordingMeta struct {
	Version    int    `json:"version"`
	Mode       string `json:"mode"`
	Root       string `json:"root,omitempty"`
	File       string `json:"file,omitempty"`
	StartedAt  string `json:"startedAt"`
	SavedAt    string `json:"savedAt"`
	DurationMs int64  `json:"durationMs"`
	Audio      string `json:"audio"`
	// DroppedWebEvents는 검증·상한으로 폐기된 웹 이벤트 수이다.
	DroppedWebEvents int              `json:"droppedWebEvents,omitempty"`
	Events           []RecordingEvent `json:"events"`
}

// TranscribeRecording은 녹화 디렉토리의 오디오를 전사해 transcript.json을 생성하는
// Orchestrator이다. 이미 whisper JSON 결과가 있으면 재사용한다 (모델 재실행 비용 절약).
// 프로세스 이벤트는 이 함수(오케스트레이터)에서만 기록하고, 워커는 결과만 반환한다.
func TranscribeRecording(dir, model, lang string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	audioPath, err := findRecordingAudio(absDir)
	if err != nil {
		logging.LogProcessEvent("execution", "step_err", "오디오 파일 탐색 실패", nil)
		return "", err
	}
	logging.Info("[Transcribe] 대상 오디오: %s", audioPath)
	logging.LogProcessEvent("execution", "step_ok", "오디오 파일 탐색: "+filepath.Base(audioPath), nil)

	resultPath := whisperResultPath(absDir, audioPath)
	if _, err := os.Stat(resultPath); os.IsNotExist(err) {
		started := time.Now()
		if err := validateRecordingAudio(audioPath); err != nil {
			logging.LogProcessEvent("execution", "step_err", "오디오 형식 검증 실패: "+err.Error(), nil)
			return "", err
		}
		if isAPIModel(model) {
			apiKey, err := ResolveOpenAIKey()
			if err != nil {
				logging.LogProcessEvent("execution", "step_err", "API 키 없음", nil)
				return "", err
			}
			artifact, err := runWhisperAPI(resultPath, audioPath, model, lang, apiKey)
			elapsed := float64(time.Since(started).Milliseconds())
			if err != nil {
				logging.LogProcessEvent("execution", "step_err", "whisper-1 API 전사 실패",
					&logging.EventOpts{ArtifactPath: artifact, DurationMs: elapsed})
				return "", err
			}
			logging.LogProcessEvent("execution", "step_ok",
				fmt.Sprintf("whisper-1 API 전사 (%.1fs)", elapsed/1000),
				&logging.EventOpts{ArtifactPath: artifact, DurationMs: elapsed})
		} else {
			artifact, err := runWhisper(absDir, audioPath, model, lang)
			elapsed := float64(time.Since(started).Milliseconds())
			if err != nil {
				logging.LogProcessEvent("execution", "step_err", "로컬 whisper 전사 실패",
					&logging.EventOpts{ArtifactPath: artifact, DurationMs: elapsed})
				return "", err
			}
			logging.LogProcessEvent("execution", "step_ok",
				fmt.Sprintf("로컬 whisper 전사 (%.1fs)", elapsed/1000),
				&logging.EventOpts{ArtifactPath: artifact, DurationMs: elapsed})
		}
	} else {
		fmt.Printf("기존 전사 결과 재사용: %s\n", resultPath)
		logging.Info("[Transcribe] 기존 전사 결과 재사용: %s", resultPath)
		logging.LogProcessEvent("execution", "step_ok", "기존 전사 결과 재사용", nil)
	}

	result, err := loadWhisperResult(resultPath)
	if err != nil {
		logging.LogProcessEvent("execution", "step_err", "전사 결과 파싱 실패", nil)
		return "", err
	}
	meta, err := loadRecordingMeta(absDir)
	if err != nil {
		// 이벤트 메타가 없어도 전사 문서는 생성한다.
		fmt.Fprintf(os.Stderr, "경고: %s 없이 전사만 수행합니다 (%v)\n", eventsFileName, err)
		logging.Warn("[Transcribe] %s 없음: %v", eventsFileName, err)
		meta = &RecordingMeta{}
	}

	doc, err := buildTranscriptJSON(meta, result, model)
	if err != nil {
		logging.LogProcessEvent("synthesis", "error", "transcript.json 생성 실패", nil)
		return "", fmt.Errorf("transcript build error: %w", err)
	}
	outPath := filepath.Join(absDir, transcriptFileName)
	if err := os.WriteFile(outPath, doc, recordingFilePerm); err != nil {
		logging.LogProcessEvent("synthesis", "error", "transcript.json 저장 실패", nil)
		return "", fmt.Errorf("transcript write error: %w", err)
	}
	statePath := logging.LogStateSnapshot("transcribe-meta", map[string]any{
		"dir": absDir, "audio": filepath.Base(audioPath), "language": result.Language,
		"segments": len(result.Segments), "events": len(meta.Events), "durationMs": meta.DurationMs,
	})
	logging.Info("[Transcribe] 문서 생성: %s (발화 %d개, 이벤트 %d개)", outPath, len(result.Segments), len(meta.Events))
	logging.LogProcessEvent("synthesis", "end",
		fmt.Sprintf("타임라인 병합: 발화 %d개 + 이벤트 %d개", len(result.Segments), len(meta.Events)),
		&logging.EventOpts{ArtifactPath: statePath})
	return outPath, nil
}

// TranscribeSkipReason은 자동 전사를 생략해야 하는 이유를 반환한다 (빈 문자열이면 전사 가능).
// 이미 whisper 결과가 있으면 키·크기와 무관하게 재사용할 수 있고, 로컬 CLI 모델은 API 제약을 받지 않는다.
func TranscribeSkipReason(dir, model string) string {
	audioPath, err := findRecordingAudio(dir)
	if err != nil {
		return "오디오 파일이 없습니다"
	}
	if _, err := os.Stat(whisperResultPath(dir, audioPath)); err == nil {
		return ""
	}
	if !isAPIModel(model) {
		return ""
	}
	if _, err := ResolveOpenAIKey(); err != nil {
		if !errors.Is(err, ErrNoOpenAIKey) {
			return "OpenAI API 키를 읽을 수 없습니다: " + err.Error()
		}
		return ErrNoOpenAIKey.Error()
	}
	if size := fileSize(audioPath); size > maxAPIAudioBytes {
		return fmt.Sprintf("오디오가 %dMB로 API 업로드 상한(%dMB)을 초과합니다", size>>20, maxAPIAudioBytes>>20)
	}
	return ""
}

// findRecordingAudio는 녹화 디렉토리에서 오디오 파일(audio.*)을 찾는다.
func findRecordingAudio(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("recording dir read error: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, audioFileBase+".") && name != whisperResultName() {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("오디오 파일(%s.*)을 찾을 수 없습니다: %s", audioFileBase, dir)
}

// whisperResultName은 whisper가 생성하는 JSON 결과 파일 이름이다 (<audio 베이스>.json).
func whisperResultName() string {
	return audioFileBase + ".json"
}

func whisperResultPath(dir, audioPath string) string {
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	return filepath.Join(dir, base+".json")
}

// isAPIModel은 OpenAI Audio API로 전사할 모델인지 반환한다.
// whisper-1만 세그먼트 타임스탬프(verbose_json)를 지원한다.
func isAPIModel(model string) bool {
	return model == DefaultTranscribeModel
}

// runWhisperAPI는 OpenAI Audio Transcriptions API로 전사를 실행하고
// verbose_json 응답(세그먼트 start/end 타임스탬프 포함)을 결과 파일에 저장한다.
// 반환값: whisper/ 카테고리에 저장된 상세 로그 아티팩트 경로 (없으면 빈 문자열).
func runWhisperAPI(resultPath, audioPath, model, lang, apiKey string) (string, error) {
	fmt.Printf("전사 실행: %s (%s)\n", modelDisplayName(model), filepath.Base(audioPath))
	started := time.Now()
	audioBytes := fileSize(audioPath)
	uploadName := transcribeUploadName(audioPath)
	requestSummary := fmt.Sprintf(
		"POST %s/audio/transcriptions\nmodel=%s\nresponse_format=verbose_json\ntimestamp_granularities[]=segment\nlanguage=%s\nfile=%s (%d bytes)",
		openaiAPIBase, model, orDefault(lang, "(자동 감지)"), uploadName, audioBytes)

	// 실패 시에도 상세 로그를 남기는 헬퍼
	fail := func(err error) (string, error) {
		artifact := logging.LogWhisperCall("openai-api", model, lang, filepath.Base(audioPath),
			audioBytes, requestSummary, "", "", float64(time.Since(started).Milliseconds()), err.Error())
		return artifact, err
	}

	audio, err := os.Open(audioPath)
	if err != nil {
		return fail(fmt.Errorf("오디오 열기 실패: %w", err))
	}
	defer audio.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", uploadName)
	if err != nil {
		return fail(err)
	}
	if _, err := io.Copy(part, audio); err != nil {
		return fail(fmt.Errorf("오디오 읽기 실패: %w", err))
	}
	_ = mw.WriteField("model", model)
	_ = mw.WriteField("response_format", "verbose_json")
	_ = mw.WriteField("timestamp_granularities[]", "segment")
	if lang != "" {
		_ = mw.WriteField("language", lang)
	}
	if err := mw.Close(); err != nil {
		return fail(err)
	}

	req, err := http.NewRequest(http.MethodPost, openaiAPIBase+"/audio/transcriptions", &buf)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: openaiRequestTimeout}
	res, err := client.Do(req)
	if err != nil {
		return fail(fmt.Errorf("OpenAI API 요청 실패: %w", err))
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fail(fmt.Errorf("OpenAI API 응답 읽기 실패: %w", err))
	}
	if res.StatusCode != http.StatusOK {
		detail := string(body)
		if len(detail) > openaiErrorBodyLimit {
			detail = detail[:openaiErrorBodyLimit] + "…"
		}
		return fail(fmt.Errorf("OpenAI API 오류 (HTTP %d): %s", res.StatusCode, detail))
	}
	// 유효성 검사를 겸해 파싱한 뒤 원본 응답을 그대로 저장한다 (로컬 whisper 결과와 동일 취급).
	var check WhisperResult
	if err := json.Unmarshal(body, &check); err != nil {
		return fail(fmt.Errorf("OpenAI API 응답 파싱 실패: %w", err))
	}
	if err := os.WriteFile(resultPath, body, recordingFilePerm); err != nil {
		return fail(fmt.Errorf("전사 결과 저장 실패: %w", err))
	}

	elapsed := float64(time.Since(started).Milliseconds())
	responseSummary := fmt.Sprintf("language=%s\nsegments=%d\nresult=%s",
		check.Language, len(check.Segments), filepath.Base(resultPath))
	artifact := logging.LogWhisperCall("openai-api", model, lang, filepath.Base(audioPath),
		audioBytes, requestSummary, responseSummary, strings.TrimSpace(check.Text), elapsed, "")
	return artifact, nil
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// runWhisper는 openai-whisper CLI로 전사를 실행한다 (블로킹, 진행 로그 passthrough).
// 반환값: whisper/ 카테고리에 저장된 상세 로그 아티팩트 경로 (없으면 빈 문자열).
func runWhisper(dir, audioPath, model, lang string) (string, error) {
	started := time.Now()
	audioBytes := fileSize(audioPath)

	logCall := func(cmdline string, errMsg string) string {
		return logging.LogWhisperCall("local-cli", model, lang, filepath.Base(audioPath),
			audioBytes, cmdline, "", "", float64(time.Since(started).Milliseconds()), errMsg)
	}

	bin, err := exec.LookPath(whisperCmdName)
	if err != nil {
		installErr := fmt.Errorf("%s", whisperInstallGuide)
		return logCall(whisperCmdName+" (미설치)", installErr.Error()), installErr
	}
	args := []string{
		audioPath,
		"--model", model,
		"--task", "transcribe",
		"--output_format", "json",
		"--output_dir", dir,
	}
	if lang != "" {
		args = append(args, "--language", lang)
	}
	cmdline := bin + " " + strings.Join(args, " ")
	fmt.Printf("전사 실행: %s (%s)\n", modelDisplayName(model), filepath.Base(audioPath))
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		runErr := fmt.Errorf("whisper 실행 실패: %w", err)
		return logCall(cmdline, runErr.Error()), runErr
	}
	return logCall(cmdline, ""), nil
}

func loadWhisperResult(path string) (*WhisperResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("whisper 결과 읽기 실패: %w", err)
	}
	var result WhisperResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("whisper 결과 파싱 실패: %w", err)
	}
	return &result, nil
}

func loadRecordingMeta(dir string) (*RecordingMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, eventsFileName))
	if err != nil {
		return nil, err
	}
	var meta RecordingMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("%s 파싱 실패: %w", eventsFileName, err)
	}
	return &meta, nil
}

// modelDisplayName은 문서에 표기할 모델 이름을 반환한다.
func modelDisplayName(model string) string {
	switch model {
	case DefaultTranscribeModel:
		return "whisper-1 (OpenAI Audio API)"
	case localWhisperDefault:
		return whisperModelCanonical
	default:
		return model
	}
}

// buildTranscriptMarkdown은 발화 세그먼트와 문서 참조 이벤트를 시간순으로 병합해
// AI가 파싱하기 쉬운 하나의 JSON 문서(transcript.json)를 만든다.
// 발화(kind=speech)와 사용자 상호작용(kind=action)을 시간순 단일 타임라인으로 병합하고,
// 각 항목의 의미를 guide 필드에 자체 기술한다.

// transcriptDoc은 transcript.json의 최상위 구조이다.
type transcriptDoc struct {
	Version    int             `json:"version"`
	Tool       string          `json:"tool"`
	Kind       string          `json:"kind"`
	Mode       string          `json:"mode,omitempty"`
	Target     string          `json:"target,omitempty"`
	RecordedAt string          `json:"recordedAt,omitempty"`
	DurationMs int64           `json:"durationMs,omitempty"`
	Model      string          `json:"model"`
	Language   string          `json:"language,omitempty"`
	Guide      transcriptGuide `json:"guide"`
	Timeline   []any           `json:"timeline"`
}

// transcriptGuide는 문서를 읽는 쪽(주로 AI)을 위한 스키마 설명이다.
type transcriptGuide struct {
	Timeline string            `json:"timeline"`
	Speech   string            `json:"speech"`
	Actions  map[string]string `json:"actions"`
	Fields   string            `json:"fields"`
}

type speechEntry struct {
	Kind    string `json:"kind"` // "speech"
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Text    string `json:"text"`
}

type actionEntry struct {
	Kind      string  `json:"kind"`   // "action"
	Action    string  `json:"action"` // open | click | select | copy
	AtMs      int64   `json:"atMs"`
	File      string  `json:"file,omitempty"`
	Line      int     `json:"line,omitempty"`
	LineEnd   int     `json:"lineEnd,omitempty"`
	Approx    bool    `json:"approx,omitempty"`
	Text      string  `json:"text,omitempty"`
	Selection string  `json:"selection,omitempty"`
	PDF       *PDFRef `json:"pdf,omitempty"`
	URL       string  `json:"url,omitempty"`
	Title     string  `json:"title,omitempty"`
	Selector  string  `json:"selector,omitempty"`
	Tag       string  `json:"tag,omitempty"`
	ID        string  `json:"id,omitempty"`
	Classes   string  `json:"classes,omitempty"`
	Heading   string  `json:"heading,omitempty"`
	Context   string  `json:"context,omitempty"`
}

// actionNameByEventType은 events.json의 내부 이벤트 타입을
// 사용자 행동이 그대로 드러나는 액션 이름으로 바꾼다.
var actionNameByEventType = map[string]string{
	"open":           "open",
	"focus":          "click",
	"word":           "select",
	"copy":           "copy",
	WebEventOpen:     "open",
	WebEventClick:    "click",
	WebEventDblClick: "select",
	WebEventCopy:     "copy",
}

func transcriptGuideText() transcriptGuide {
	return transcriptGuide{
		Timeline: "발화(kind=speech)와 사용자 상호작용(kind=action)을 시간순으로 병합한 목록. 모든 시간은 녹음 시작 기준 ms.",
		Speech:   "오디오에서 전사된 발화 구간. text가 실제 발화 내용이다 (startMs~endMs).",
		Actions: map[string]string{
			"open":   "사용자가 파일을 열람함 (url이 있으면 웹 페이지를 열어 들어감)",
			"click":  "사용자가 본문 라인/블록을 클릭해 가리킴 (text=해당 위치의 원문). 웹이면 클릭한 요소 (text=요소 텍스트, selector/tag/id/classes=요소 정보)",
			"select": "사용자가 더블클릭으로 단어/텍스트를 선택해 지칭함 (text=선택된 문자열). 웹이면 selection=선택 문자열",
			"copy":   "사용자가 텍스트를 클립보드로 복사함 (text=원본 라인 전체, selection=실제 복사된 문자열). 웹이면 selection=복사 시점의 선택 문자열",
		},
		Fields: "file/line/lineEnd는 원본 파일 기준 위치. approx=true면 라인 번호가 근사값. " +
			"PDF 문서는 line 대신 pdf.page(1부터)와 pdf.x/pdf.y(페이지 좌상단 기준 0~1 정규화 클릭 좌표)를 사용하며, " +
			"click의 text는 클릭 지점 주변 텍스트다. " +
			"url이 있으면 웹 페이지 조작이다: file 대신 조작 당시 페이지 주소(url)·제목(title)과 요소 정보(selector/tag/id/classes), " +
			"요소 텍스트(text), 가장 가까운 제목(heading), 주변 텍스트(context)를 가진다. " +
			"selector는 다시 클릭할 정확한 경로가 아니라 요소를 알아볼 단서다.",
	}
}

// buildTranscriptJSON은 발화 세그먼트와 이벤트를 병합해 transcript.json 본문을 만든다.
func buildTranscriptJSON(meta *RecordingMeta, result *WhisperResult, model string) ([]byte, error) {
	type timed struct {
		at    int64
		rank  int // 같은 시각이면 발화를 먼저
		entry any
	}
	var items []timed
	for _, seg := range result.Segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		startMs := int64(seg.Start * 1000)
		items = append(items, timed{at: startMs, rank: 0, entry: speechEntry{
			Kind: "speech", StartMs: startMs, EndMs: int64(seg.End * 1000), Text: text,
		}})
	}
	for _, e := range meta.Events {
		action := actionNameByEventType[e.Type]
		if action == "" {
			action = e.Type
		}
		items = append(items, timed{at: e.T, rank: 1, entry: actionEntry{
			Kind: "action", Action: action, AtMs: e.T,
			File: e.File, Line: e.Line, LineEnd: e.LineEnd, Approx: e.Approx,
			Text: e.Text, Selection: e.Selection, PDF: e.PDF,
			URL: e.URL, Title: e.Title, Selector: e.Selector, Tag: e.Tag, ID: e.ID,
			Classes: e.Classes, Heading: e.Heading, Context: e.Context,
		}})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].at != items[j].at {
			return items[i].at < items[j].at
		}
		return items[i].rank < items[j].rank
	})
	timeline := make([]any, len(items))
	for i, it := range items {
		timeline[i] = it.entry
	}

	target := meta.Root
	if meta.Mode == string(ModeFile) {
		target = meta.File
	}
	doc := transcriptDoc{
		Version:    1,
		Tool:       "mdview",
		Kind:       "recording-transcript",
		Mode:       meta.Mode,
		Target:     target,
		RecordedAt: meta.StartedAt,
		DurationMs: meta.DurationMs,
		Model:      modelDisplayName(model),
		Language:   result.Language,
		Guide:      transcriptGuideText(),
		Timeline:   timeline,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // 마크다운/코드 본문을 이스케이프 없이 그대로 보존
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
