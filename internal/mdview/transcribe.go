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

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

// Policy constants for transcription (audio → text).
// The default backend is the whisper-1 model of the OpenAI Audio Transcriptions API
// (requires an API key from the config file or OPENAI_API_KEY); segment timestamps
// from the verbose_json response are merged with the event timeline.
// JSON transcription models are stored as a full transcript without timing.
// Passing a non-OpenAI model name such as -model large-v3 runs the local whisper CLI instead.
const (
	DefaultTranscribeModel = "whisper-1" // OpenAI API model
	localWhisperDefault    = "large-v3"  // recommended model for the local CLI
	whisperModelCanonical  = "openai/whisper-large-v3"
	whisperCmdName         = "whisper"
	transcriptFileName     = "transcript.json"

	// OpenAI Audio API request policy
	openaiRequestTimeout = 10 * time.Minute
	openaiErrorBodyLimit = 500
	maxAPIAudioBytes     = 25 << 20 // OpenAI Audio API upload limit (25MB)
)

// openaiAPIBase is the OpenAI API endpoint (replaced in tests).
var openaiAPIBase = "https://api.openai.com/v1"

// whisperInstallGuide is the guidance shown when the whisper CLI is missing.
const whisperInstallGuide = `whisper CLI not found.
To install:
  pip install -U openai-whisper   # uses the openai/whisper-large-v3 model
  brew install ffmpeg             # required for audio decoding (macOS)`

// TranscriptSegment is a speech segment output by Whisper (in seconds).
type TranscriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// WhisperResult holds the fields used from whisper --output_format json output.
type WhisperResult struct {
	Text      string              `json:"text"`
	Language  string              `json:"language"`
	Segments  []TranscriptSegment `json:"segments"`
	Languages []struct {
		Code string `json:"code"`
	} `json:"languages,omitempty"`
}

// RecordingEvent is a single event recorded in events.json.
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
}

// PDFRef is a reference position within a PDF document. Since PDFs have no line numbers,
// a position is expressed as a page number plus 0~1 normalized coordinates (x, y) from the page's top-left.
// Events without coordinates (select/copy) record only the page number, so X/Y are pointers to tell them apart.
type PDFRef struct {
	Page int      `json:"page"`
	X    *float64 `json:"x,omitempty"`
	Y    *float64 `json:"y,omitempty"`
}

// RecordingMeta is the overall structure of events.json.
type RecordingMeta struct {
	Version    int              `json:"version"`
	Mode       string           `json:"mode"`
	Root       string           `json:"root,omitempty"`
	File       string           `json:"file,omitempty"`
	StartedAt  string           `json:"startedAt"`
	SavedAt    string           `json:"savedAt"`
	DurationMs int64            `json:"durationMs"`
	Audio      string           `json:"audio"`
	Events     []RecordingEvent `json:"events"`
}

