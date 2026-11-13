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
		{DefaultTranscribeModel, "Whisper 1 (default)", true},
		{"gpt-transcribe", "GPT Transcribe", false},
		{"gpt-4o-transcribe", "GPT-4o Transcribe", false},
		{"gpt-4o-mini-transcribe", "GPT-4o mini Transcribe", false},
	}
}

// settingsResponse only says whether a key exists. The stored secret key is never returned.
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
			http.Error(w, "Please send settings as JSON", http.StatusUnsupportedMediaType)
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
			http.Error(w, "Invalid settings request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "Invalid settings request", http.StatusBadRequest)
			return
		}
		if update.Model != nil && !isAPIModel(*update.Model) {
			http.Error(w, "Unsupported transcription model", http.StatusBadRequest)
			return
		}
		if update.APIKey != nil {
			key := strings.TrimSpace(*update.APIKey)
			if len(key) > 4096 || strings.IndexFunc(key, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
				http.Error(w, "The API key contains whitespace or invalid characters", http.StatusBadRequest)
				return
			}
		}
		if err := UpdateConfig(update.APIKey, update.Model); err != nil {
			http.Error(w, "Could not save settings. Check write permission on the config folder.", http.StatusInternalServerError)
			return
		}
	}
	cfg, err := LoadConfig()
	if err != nil {
		http.Error(w, "Could not read the settings file", http.StatusInternalServerError)
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
