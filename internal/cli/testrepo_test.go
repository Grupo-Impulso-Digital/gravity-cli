package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, dir, "README.md", "# Project\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "chore: initial")
	for rel, body := range files {
		writeFile(t, dir, rel, body)
	}
	writeFile(t, dir, "feature.go", "package main\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "feat: add feature")
	return dir
}

type fakePlatform struct {
	mu       sync.Mutex
	requests map[string][][]byte
	features map[string]bool
	llm      func(body []byte) any
	routes   map[string]http.HandlerFunc
}

func (f *fakePlatform) bodies(key string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[key]
}

func (f *fakePlatform) serve(t *testing.T) *httptest.Server {
	t.Helper()
	f.requests = map[string][][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.requests[key] = append(f.requests[key], body)
		f.mu.Unlock()
		if h, ok := f.routes[key]; ok {
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			h(w, r)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/whoami":
			_ = json.NewEncoder(w).Encode(map[string]any{"organizationName": "Acme", "features": f.features})
		case r.URL.Path == "/api/llm/v1/messages" && f.llm != nil:
			_ = json.NewEncoder(w).Encode(f.llm(body))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"Not found."}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func toolUseResponse(name string, input any) map[string]any {
	raw, _ := json.Marshal(input)
	return map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "stop_reason": "tool_use",
		"content": []any{map[string]any{"type": "tool_use", "id": "tu_1", "name": name, "input": json.RawMessage(raw)}},
	}
}
