package mdview

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ryujt/mdview/internal/mdview/logging"
)

// maxJobNoteRunes는 TranscribeJob.Note(전사 실패 사유 등)의 최대 길이(rune)이다. 토스트 한 줄을 넘지 않게 한다.
const maxJobNoteRunes = 200

// Pipeline은 녹음 종료 후 "저장된 녹화 → 전사(가능 시) → prompt.txt" 단계를 진행하는 Sub-Orchestrator이다.
type Pipeline struct{}

// NewPipeline은 파이프라인을 만든다.
func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// Run은 녹화 디렉토리 하나를 후처리하고 결과 상태를 반환한다.
// 전사는 조건이 맞을 때만 수행하며, 전사가 없어도 프롬프트 생성은 진행한다.
func (p *Pipeline) Run(dir string) *TranscribeJob {
	// events.json이 없거나 깨졌으면 어차피 프롬프트를 만들 수 없으므로,
	// 유료 전사 API를 호출하기 전에 먼저 실패시킨다 (buildPrompt가 같은 오류 job을 만든다).
	if _, err := loadRecordingMeta(dir); err != nil {
		return p.buildPrompt(dir, nil)
	}
	result, note := transcribeIfPossible(dir)
	job := p.buildPrompt(dir, result)
	job.Note = note
	return job
}

// RunAfterTranscribe는 이미 전사를 마친(또는 생략한) 녹화에 대해 프롬프트 생성부터 진행한다.
func (p *Pipeline) RunAfterTranscribe(dir string) *TranscribeJob {
	return p.buildPrompt(dir, loadExistingWhisperResult(dir))
}

func (p *Pipeline) buildPrompt(dir string, result *WhisperResult) *TranscribeJob {
	job := &TranscribeJob{ID: filepath.Base(dir), State: TranscribeStateDone, Dir: dir}
	meta, err := loadRecordingMeta(dir)
	if err != nil {
		logging.Error("[Pipeline] %s 읽기 실패: %v", eventsFileName, err)
		logging.LogProcessEvent("execution", "step_err", eventsFileName+" 읽기 실패", nil)
		job.State = TranscribeStateError
		job.Error = err.Error()
		return job
	}
	// transcript.json이 실제로 저장된 경우에만 경로를 알린다 (결과는 프롬프트에 쓰이더라도 파일은 없을 수 있다).
	if result != nil {
		transcriptPath := filepath.Join(dir, transcriptFileName)
		if _, err := os.Stat(transcriptPath); err == nil {
			job.Transcript = transcriptPath
		}
	}
	promptPath := filepath.Join(dir, promptFileName)
	text := BuildPrompt(meta, result)
	if err := os.WriteFile(promptPath, []byte(text), recordingFilePerm); err != nil {
		logging.Error("[Pipeline] %s 저장 실패: %v", promptFileName, err)
		logging.LogProcessEvent("synthesis", "error", promptFileName+" 저장 실패", nil)
		job.State = TranscribeStateError
		job.Error = err.Error()
		return job
	}
	job.Prompt = promptPath
	logging.Info("[Pipeline] 프롬프트 생성: %s (%d바이트)", promptPath, len(text))
	logging.LogProcessEvent("synthesis", "end", "프롬프트 생성: "+promptFileName, nil)
	return job
}

// transcribeIfPossible은 전사 조건을 확인한 뒤 전사를 시도한다. 생략·실패 모두 결과 nil을 반환할 뿐 중단하지 않는다.
// note는 사용자에게 보여 줄 생략 사유 또는 "전사 실패: ..." 안내이며, 전사가 정상이면 빈 문자열이다.
func transcribeIfPossible(dir string) (result *WhisperResult, note string) {
	if reason := TranscribeSkipReason(dir, DefaultTranscribeModel); reason != "" {
		logging.Info("[Pipeline] 전사 생략: %s", reason)
		fmt.Printf("전사를 생략합니다 (%s) — 조작 기록만으로 프롬프트를 만듭니다.\n", reason)
		return nil, reason
	}
	if _, err := TranscribeRecording(dir, DefaultTranscribeModel, ""); err != nil {
		logging.Error("[Pipeline] 전사 실패: %v", err)
		fmt.Fprintf(os.Stderr, "전사 실패 (%v) — 조작 기록만으로 프롬프트를 만듭니다\n", err)
		return nil, truncateRunes("전사 실패: "+err.Error(), maxJobNoteRunes)
	}
	return loadExistingWhisperResult(dir), ""
}

// loadExistingWhisperResult는 저장된 전사 결과를 읽는다 (없으면 nil).
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
