package distribution_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fakeCurl = `#!/bin/sh
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -H) shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
printf '%s\n' "$url" >> "$FAKE_CURL_LOG"
file="$(awk -F '\t' -v u="$url" '$1 == u { print $2; exit }' "$FAKE_CURL_ROUTES")"
[ -n "$file" ] || exit 22
if [ -n "$out" ]; then cp "$file" "$out"; else cat "$file"; fi
`

const releasesAPI = "https://api.github.com/repos/Grupo-Impulso-Digital/gravity-cli/releases"

type fakeNet struct {
	t      *testing.T
	dir    string
	routes map[string]string
}

func newFakeNet(t *testing.T) *fakeNet {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh targets Linux and macOS")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeNet{t: t, dir: dir, routes: map[string]string{}}
}

func (f *fakeNet) serve(url string, body []byte) {
	f.t.Helper()
	name := filepath.Join(f.dir, fmt.Sprintf("r%d", len(f.routes)))
	if err := os.WriteFile(name, body, 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.routes[url] = name
}

func (f *fakeNet) releases(pages ...[]string) {
	f.t.Helper()
	for i, tags := range pages {
		for n := 0; i < len(pages)-1 && len(tags) < 100; n++ {
			tags = append(tags, fmt.Sprintf("v0.0.%d", n))
		}
		list := make([]map[string]any, 0, len(tags))
		for _, tag := range tags {
			list = append(list, map[string]any{"tag_name": tag, "prerelease": strings.Contains(tag, "-")})
		}
		data, _ := json.MarshalIndent(list, "", "  ")
		f.serve(fmt.Sprintf("%s?per_page=100&page=%d", releasesAPI, i+1), data)
	}
	f.serve(fmt.Sprintf("%s?per_page=100&page=%d", releasesAPI, len(pages)+1), []byte("[]"))
}

func (f *fakeNet) run(env ...string) (string, string, error) {
	f.t.Helper()
	var lines []string
	for url, file := range f.routes {
		lines = append(lines, url+"\t"+file)
	}
	routes := filepath.Join(f.dir, "routes.tsv")
	if err := os.WriteFile(routes, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join("..", "..", "install.sh"))
	cmd.Env = append([]string{
		"PATH=" + filepath.Join(f.dir, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + f.dir, "FAKE_CURL_LOG=" + filepath.Join(f.dir, "curl.log"), "FAKE_CURL_ROUTES=" + routes,
	}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), stderr.String(), err
}

func TestInstallResolvesTheNewestReleaseOfAMajor(t *testing.T) {
	f := newFakeNet(t)
	f.releases(
		[]string{"v2.0.0", "v1.10.0-rc.1", "v1.9.3", "v0.3.1", "docs-synced"},
		[]string{"v1.10.0", "v1.2.9", "v0.3.0", "v0.2.2"},
	)
	f.serve(releasesAPI+"/latest", []byte(`{"tag_name": "v2.0.0"}`))
	cases := map[string]string{"": "v1.10.0", "1": "v1.10.0", "v1": "v1.10.0", "0": "v0.3.1", "2": "v2.0.0", "latest": "v2.0.0", "v1.2.9": "v1.2.9", "1.2.9": "v1.2.9"}
	for version, want := range cases {
		env := []string{"GRAVITY_RESOLVE_ONLY=1"}
		if version != "" {
			env = append(env, "GRAVITY_VERSION="+version)
		}
		got, stderr, err := f.run(env...)
		if err != nil || got != want {
			t.Fatalf("GRAVITY_VERSION=%q resolved %q, want %q (err %v)\n%s", version, got, want, err, stderr)
		}
	}
	for _, bad := range []string{"3", "1.2", "main"} {
		if _, stderr, err := f.run("GRAVITY_RESOLVE_ONLY=1", "GRAVITY_VERSION="+bad); err == nil {
			t.Fatalf("GRAVITY_VERSION=%q must fail\n%s", bad, stderr)
		}
	}
}

func targetPlatform(t *testing.T) (string, string) {
	t.Helper()
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		t.Skipf("no gravity build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return runtime.GOOS, arch
}

func archive(t *testing.T, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "gravity", Mode: 0o755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallDownloadsVerifiesAndInstallsTheMajorRelease(t *testing.T) {
	goos, arch := targetPlatform(t)
	f := newFakeNet(t)
	f.releases([]string{"v1.4.2", "v1.4.1", "v0.3.1"})
	name := fmt.Sprintf("gravity_1.4.2_%s_%s.tar.gz", goos, arch)
	data := archive(t, "#!/bin/sh\necho gravity 1.4.2\n")
	sum := sha256.Sum256(data)
	base := "https://github.com/Grupo-Impulso-Digital/gravity-cli/releases/download/v1.4.2/"
	f.serve(base+name, data)
	f.serve(base+"checksums.txt", []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"))
	dir := filepath.Join(t.TempDir(), "bin")
	if _, stderr, err := f.run("GRAVITY_INSTALL_DIR=" + dir); err != nil {
		t.Fatalf("install: %v\n%s", err, stderr)
	}
	out, err := exec.Command(filepath.Join(dir, "gravity")).Output()
	if err != nil || strings.TrimSpace(string(out)) != "gravity 1.4.2" {
		t.Fatalf("installed binary: %q %v", out, err)
	}
	f.serve(base+"checksums.txt", []byte(strings.Repeat("0", 64)+"  "+name+"\n"))
	if _, stderr, err := f.run("GRAVITY_INSTALL_DIR=" + dir); err == nil || !strings.Contains(stderr, "checksum mismatch") {
		t.Fatalf("a checksum mismatch must fail: %v\n%s", err, stderr)
	}
}
