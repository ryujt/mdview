package mdview

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTMLPreviewServesOriginalDocumentAndAssets(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"screens/index.html":        "<!doctype html><h1>HTML preview</h1>",
		"screens/order.HTM":         "<!doctype html><h1>Order</h1>",
		"screens/assets/review.css": "h1 { color: blue; }",
		"screens/assets/review.js":  "document.body.dataset.ready = 'yes';",
	}
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []Mode{ModeFolder, ModeFile} {
		t.Run(string(mode), func(t *testing.T) {
			hub := NewHub()
			var server *Server
			var err error
			prefix := "/screens/"
			if mode == ModeFolder {
				server, err = NewFolderServer(hub, root)
			} else {
				server, err = NewFileServer(hub, filepath.Join(root, "screens/index.html"))
				prefix = "/"
			}
			if err != nil {
				t.Fatal(err)
			}
			handler := server.Handler()
			for _, asset := range []struct{ name, dest, contentType string }{
				{"index.html", "iframe", "text/html"},
				{"order.HTM", "iframe", "text/html"},
				{"assets/review.css", "style", "text/css"},
				{"assets/review.js", "script", "javascript"},
			} {
				req := httptest.NewRequest(http.MethodGet, "http://localhost"+prefix+asset.name+"?v=1", nil)
				req.Header.Set("Sec-Fetch-Dest", asset.dest)
				req.Header.Set("Accept", "text/html,*/*")
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				if resp.Code != http.StatusOK || resp.Body.String() != files["screens/"+asset.name] {
					t.Errorf("%s: status=%d body=%q", asset.name, resp.Code, resp.Body.String())
				}
				if !strings.Contains(resp.Header().Get("Content-Type"), asset.contentType) {
					t.Errorf("%s: unexpected content type %q", asset.name, resp.Header().Get("Content-Type"))
				}
			}
			if mode == ModeFolder {
				req := httptest.NewRequest(http.MethodGet, "http://localhost/screens/index.html", nil)
				req.Header.Set("Sec-Fetch-Dest", "document")
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "/static/js/app.js") {
					t.Fatal("top-level HTML navigation must open the viewer")
				}
			}
		})
	}
}
