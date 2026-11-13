package mdview

// Mode describes how mdview is running.
type Mode string

const (
	ModeFile   Mode = "file"
	ModeFolder Mode = "folder"
)

// ModeInfo is the DTO that tells the frontend the current run mode.
type ModeInfo struct {
	Mode     Mode   `json:"mode"`
	Filename string `json:"filename,omitempty"`
	Root     string `json:"root,omitempty"`
}

// TreeNode represents a single node in the directory tree.
// Path is relative to the root directory and uses slash separators.
type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"isDir"`
	Children []*TreeNode `json:"children,omitempty"`
}

// FileMessage is the message sent over WS in folder mode.
// File mode sends raw text for backward compatibility.
type FileMessage struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// RecordingResult is the response DTO of the recording save API (/api/recording).
type RecordingResult struct {
	Dir    string `json:"dir"`
	Audio  string `json:"audio"`
	Events string `json:"events"`
	// ID is the recording directory name (key for transcription status lookups)
	ID string `json:"id"`
	// Transcribing reports whether background post-processing (transcription, prompt) has started
	Transcribing bool `json:"transcribing"`
}

// Auto-transcription job states
const (
	TranscribeStateRunning = "transcribing"
	TranscribeStateDone    = "done"
	TranscribeStateError   = "error"
)

// TranscribeJob is the status DTO of a recording's auto-transcription job (/api/recording/status).
type TranscribeJob struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Dir   string `json:"dir"`
	// Transcript is the absolute path of transcript.json once complete
	Transcript string `json:"transcript,omitempty"`
	// TranscriptRel is the root-relative path in folder mode (for opening directly in the viewer)
	TranscriptRel string `json:"transcriptRel,omitempty"`
	// Prompt is the absolute path of the always-generated prompt.txt (when post-processing succeeds)
	Prompt string `json:"prompt,omitempty"`
	// PromptRel is the root-relative path in folder mode
	PromptRel string `json:"promptRel,omitempty"`
	Error     string `json:"error,omitempty"`
	// Note is a short user-facing message, e.g. why transcription was skipped or failed
	Note string `json:"note,omitempty"`
	// ClipboardCopied reports whether the full transcript.json was copied to the system clipboard.
	ClipboardCopied   bool   `json:"clipboardCopied"`
	ClipboardError    string `json:"clipboardError,omitempty"`
	NotificationError string `json:"notificationError,omitempty"`
	UpdatedAt         string `json:"updatedAt"`
}
