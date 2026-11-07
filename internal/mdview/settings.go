package mdview

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
)

const apiSettingsPath = "/api/settings"

type transcriptionModelOption struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Timestamps bool   `json:"timestamps"`
}

func transcriptionModels() []transcriptionModelOption {
	return []transcriptionModelOption{
		{DefaultTranscribeModel, "Whisper 1 (기본)", true},
		{"gpt-transcribe", "GPT Transcribe", false},
		{"gpt-4o-transcribe", "GPT-4o Transcribe", false},
		{"gpt-4o-mini-transcribe", "GPT-4o mini Transcribe", false},
	}
}

// settingsResponse에는 키의 존재 여부만 넣는다. 저장된 비밀 키는 반환하지 않는다.
type settingsResponse struct {
	HasAPIKey       bool                       `json:"has_api_key"`
	HasSavedAPIKey  bool                       `json:"has_saved_api_key"`
	KeySource       string                     `json:"key_source"`
	TranscribeModel string                     `json:"transcribe_model"`
	DefaultModel    string                     `json:"default_model"`
	Models          []transcriptionModelOption `json:"models"`
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, methodNotAllowedMessage, http.StatusMethodNotAllowed)
		return
	}
	if !isSameOrigin(r) || (r.Header.Get(secFetchSiteHeader) != "" && r.Header.Get(secFetchSiteHeader) != secFetchSiteSame) || (r.Method == http.MethodPost && !hasLocalOrigin(r)) {
		http.Error(w, forbiddenOriginMessage, http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "설정은 JSON 형식으로 전송해 주세요", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		var update struct {
			APIKey *string `json:"openai_api_key"`
			Model  *string `json:"transcribe_model"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&update); err != nil {
			http.Error(w, "설정 요청 형식이 올바르지 않습니다", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "설정 요청 형식이 올바르지 않습니다", http.StatusBadRequest)
			return
		}
		if update.Model != nil && !isAPIModel(*update.Model) {
			http.Error(w, "지원하지 않는 전사 모델입니다", http.StatusBadRequest)
			return
		}
		if update.APIKey != nil {
			key := strings.TrimSpace(*update.APIKey)
			if len(key) > 4096 || strings.IndexFunc(key, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
				http.Error(w, "API 키에 공백이나 잘못된 문자가 포함되어 있습니다", http.StatusBadRequest)
				return
			}
		}
		if err := UpdateConfig(update.APIKey, update.Model); err != nil {
			http.Error(w, "설정을 저장하지 못했습니다. 설정 폴더의 쓰기 권한을 확인해 주세요.", http.StatusInternalServerError)
			return
		}
	}
	cfg, err := LoadConfig()
	if err != nil {
		http.Error(w, "설정 파일을 읽지 못했습니다", http.StatusInternalServerError)
		return
	}
	source := "none"
	if cfg.OpenAIAPIKey != "" {
		source = "config"
	}
	if os.Getenv(openaiKeyEnv) != "" {
		source = "environment"
	}
	writeJSON(w, http.StatusOK, settingsResponse{
		HasAPIKey: source != "none", HasSavedAPIKey: cfg.OpenAIAPIKey != "", KeySource: source,
		TranscribeModel: orDefault(cfg.TranscribeModel, DefaultTranscribeModel),
		DefaultModel:    DefaultTranscribeModel, Models: transcriptionModels(),
	})
}
