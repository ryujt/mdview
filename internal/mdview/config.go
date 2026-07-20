package mdview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// 사용자 설정 파일 위치: ~/.config/mdview/config.json
const (
	configDirName  = "mdview"
	configFileName = "config.json"

	openaiKeyEnv = "OPENAI_API_KEY"
)

// Config는 mdview의 사용자 설정이다.
type Config struct {
	// OpenAIAPIKey는 전사(whisper-1) API 호출에 사용하는 키이다.
	OpenAIAPIKey string `json:"openai_api_key"`
}

// ConfigPath는 설정 파일 경로를 반환한다.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("홈 디렉토리를 찾을 수 없습니다: %w", err)
	}
	return filepath.Join(home, ".config", configDirName, configFileName), nil
}

// LoadConfig는 설정 파일을 읽는다. 파일이 없으면 빈 설정을 반환한다.
func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("설정 파일 읽기 실패 (%s): %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("설정 파일 파싱 실패 (%s): %w", path, err)
	}
	return &cfg, nil
}

// ResolveOpenAIKey는 OPENAI_API_KEY 환경변수 → 설정 파일 순서로 API 키를 찾는다.
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
		"OpenAI API 키가 없습니다. %s 환경변수를 설정하거나 설정 파일을 작성하세요:\n  %s\n  {\"openai_api_key\": \"sk-...\"}",
		openaiKeyEnv, path)
}
