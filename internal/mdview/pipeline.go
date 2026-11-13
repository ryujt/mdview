package mdview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryu/tools-camp/cli/mdview/internal/mdview/logging"
)

// maxJobNoteRunes is the maximum length (in runes) of TranscribeJob.Note (e.g. a transcription failure reason). Keeps it within one toast line.
const maxJobNoteRunes = 200

// Pipeline is the Sub-Orchestrator that runs the "saved recording → transcription (if possible) → prompt.txt" steps after recording ends.
type Pipeline struct{}

// NewPipeline creates a pipeline.
func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// Run post-processes one recording directory and returns the resulting state.
// Transcription runs only when conditions allow; prompt generation proceeds even without it.
func (p *Pipeline) Run(dir string) *TranscribeJob {
	// If events.json is missing or broken, no prompt can be built anyway,
	// so fail before calling the paid transcription API (buildPrompt produces the same error job).
	if _, err := loadRecordingMeta(dir); err != nil {
		return p.buildPrompt(dir, nil)
	}
	result, note := transcribeIfPossible(dir)
	job := p.buildPrompt(dir, result)
	job.Note = note
	return job
}

// RunAfterTranscribe starts from prompt generation for a recording whose transcription is already done (or skipped).
func (p *Pipeline) RunAfterTranscribe(dir string) *TranscribeJob {
	return p.buildPrompt(dir, loadExistingWhisperResult(dir))
}

func (p *Pipeline) buildPrompt(dir string, result *WhisperResult) *TranscribeJob {
	job := &TranscribeJob{ID: filepath.Base(dir), State: TranscribeStateDone, Dir: dir}
	meta, err := loadRecordingMeta(dir)
	if err != nil {
		logging.Error("[Pipeline] failed to read %s: %v", eventsFileName, err)
		logging.LogProcessEvent("execution", "step_err", "failed to read "+eventsFileName, nil)
		job.State = TranscribeStateError
		job.Error = err.Error()
		return job
	}
	// Report the transcript.json path only if it was actually saved (the result may feed the prompt even when the file is absent).
	if result != nil {
		transcriptPath := filepath.Join(dir, transcriptFileName)
		if _, err := os.Stat(transcriptPath); err == nil {
			job.Transcript = transcriptPath
		}
	}
	promptPath := filepath.Join(dir, promptFileName)
	text := BuildPrompt(meta, result)
	if err := os.WriteFile(promptPath, []byte(text), recordingFilePerm); err != nil {
		logging.Error("[Pipeline] failed to save %s: %v", promptFileName, err)
		logging.LogProcessEvent("synthesis", "error", "failed to save "+promptFileName, nil)
		job.State = TranscribeStateError
		job.Error = err.Error()
		return job
	}
	job.Prompt = promptPath
	logging.Info("[Pipeline] prompt created: %s (%d bytes)", promptPath, len(text))
	logging.LogProcessEvent("synthesis", "end", "prompt created: "+promptFileName, nil)
	return job
}

// transcribeIfPossible checks the transcription conditions and then attempts transcription. Skips and failures both just return a nil result without aborting.
// note is the skip reason or a "transcription failed: ..." message to show the user; it is empty when transcription succeeds.
func transcribeIfPossible(dir string) (result *WhisperResult, note string) {
	model, err := ResolveTranscribeModel()
	if err != nil {
		return nil, "cannot read transcription settings"
	}
	if reason := TranscribeSkipReason(dir, model); reason != "" {
		logging.Info("[Pipeline] transcription skipped: %s", reason)
		fmt.Printf("Skipping transcription (%s) — building the prompt from the action log only.\n", reason)
		return nil, reason
	}
	if _, err := TranscribeRecording(dir, model, ""); err != nil {
		logging.Error("[Pipeline] transcription failed: %v", err)
		fmt.Fprintf(os.Stderr, "Transcription failed (%v) — building the prompt from the action log only\n", err)
		return nil, truncateRunes("transcription failed: "+err.Error(), maxJobNoteRunes)
	}
	return loadExistingWhisperResult(dir), ""
}

// loadExistingWhisperResult reads the saved transcription result (nil if absent).
func loadExistingWhisperResult(dir string) *WhisperResult {
	audioPath, err := findRecordingAudio(dir)
	if err != nil {
		return nil
	}
	result, err := loadWhisperResult(whisperResultPath(dir, audioPath))
	if err != nil {
		return nil
	}
	return result
}

// truncateRunes trims surrounding whitespace and keeps at most max runes.
func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
