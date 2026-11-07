package mdview

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func ptr[T any](value T) *T { return &value }

func TestSettingsDefaultAndPersistence(t *testing.T) {
	ts := newWebTestServer(t)
	res := ts.get(t, apiSettingsPath)
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("settings response must not be cached")
	}
	initial := decodeJSON[settingsResponse](t, res)
	if initial.TranscribeModel != DefaultTranscribeModel || initial.HasAPIKey || initial.KeySource != "none" {
		t.Fatalf("unexpected defaults: %+v", initial)
	}
	path, _ := ConfigPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reading defaults must not create a config file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"other_setting":{"keep":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res = ts.post(t, apiSettingsPath, `{"openai_api_key":"sk-private-test-key","transcribe_model":"gpt-4o-mini-transcribe"}`, nil)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("save status=%d: %s", res.StatusCode, body)
	}
	if strings.Contains(string(body), "sk-private-test-key") || strings.Contains(string(body), `"openai_api_key"`) {
		t.Fatal("API response exposed the stored key")
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.OpenAIAPIKey != "sk-private-test-key" || cfg.TranscribeModel != "gpt-4o-mini-transcribe" {
		t.Fatalf("saved settings were not applied: %+v, %v", cfg, err)
	}
	info, _ := os.Stat(path)
	// Windows uses ACLs and does not expose Unix permission bits.
	if runtime.GOOS != osWindows && info.Mode().Perm() != 0o600 {
		t.Fatalf("key file permissions = %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"other_setting"`) {
		t.Fatal("saving settings discarded unrelated config")
	}

	res = ts.post(t, apiSettingsPath, `{"transcribe_model":"whisper-1"}`, nil)
	updated := decodeJSON[settingsResponse](t, res)
	cfg, _ = LoadConfig()
	if cfg.OpenAIAPIKey != "sk-private-test-key" || !updated.HasSavedAPIKey {
		t.Fatal("changing only the model erased the key")
	}
	model, err := ResolveTranscribeModel()
	if err != nil || model != DefaultTranscribeModel {
		t.Fatalf("saved model not resolved: %s %v", model, err)
	}

	res = ts.post(t, apiSettingsPath, `{"openai_api_key":""}`, nil)
	cleared := decodeJSON[settingsResponse](t, res)
	if cleared.HasAPIKey || cleared.HasSavedAPIKey {
		t.Fatal("explicit key deletion was not saved")
	}
	if _, err := ResolveOpenAIKey(); err == nil {
		t.Fatal("deleted key is still used")
	}
}

func TestSettingsEnvironmentKeyIsNeverExposedOrOverwritten(t *testing.T) {
	ts := newWebTestServer(t)
	t.Setenv(openaiKeyEnv, "environment-private-key")
	res := ts.post(t, apiSettingsPath, `{"openai_api_key":"saved-private-key"}`, nil)
	state := decodeJSON[settingsResponse](t, res)
	if state.KeySource != "environment" || !state.HasSavedAPIKey {
		t.Fatalf("bad key source: %+v", state)
	}
	if key, _ := ResolveOpenAIKey(); key != "environment-private-key" {
		t.Fatal("environment precedence changed")
	}
	res = ts.post(t, apiSettingsPath, `{"openai_api_key":""}`, nil)
	state = decodeJSON[settingsResponse](t, res)
	if state.KeySource != "environment" || !state.HasAPIKey || state.HasSavedAPIKey {
		t.Fatalf("bad deletion state: %+v", state)
	}
	res = ts.get(t, apiSettingsPath)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(body), "private-key") {
		t.Fatal("GET exposed a secret")
	}
}

func TestSettingsRejectsInvalidAndCrossOriginWrites(t *testing.T) {
	ts := newWebTestServer(t)
	if err := UpdateConfig(ptr("original-key"), ptr(DefaultTranscribeModel)); err != nil {
		t.Fatal(err)
	}
	path, _ := ConfigPath()
	before, _ := os.ReadFile(path)
	cases := []struct {
		name, body, origin, contentType, site string
		status                                int
	}{
		{"missing origin", `{}`, "", "application/json", "", 403},
		{"remote origin", `{}`, "https://evil.example", "application/json", "", 403},
		{"different local port", `{}`, "http://localhost:1", "application/json", "", 403},
		{"cross site", `{}`, ts.URL, "application/json", "cross-site", 403},
		{"chat model", `{"transcribe_model":"gpt-4o","openai_api_key":"replacement"}`, ts.URL, "application/json", "", 400},
		{"local CLI model", `{"transcribe_model":"large-v3"}`, ts.URL, "application/json", "", 400},
		{"unknown field", `{"secret-unknown-field":"secret-value"}`, ts.URL, "application/json", "", 400},
		{"header injection", `{"openai_api_key":"bad\nkey"}`, ts.URL, "application/json", "", 400},
		{"trailing object", `{} {}`, ts.URL, "application/json", "", 400},
		{"oversize", `{"openai_api_key":"` + strings.Repeat("x", 9000) + `"}`, ts.URL, "application/json", "", 400},
		{"form submission", `{}`, ts.URL, "application/x-www-form-urlencoded", "", 415},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			headers := map[string]string{"Content-Type": item.contentType}
			if item.origin != "" {
				headers[originHeader] = item.origin
			}
			if item.site != "" {
				headers[secFetchSiteHeader] = item.site
			}
			res := ts.post(t, apiSettingsPath, item.body, headers)
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != item.status {
				t.Fatalf("status=%d want=%d: %s", res.StatusCode, item.status, body)
			}
			if strings.Contains(string(body), "secret-value") || strings.Contains(string(body), "original-key") {
				t.Fatal("error exposed secrets")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("rejected request modified configuration")
			}
		})
	}
}

