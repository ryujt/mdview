package mdview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var configMu sync.RWMutex

// User config file location: ~/.config/mdview/config.json
const (
	configDirName  = "mdview"
	configFileName = "config.json"

	openaiKeyEnv = "OPENAI_API_KEY"
)

// ErrNoOpenAIKey is the error ResolveOpenAIKey wraps when neither the environment nor the config file has an OpenAI API key.
var ErrNoOpenAIKey = errors.New("OpenAI API key not found")

// Config is mdview's user configuration.
type Config struct {
	// OpenAIAPIKey is the key used for transcription API calls.
	OpenAIAPIKey    string `json:"openai_api_key"`
	TranscribeModel string `json:"transcribe_model,omitempty"`
}

// ConfigDir returns the mdview config directory (~/.config/mdview).
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory: %w", err)
	}
	return filepath.Join(home, ".config", configDirName), nil
}

// ConfigPath returns the config file path.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// LoadConfig reads the config file. It returns an empty config if the file does not exist.
func LoadConfig() (*Config, error) {
	configMu.RLock()
	defer configMu.RUnlock()
	return loadConfig()
}

func loadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config file (%s): %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file (%s): %w", path, err)
	}
	return &cfg, nil
}

// UpdateConfig saves atomically, preserving omitted fields and unknown existing settings.
// If the API key pointer points to an empty string, the stored key is deleted.
func UpdateConfig(apiKey, model *string) error {
	configMu.Lock()
	defer configMu.Unlock()
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	fields := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
	}
	if apiKey != nil {
		fields["openai_api_key"], _ = json.Marshal(strings.TrimSpace(*apiKey))
	}
	if model != nil {
		fields["transcribe_model"], _ = json.Marshal(*model)
	}
	data, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// ResolveTranscribeModel reads the saved choice and falls back to the default model if none is set.
func ResolveTranscribeModel() (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	if cfg.TranscribeModel == "" {
		return DefaultTranscribeModel, nil
	}
	if !isAPIModel(cfg.TranscribeModel) {
		return "", errors.New("unsupported transcription model")
	}
	return cfg.TranscribeModel, nil
}

// ResolveOpenAIKey looks up the API key from the OPENAI_API_KEY environment variable, then the config file.
func ResolveOpenAIKey() (string, error) {
	if key := os.Getenv(openaiKeyEnv); key != "" {
		return key, nil
	}
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	if cfg.OpenAIAPIKey != "" {
		return cfg.OpenAIAPIKey, nil
	}
	path, _ := ConfigPath()
	return "", fmt.Errorf(
		"%w. Set the %s environment variable or create the config file:\n  %s\n  {\"openai_api_key\": \"sk-...\"}",
		ErrNoOpenAIKey, openaiKeyEnv, path)
}
