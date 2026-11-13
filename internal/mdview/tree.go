package mdview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// errTreePath indicates that the tree path requested by the client is invalid.
// Handlers use it to tell internal server errors (500) from bad requests (400).
var errTreePath = errors.New("invalid tree path")

// Heavy directories common in home/project folders are excluded from the tree.
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

// BuildTreeLevel builds only the single level directly under rel (subdirectories + viewable files).
// Subdirectory contents are not read — the sidebar requests them each time a folder is expanded.
// The Path field is relative to root and slash-separated (OS-independent).
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
		// Skip symlinks and special files.
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

// joinRel appends a child name to a slash-separated relative path.
func joinRel(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// sortNodes sorts directories first, then files, each by name.
func sortNodes(nodes []*TreeNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// relPath returns path relative to root with slash separators.
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

// ResolveRelPath validates a client-requested relative path and converts it to an absolute path.
// Paths outside the root and non-viewable files are rejected.
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
	// Convert slashes to OS separators, then clean
	candidate := filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(rel)))
	// Check that candidate is under root (including edge cases)
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

// ResolveRelDir validates a client-requested relative directory path and converts it to an absolute path.
// An empty string means the root. Paths outside the root, paths through excluded directories, and regular files are rejected.
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

// containsExcludedDir reports whether any component of a root-relative path is an excluded directory.
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

// IsExcludedDir reports whether the name is a directory excluded from tree scans.
func IsExcludedDir(name string) bool {
	return excludedDirs[name]
}
