package mdview

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustWrite(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func levelPaths(node *TreeNode) map[string]bool {
	paths := map[string]bool{}
	for _, child := range node.Children {
		key := child.Path
		if child.IsDir {
			key += "/"
		}
		paths[key] = true
	}
	return paths
}

func TestBuildTreeLevelListsSingleLevel(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "a.md")
	mustWrite(t, root, "b.go")
	mustWrite(t, root, "c.png")
	mustWrite(t, root, "notes.txt")
	mustWrite(t, root, "node_modules/x.js")
	mustWrite(t, root, "src/util.go")
	mustWrite(t, root, "assets/logo.svg")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	level, err := BuildTreeLevel(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !level.IsDir || level.Path != "" {
		t.Errorf("루트 노드 불일치: %+v", level)
	}
	paths := levelPaths(level)

	// 뷰어 대상 파일과 (배제 목록 외) 모든 하위 디렉토리가 1레벨로 노출된다.
	for _, want := range []string{"a.md", "b.go", "notes.txt", "src/", "assets/", "empty/"} {
		if !paths[want] {
			t.Errorf("expected %q in level, got %v", want, paths)
		}
	}
	for _, exclude := range []string{"c.png", "node_modules/", "src/util.go"} {
		if paths[exclude] {
			t.Errorf("expected %q to be excluded, got %v", exclude, paths)
		}
	}
	// 하위 디렉토리의 내용은 조회하지 않는다.
	for _, child := range level.Children {
		if len(child.Children) != 0 {
			t.Errorf("1레벨 응답에 하위 내용이 포함됨: %+v", child)
		}
	}

	sub, err := BuildTreeLevel(root, "src")
	if err != nil {
		t.Fatal(err)
	}
	if subPaths := levelPaths(sub); !subPaths["src/util.go"] {
		t.Errorf("src 레벨에 util.go가 없음: %v", subPaths)
	}
}

func TestBuildTreeLevelPathErrors(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "doc.md")

	if _, err := BuildTreeLevel(root, ".."); !errors.Is(err, errTreePath) {
		t.Errorf("잘못된 경로 오류가 errTreePath로 식별되지 않음: %v", err)
	}
	if _, err := BuildTreeLevel(root, "missing"); !errors.Is(err, errTreePath) {
		t.Errorf("없는 경로 오류가 errTreePath로 식별되지 않음: %v", err)
	}
	if _, err := BuildTreeLevel(root, ""); err != nil {
		t.Errorf("루트 조회 실패: %v", err)
	}
}

func TestResolveRelDir(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "src/util.go")
	mustWrite(t, root, "node_modules/x.js")
	mustWrite(t, root, "doc.md")

	for _, rel := range []string{"", "src"} {
		if _, err := ResolveRelDir(root, rel); err != nil {
			t.Errorf("ResolveRelDir(%q) unexpected error: %v", rel, err)
		}
	}
	for _, rel := range []string{"..", "../outside", "node_modules", "src/../node_modules", "doc.md", "missing"} {
		if _, err := ResolveRelDir(root, rel); err == nil {
			t.Errorf("ResolveRelDir(%q) expected error", rel)
		}
	}
}

func TestResolveRelPath(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "doc.md")
	mustWrite(t, root, "main.go")
	mustWrite(t, root, "img.png")
	if err := os.MkdirAll(filepath.Join(root, "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"doc.md", "main.go"} {
		if _, err := ResolveRelPath(root, rel); err != nil {
			t.Errorf("ResolveRelPath(%q) unexpected error: %v", rel, err)
		}
	}
	for _, rel := range []string{"", "img.png", "../outside.md", "dir.md", "missing.go"} {
		if _, err := ResolveRelPath(root, rel); err == nil {
			t.Errorf("ResolveRelPath(%q) expected error", rel)
		}
	}
}
