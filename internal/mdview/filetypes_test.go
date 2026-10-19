package mdview

import "testing"

func TestIsViewablePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"README.md", true},
		{"docs/guide.MD", true},
		{"main.go", true},
		{"src/app.TS", true},
		{"config.yaml", true},
		{"data.json", true},
		{".env", true},
		{".env.local", true},
		{"Dockerfile", true},
		{"sub/Makefile", true},
		{".gitignore", true},
		{"go.mod", true},
		{"yarn.lock", true},
		{"/abs/path/util.go", true},
		{"notes.txt", true},
		{"docs/report.pdf", true},
		{"Report.PDF", true},
		{"image.png", false},
		{"binary", false},
		{"archive.tar.gz", false},
		{".envrc", false},
	}
	for _, c := range cases {
		if got := IsViewablePath(c.path); got != c.want {
			t.Errorf("IsViewablePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsPDFPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"doc.pdf", true},
		{"docs/Report.PDF", true},
		{"doc.pdf.md", false},
		{"pdf", false},
		{"a.md", false},
	}
	for _, c := range cases {
		if got := IsPDFPath(c.path); got != c.want {
			t.Errorf("IsPDFPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsMarkdownPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"a.md", true},
		{"docs/B.MD", true},
		{"a.markdown", false},
		{"a.go", false},
		{"md", false},
	}
	for _, c := range cases {
		if got := IsMarkdownPath(c.path); got != c.want {
			t.Errorf("IsMarkdownPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
