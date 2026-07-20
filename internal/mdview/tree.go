package mdview

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

// BuildTree는 root 기준 뷰어 대상 파일 + 해당 파일을 포함하는 디렉토리로만 이루어진 트리를 만든다.
// Path 필드는 root 기준 상대 경로이며 슬래시로 구분된다(OS 독립적 표현).
func BuildTree(root string) (*TreeNode, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("root is not a directory")
	}
	node, _ := buildNode(absRoot, absRoot, info)
	if node == nil {
		node = &TreeNode{
			Name:  filepath.Base(absRoot),
			Path:  "",
			IsDir: true,
		}
	}
	return node, nil
}

// buildNode는 주어진 경로의 노드를 재귀적으로 구성한다.
// 자손에 뷰어 대상 파일이 하나도 없으면 nil을 반환하여 상위에서 배제되도록 한다.
func buildNode(root, path string, info os.FileInfo) (*TreeNode, bool) {
	rel := relPath(root, path)
	name := info.Name()

	if info.IsDir() {
		if rel != "" && excludedDirs[name] {
			return nil, false
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, false
		}
		children := make([]*TreeNode, 0, len(entries))
		hasViewable := false
		for _, entry := range entries {
			// 심볼릭 링크/특수 파일은 건너뛴다.
			entryInfo, err := entry.Info()
			if err != nil {
				continue
			}
			if !entryInfo.IsDir() && !entryInfo.Mode().IsRegular() {
				continue
			}
			childPath := filepath.Join(path, entry.Name())
			child, childHasViewable := buildNode(root, childPath, entryInfo)
			if child != nil {
				children = append(children, child)
				if childHasViewable {
					hasViewable = true
				}
			}
		}
		// 루트는 항상 노출. 그 외에는 뷰어 대상 파일이 포함된 디렉토리만 유지한다.
		if rel == "" || hasViewable {
			sortNodes(children)
			displayName := name
			if rel == "" {
				displayName = filepath.Base(root)
			}
			return &TreeNode{
				Name:     displayName,
				Path:     rel,
				IsDir:    true,
				Children: children,
			}, hasViewable
		}
		return nil, false
	}

	// 파일
	if !IsViewablePath(name) {
		return nil, false
	}
	return &TreeNode{
		Name:  name,
		Path:  rel,
		IsDir: false,
	}, true
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

// IsExcludedDir는 해당 이름이 트리 스캔에서 배제되는 디렉토리인지 반환한다.
func IsExcludedDir(name string) bool {
	return excludedDirs[name]
}
