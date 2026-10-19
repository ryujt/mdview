package mdview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// errTreePath는 클라이언트가 요청한 트리 경로가 유효하지 않음을 나타낸다.
// 핸들러가 서버 내부 오류(500)와 잘못된 요청(400)을 구분하는 데 쓴다.
var errTreePath = errors.New("invalid tree path")

// 사용자 홈/프로젝트 폴더에 흔한 무거운 디렉토리는 트리에서 제외한다.
var excludedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".next":        true,
	".nuxt":        true,
	".cache":       true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".idea":        true,
	".vscode":      true,
}

// BuildTreeLevel은 rel 디렉토리 바로 아래 1레벨(하위 디렉토리 + 뷰어 대상 파일)만 구성한다.
// 하위 디렉토리의 내용은 조회하지 않는다 — 사이드바가 폴더를 펼칠 때마다 다시 요청한다.
// Path 필드는 root 기준 상대 경로이며 슬래시로 구분된다(OS 독립적 표현).
func BuildTreeLevel(root, rel string) (*TreeNode, error) {
	absDir, err := ResolveRelDir(root, rel)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errTreePath, err)
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	children := make([]*TreeNode, 0, len(entries))
	for _, entry := range entries {
		// 심볼릭 링크/특수 파일은 건너뛴다.
		entryInfo, err := entry.Info()
		if err != nil {
			continue
		}
		name := entry.Name()
		if entryInfo.IsDir() {
			if excludedDirs[name] {
				continue
			}
			children = append(children, &TreeNode{Name: name, Path: joinRel(rel, name), IsDir: true})
			continue
		}
		if !entryInfo.Mode().IsRegular() || !IsViewablePath(name) {
			continue
		}
		children = append(children, &TreeNode{Name: name, Path: joinRel(rel, name), IsDir: false})
	}
	sortNodes(children)

	displayName := filepath.Base(absDir)
	return &TreeNode{
		Name:     displayName,
		Path:     rel,
		IsDir:    true,
		Children: children,
	}, nil
}

// joinRel은 슬래시 구분 상대 경로에 하위 이름을 잇는다.
func joinRel(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// sortNodes는 디렉토리를 먼저, 파일을 뒤로 정렬한다. 같은 종류끼리는 이름순이다.
func sortNodes(nodes []*TreeNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// relPath는 root 기준 path의 상대 경로를 슬래시 구분자로 반환한다.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ResolveRelPath는 클라이언트가 요청한 상대 경로를 검증하고 절대 경로로 변환한다.
// 루트 바깥을 가리키는 경로나 뷰어 대상이 아닌 파일은 거부한다.
func ResolveRelPath(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path")
	}
	if !IsViewablePath(rel) {
		return "", errors.New("file type not allowed")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// 슬래시를 OS 구분자로 변환 후 clean
	candidate := filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(rel)))
	// candidate가 root 하위인지 확인 (경계 사례 포함)
	rootWithSep := absRoot + string(filepath.Separator)
	if candidate != absRoot && !strings.HasPrefix(candidate, rootWithSep) {
		return "", errors.New("path escapes root")
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("path is a directory")
	}
	return candidate, nil
}

// ResolveRelDir는 클라이언트가 요청한 상대 디렉토리 경로를 검증하고 절대 경로로 변환한다.
// 빈 문자열은 루트를 뜻한다. 루트 밖 경로, 배제 디렉토리를 지나는 경로, 일반 파일은 거부한다.
func ResolveRelDir(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := absRoot
	if rel != "" {
		candidate = filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(rel)))
		rootWithSep := absRoot + string(filepath.Separator)
		if candidate != absRoot && !strings.HasPrefix(candidate, rootWithSep) {
			return "", errors.New("path escapes root")
		}
		if containsExcludedDir(absRoot, candidate) {
			return "", errors.New("directory not allowed")
		}
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return candidate, nil
}

// containsExcludedDir는 root 기준 상대 경로의 구성요소 중 배제 디렉토리가 있는지 확인한다.
func containsExcludedDir(absRoot, candidate string) bool {
	rel := relPath(absRoot, candidate)
	if rel == "" {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if excludedDirs[part] {
			return true
		}
	}
	return false
}

// IsExcludedDir는 해당 이름이 트리 스캔에서 배제되는 디렉토리인지 반환한다.
func IsExcludedDir(name string) bool {
	return excludedDirs[name]
}