// TranscribeRecording is the Orchestrator that transcribes the audio in a recording directory
// and produces transcript.json. An existing whisper JSON result is reused (saves re-running the model).
// Process events are logged only here (the orchestrator); workers just return results.
func TranscribeRecording(dir, model, lang string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	audioPath, err := findRecordingAudio(absDir)
	if err != nil {
		logging.LogProcessEvent("execution", "step_err", "failed to locate audio file", nil)
		return "", err
	}
	logging.Info("[Transcribe] target audio: %s", audioPath)
	logging.LogProcessEvent("execution", "step_ok", "located audio file: "+filepath.Base(audioPath), nil)

	resultPath := whisperResultPath(absDir, audioPath)
	if _, err := os.Stat(resultPath); os.IsNotExist(err) {
		started := time.Now()
		if err := validateRecordingAudio(audioPath); err != nil {
			logging.LogProcessEvent("execution", "step_err", "audio format validation failed: "+err.Error(), nil)
			return "", err
		}
		if isAPIModel(model) {
			apiKey, err := ResolveOpenAIKey()
			if err != nil {
				logging.LogProcessEvent("execution", "step_err", "no API key", nil)
				return "", err
			}
			artifact, err := runWhisperAPI(resultPath, audioPath, model, lang, apiKey)
			elapsed := float64(time.Since(started).Milliseconds())
			if err != nil {
				logging.LogProcessEvent("execution", "step_err", model+" API transcription failed",
					&logging.EventOpts{ArtifactPath: artifact, DurationMs: elapsed})
				return "", err
			}
			logging.LogProcessEvent("execution", "step_ok",
				fmt.Sprintf("%s API transcription (%.1fs)", model, elapsed/1000),
				&logging.EventOpts{ArtifactPath: artifact, DurationMs: elapsed})
		} else {
			artifact, err := runWhisper(absDir, audioPath, model, lang)
			elapsed := float64(time.Since(started).Milliseconds())
			if err != nil {
				logging.LogProcessEvent("execution", "step_err", "local whisper transcription failed",
					&logging.EventOpts{ArtifactPath: artifact, DurationMs: elapsed})
				return "", err
			}
			logging.LogProcessEvent("execution", "step_ok",
				fmt.Sprintf("local whisper transcription (%.1fs)", elapsed/1000),
				&logging.EventOpts{ArtifactPath: artifact, DurationMs: elapsed})
		}
	} else {
		fmt.Printf("Reusing existing transcription result: %s\n", resultPath)
		logging.Info("[Transcribe] reusing existing transcription result: %s", resultPath)
		logging.LogProcessEvent("execution", "step_ok", "reused existing transcription result", nil)
	}

	result, err := loadWhisperResult(resultPath)
	if err != nil {
		logging.LogProcessEvent("execution", "step_err", "failed to parse transcription result", nil)
		return "", err
	}
	meta, err := loadRecordingMeta(absDir)
	if err != nil {
		// Generate the transcript even without event metadata.
		fmt.Fprintf(os.Stderr, "warning: transcribing without %s (%v)\n", eventsFileName, err)
		logging.Warn("[Transcribe] %s missing: %v", eventsFileName, err)
		meta = &RecordingMeta{}
	}

	doc, err := buildTranscriptJSON(meta, result, model)
	if err != nil {
		logging.LogProcessEvent("synthesis", "error", "failed to build transcript.json", nil)
		return "", fmt.Errorf("transcript build error: %w", err)
	}
	outPath := filepath.Join(absDir, transcriptFileName)
	if err := os.WriteFile(outPath, doc, recordingFilePerm); err != nil {
		logging.LogProcessEvent("synthesis", "error", "failed to save transcript.json", nil)
		return "", fmt.Errorf("transcript write error: %w", err)
	}
	statePath := logging.LogStateSnapshot("transcribe-meta", map[string]any{
		"dir": absDir, "audio": filepath.Base(audioPath), "language": result.Language,
		"segments": len(result.Segments), "events": len(meta.Events), "durationMs": meta.DurationMs,
	})
	logging.Info("[Transcribe] document created: %s (%d segments, %d events)", outPath, len(result.Segments), len(meta.Events))
	logging.LogProcessEvent("synthesis", "end",
		fmt.Sprintf("timeline merged: %d segments + %d events", len(result.Segments), len(meta.Events)),
		&logging.EventOpts{ArtifactPath: statePath})
	return outPath, nil
}

// TranscribeSkipReason returns why automatic transcription should be skipped (empty means it can run).
// An existing whisper result can be reused regardless of key or size, and local CLI models are not bound by API limits.
func TranscribeSkipReason(dir, model string) string {
	audioPath, err := findRecordingAudio(dir)
	if err != nil {
		return "no audio file"
	}
	if _, err := os.Stat(whisperResultPath(dir, audioPath)); err == nil {
		return ""
	}
	if !isAPIModel(model) {
		return ""
	}
	if _, err := ResolveOpenAIKey(); err != nil {
		if !errors.Is(err, ErrNoOpenAIKey) {
			return "cannot read the OpenAI API key: " + err.Error()
		}
		return ErrNoOpenAIKey.Error()
	}
	if size := fileSize(audioPath); size > maxAPIAudioBytes {
		return fmt.Sprintf("audio is %dMB, exceeding the API upload limit (%dMB)", size>>20, maxAPIAudioBytes>>20)
	}
	return ""
}

// findRecordingAudio finds the audio file (audio.*) in a recording directory.
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
	return "", fmt.Errorf("audio file (%s.*) not found: %s", audioFileBase, dir)
}

// whisperResultName is the name of the JSON result file whisper produces (<audio base>.json).
func whisperResultName() string {
	return audioFileBase + ".json"
}

