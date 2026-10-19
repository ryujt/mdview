package mdview

import (
	"encoding/json"
	"errors"
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

// ErrNoOpenAIKey는 환경변수와 설정 파일 어디에도 OpenAI API 키가 없을 때 ResolveOpenAIKey가 감싸서 돌려주는 오류다.
var ErrNoOpenAIKey = errors.New("OpenAI API 키가 없습니다")

// Config는 mdview의 사용자 설정이다.
type Config struct {
	// OpenAIAPIKey는 전사(whisper-1) API 호출에 사용하는 키이다.
	OpenAIAPIKey string `json:"openai_api_key"`
}

// ConfigDir은 mdview 설정 디렉토리(~/.config/mdview)를 반환한다. Chrome 전용 프로필도 이 아래에 둔다.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("홈 디렉토리를 찾을 수 없습니다: %w", err)
	}
	return filepath.Join(home, ".config", configDirName), nil
}

// ConfigPath는 설정 파일 경로를 반환한다.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
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
		"%w. %s 환경변수를 설정하거나 설정 파일을 작성하세요:\n  %s\n  {\"openai_api_key\": \"sk-...\"}",
		ErrNoOpenAIKey, openaiKeyEnv, path)
}