func TestSettingsAvailableInFileModeAndRejectsRebinding(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv(openaiKeyEnv, "")
	file := filepath.Join(t.TempDir(), "document.md")
	if err := os.WriteFile(file, []byte("# Document"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileServer(NewHub(), file)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	res, err := http.Get(server.URL + apiSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	settings := decodeJSON[settingsResponse](t, res)
	if settings.DefaultModel != DefaultTranscribeModel {
		t.Fatal("settings unavailable in file mode")
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+apiSettingsPath, nil)
	req.Host = "evil.example"
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("nonlocal Host was accepted")
	}
}

func TestPipelineUsesSelectedAPIModelAndPreservesUntimedSpeech(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv(openaiKeyEnv, "")
	if err := UpdateConfig(ptr("selected-model-key"), ptr("gpt-4o-mini-transcribe")); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer selected-model-key" {
			t.Error("saved key not used")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		if r.FormValue("model") != "gpt-4o-mini-transcribe" || r.FormValue("response_format") != "json" || r.FormValue("timestamp_granularities[]") != "" {
			t.Error("wrong selected model request format")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"새 모델로 변환한 전체 발화입니다"}`)
	}))
	defer srv.Close()
	withMockAPIBase(t, srv.URL)
	dir := pipelineFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "audio.webm"), append([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte("FAKEAUDIO")...), 0o600); err != nil {
		t.Fatal(err)
	}
	job := NewPipeline().Run(dir)
	if job.State != TranscribeStateDone || job.Note != "" || calls != 1 {
		t.Fatalf("pipeline failed: %+v calls=%d", job, calls)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, dir, transcriptFileName)), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["untimedSpeech"] != "새 모델로 변환한 전체 발화입니다" {
		t.Fatal("untimed transcript was lost")
	}
	for _, item := range doc["timeline"].([]any) {
		if item.(map[string]any)["kind"] == "speech" {
			t.Fatal("timestamps were invented for an untimed response")
		}
	}
	prompt := readFixture(t, dir, promptFileName)
	if !strings.Contains(prompt, "새 모델로 변환한 전체 발화입니다") || !strings.Contains(prompt, "시간 정보 없음") {
		t.Fatal("prompt lost the untimed speech")
	}
}

func TestRunWhisperAPIJSONModels(t *testing.T) {
	for _, model := range []string{"gpt-transcribe", "gpt-4o-transcribe", "gpt-4o-mini-transcribe"} {
		t.Run(model, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				if r.FormValue("model") != model || r.FormValue("response_format") != "json" || r.FormValue("timestamp_granularities[]") != "" {
					t.Error("invalid JSON model request")
				}
				languageField := "language"
				if model == "gpt-transcribe" {
					languageField = "languages[]"
				}
				if r.FormValue(languageField) != "ko" {
					t.Error("language hint missing")
				}
				io.WriteString(w, `{"text":"전체 발화","languages":[{"code":"ko"}]}`)
			}))
			defer srv.Close()
			withMockAPIBase(t, srv.URL)
			dir := t.TempDir()
			audio := filepath.Join(dir, "audio.webm")
			if err := os.WriteFile(audio, []byte("test audio"), 0o600); err != nil {
				t.Fatal(err)
			}
			resultPath := filepath.Join(dir, "audio.json")
			if _, err := runWhisperAPI(resultPath, audio, model, "ko", "test-key"); err != nil {
				t.Fatal(err)
			}
			result, err := loadWhisperResult(resultPath)
			if err != nil || result.Language != "ko" || result.Text != "전체 발화" {
				t.Fatalf("bad JSON result: %+v %v", result, err)
			}
		})
	}
}