func whisperResultPath(dir, audioPath string) string {
	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	return filepath.Join(dir, base+".json")
}

// isAPIModel reports whether the model is transcribed via the OpenAI Audio API.
func isAPIModel(model string) bool {
	for _, option := range transcriptionModels() {
		if option.ID == model {
			return true
		}
	}
	return false
}

// runWhisperAPI runs transcription via the OpenAI Audio Transcriptions API and stores
// the verbose_json response for whisper-1, or the json response for other API models.
// Returns: path of the detailed log artifact saved under the whisper/ category (empty if none).
func runWhisperAPI(resultPath, audioPath, model, lang, apiKey string) (string, error) {
	fmt.Printf("Transcribing: %s (%s)\n", modelDisplayName(model), filepath.Base(audioPath))
	started := time.Now()
	audioBytes := fileSize(audioPath)
	uploadName := transcribeUploadName(audioPath)
	responseFormat := "json"
	if model == DefaultTranscribeModel {
		responseFormat = "verbose_json"
	}
	requestSummary := fmt.Sprintf(
		"POST %s/audio/transcriptions\nmodel=%s\nresponse_format=%s\nlanguage=%s\nfile=%s (%d bytes)",
		openaiAPIBase, model, responseFormat, orDefault(lang, "(auto-detect)"), uploadName, audioBytes)

	// Helper that writes a detailed log even on failure
	fail := func(err error) (string, error) {
		artifact := logging.LogWhisperCall("openai-api", model, lang, filepath.Base(audioPath),
			audioBytes, requestSummary, "", "", float64(time.Since(started).Milliseconds()), err.Error())
		return artifact, err
	}

	audio, err := os.Open(audioPath)
	if err != nil {
		return fail(fmt.Errorf("failed to open audio: %w", err))
	}
	defer audio.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", uploadName)
	if err != nil {
		return fail(err)
	}
	if _, err := io.Copy(part, audio); err != nil {
		return fail(fmt.Errorf("failed to read audio: %w", err))
	}
	_ = mw.WriteField("model", model)
	_ = mw.WriteField("response_format", responseFormat)
	if model == DefaultTranscribeModel {
		_ = mw.WriteField("timestamp_granularities[]", "segment")
	}
	if lang != "" {
		field := "language"
		if model == "gpt-transcribe" {
			field = "languages[]"
		}
		_ = mw.WriteField(field, lang)
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
		return fail(fmt.Errorf("OpenAI API request failed: %w", err))
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fail(fmt.Errorf("failed to read OpenAI API response: %w", err))
	}
	if res.StatusCode != http.StatusOK {
		detail := strings.ReplaceAll(string(body), apiKey, "[redacted]")
		if len(detail) > openaiErrorBodyLimit {
			detail = detail[:openaiErrorBodyLimit] + "…"
		}
		return fail(fmt.Errorf("OpenAI API error (HTTP %d): %s", res.StatusCode, detail))
	}
	// Parse to validate, then save the raw response as-is (treated the same as a local whisper result).
	var check WhisperResult
	if err := json.Unmarshal(body, &check); err != nil {
		return fail(fmt.Errorf("failed to parse OpenAI API response: %w", err))
	}
	if err := os.WriteFile(resultPath, body, recordingFilePerm); err != nil {
		return fail(fmt.Errorf("failed to save transcription result: %w", err))
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

// runWhisper runs transcription with the openai-whisper CLI (blocking, progress output passed through).
// Returns: path of the detailed log artifact saved under the whisper/ category (empty if none).
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
		return logCall(whisperCmdName+" (not installed)", installErr.Error()), installErr
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
	fmt.Printf("Transcribing: %s (%s)\n", modelDisplayName(model), filepath.Base(audioPath))
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		runErr := fmt.Errorf("whisper run failed: %w", err)
		return logCall(cmdline, runErr.Error()), runErr
	}
	return logCall(cmdline, ""), nil
}

func loadWhisperResult(path string) (*WhisperResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read whisper result: %w", err)
	}
	var result WhisperResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse whisper result: %w", err)
	}
	if result.Language == "" && len(result.Languages) > 0 {
		result.Language = result.Languages[0].Code
	}
	return &result, nil
}

