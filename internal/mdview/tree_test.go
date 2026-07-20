package mdview

import (
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

func collectPaths(node *TreeNode, paths map[string]bool) {
	if !node.IsDir {
		paths[node.Path] = true
		return
	}
	if node.Path != "" {
		paths[node.Path+"/"] = true
	}
	for _, child := range node.Children {
		collectPaths(child, paths)
	}
}

func TestBuildTreeFiltersViewable(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "a.md")
	mustWrite(t, root, "b.go")
	mustWrite(t, root, "c.png")
	mustWrite(t, root, "node_modules/x.js")
	mustWrite(t, root, "src/util.go")
	mustWrite(t, root, "assets/logo.svg")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	tree, err := BuildTree(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	collectPaths(tree, paths)

	for _, want := range []string{"a.md", "b.go", "src/util.go", "src/"} {
		if !paths[want] {
			t.Errorf("expected %q in tree, got %v", want, paths)
		}
	}
	for _, exclude := range []string{"c.png", "node_modules/x.js", "node_modules/", "assets/", "empty/"} {
		if paths[exclude] {
			t.Errorf("expected %q to be excluded, got %v", exclude, paths)
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
