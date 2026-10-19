package mdview

// Mode는 mdview 실행 방식을 나타낸다.
type Mode string

const (
	ModeFile   Mode = "file"
	ModeFolder Mode = "folder"
)

// ModeInfo는 프론트엔드에 현재 실행 모드를 알려주는 DTO이다.
type ModeInfo struct {
	Mode     Mode   `json:"mode"`
	Filename string `json:"filename,omitempty"`
	Root     string `json:"root,omitempty"`
}

// TreeNode는 디렉토리 트리의 단일 노드를 표현한다.
// Path는 루트 디렉토리 기준 상대 경로이며 슬래시 구분자를 사용한다.
type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"isDir"`
	Children []*TreeNode `json:"children,omitempty"`
}

// FileMessage는 folder 모드에서 WS를 통해 전달되는 메시지이다.
// file 모드는 하위 호환을 위해 raw 텍스트를 전달한다.
type FileMessage struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// RecordingResult는 녹화 저장 API(/api/recording)의 응답 DTO이다.
type RecordingResult struct {
	Dir    string `json:"dir"`
	Audio  string `json:"audio"`
	Events string `json:"events"`
	// ID는 녹화 디렉토리 이름 (전사 상태 조회 키)
	ID string `json:"id"`
	// Transcribing은 백그라운드 후처리(전사·프롬프트)가 시작되었는지 여부
	Transcribing bool `json:"transcribing"`
}

// 자동 전사 작업 상태
const (
	TranscribeStateRunning = "transcribing"
	TranscribeStateDone    = "done"
	TranscribeStateError   = "error"
)

// 웹 페이지 조작 이벤트 타입 (events.json의 type 값).
// 문서 이벤트(open/focus/word/copy)와 구분하기 위해 web- 접두를 둔다.
const (
	WebEventOpen     = "web-open"
	WebEventClick    = "web-click"
	WebEventDblClick = "web-dblclick"
	WebEventCopy     = "web-copy"
)

// TranscribeJob은 녹화 자동 전사 작업의 상태 DTO이다 (/api/recording/status).
type TranscribeJob struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Dir   string `json:"dir"`
	// Transcript는 완료 시 transcript.json 절대 경로
	Transcript string `json:"transcript,omitempty"`
	// TranscriptRel은 folder 모드에서 루트 기준 상대 경로 (뷰어에서 바로 열기용)
	TranscriptRel string `json:"transcriptRel,omitempty"`
	// Prompt는 항상 생성되는 prompt.txt 절대 경로 (후처리 성공 시)
	Prompt string `json:"prompt,omitempty"`
	// PromptRel은 folder 모드에서 루트 기준 상대 경로
	PromptRel string `json:"promptRel,omitempty"`
	Error     string `json:"error,omitempty"`
	// Note는 전사를 생략·실패한 사유 등 사용자에게 보여 줄 짧은 안내
	Note      string `json:"note,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}