// untimedSpeech is the full transcript from a model that provides no timing.
func untimedSpeech(result *WhisperResult) string {
	if result == nil {
		return ""
	}
	for _, seg := range result.Segments {
		if strings.TrimSpace(seg.Text) != "" {
			return ""
		}
	}
	return strings.TrimSpace(result.Text)
}

func loadRecordingMeta(dir string) (*RecordingMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, eventsFileName))
	if err != nil {
		return nil, err
	}
	var meta RecordingMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", eventsFileName, err)
	}
	return &meta, nil
}

// modelDisplayName returns the model name shown in the document.
func modelDisplayName(model string) string {
	if isAPIModel(model) {
		return model + " (OpenAI Audio API)"
	}
	switch model {
	case localWhisperDefault:
		return whisperModelCanonical
	default:
		return model
	}
}

// buildTranscriptMarkdown merges speech segments and document reference events chronologically
// into a single JSON document (transcript.json) that is easy for AI to parse.
// Speech (kind=speech) and user interactions (kind=action) are merged into one chronological timeline,
// and the meaning of each entry is self-described in the guide field.

// transcriptDoc is the top-level structure of transcript.json.
type transcriptDoc struct {
	Version       int             `json:"version"`
	Tool          string          `json:"tool"`
	Kind          string          `json:"kind"`
	Mode          string          `json:"mode,omitempty"`
	Target        string          `json:"target,omitempty"`
	RecordedAt    string          `json:"recordedAt,omitempty"`
	DurationMs    int64           `json:"durationMs,omitempty"`
	Model         string          `json:"model"`
	Language      string          `json:"language,omitempty"`
	UntimedSpeech string          `json:"untimedSpeech,omitempty"`
	Guide         transcriptGuide `json:"guide"`
	Timeline      []any           `json:"timeline"`
}

// transcriptGuide is a schema description for the reader of the document (mainly AI).
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
}

// actionNameByEventType maps internal events.json event types
// to action names that directly describe what the user did.
// Types not listed here (e.g. web page interaction events left in older recordings) are excluded from the timeline.
var actionNameByEventType = map[string]string{
	"open":  "open",
	"focus": "click",
	"word":  "select",
	"copy":  "copy",
}

func transcriptGuideText() transcriptGuide {
	return transcriptGuide{
		Timeline: "Speech (kind=speech) and user interactions (kind=action) merged in chronological order. All times are in ms from the start of the recording.",
		Speech:   "A speech segment transcribed from the audio. text is what was actually said (startMs~endMs).",
		Actions: map[string]string{
			"open":   "The user opened a file",
			"click":  "The user clicked a line/block in the body to point at it (text=source text at that position)",
			"select": "The user double-clicked to select a word/text and refer to it (text=selected string)",
			"copy":   "The user copied text to the clipboard (text=entire source line, selection=the string actually copied)",
		},
		Fields: "untimedSpeech is the full transcript from a model that provides no per-segment timing; do not align it with action times. " +
			"file/line/lineEnd are positions in the source file. approx=true means the line number is approximate. " +
			"PDF documents use pdf.page (1-based) and pdf.x/pdf.y (click coordinates normalized to 0~1 from the page's top-left) instead of line, " +
			"and a click's text is the text around the click point.",
	}
}

// buildTranscriptJSON merges speech segments and events into the transcript.json body.
func buildTranscriptJSON(meta *RecordingMeta, result *WhisperResult, model string) ([]byte, error) {
	type timed struct {
		at    int64
		rank  int // at equal times, speech comes first
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
		action, known := actionNameByEventType[e.Type]
		if !known {
			continue
		}
		items = append(items, timed{at: e.T, rank: 1, entry: actionEntry{
			Kind: "action", Action: action, AtMs: e.T,
			File: e.File, Line: e.Line, LineEnd: e.LineEnd, Approx: e.Approx,
			Text: e.Text, Selection: e.Selection, PDF: e.PDF,
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
		Version:       1,
		Tool:          "mdview",
		Kind:          "recording-transcript",
		Mode:          meta.Mode,
		Target:        target,
		RecordedAt:    meta.StartedAt,
		DurationMs:    meta.DurationMs,
		Model:         modelDisplayName(model),
		Language:      result.Language,
		UntimedSpeech: untimedSpeech(result),
		Guide:         transcriptGuideText(),
		Timeline:      timeline,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // preserve Markdown/code bodies verbatim without escaping
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
