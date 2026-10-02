// Package version resolves the gravity CLI version from the linker stamp or the module build info.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

// Version is stamped at build time with -ldflags "-X github.com/Grupo-Impulso-Digital/gravity-cli/internal/version.Version=<v>".
var Version = ""

// Commit is stamped at build time with the source revision.
var Commit = ""

// Date is stamped at build time with the RFC 3339 build date.
var Date = ""

const fallback = "dev"

var (
	once     sync.Once
	resolved string
	build    Info
)

// Info describes the running binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// String returns the effective version without a leading "v".
func String() string {
	load()
	return resolved
}

// Build returns the version, commit, build date, Go version and platform of the binary.
func Build() Info {
	load()
	return build
}

// UserAgent returns the HTTP User-Agent the CLI sends.
func UserAgent() string {
	return fmt.Sprintf("gravity-cli/%s (%s; %s)", String(), runtime.GOOS, runtime.GOARCH)
}

func load() {
	once.Do(func() {
		bi, ok := debug.ReadBuildInfo()
		resolved = resolve(Version, func() (string, bool) {
			if !ok {
				return "", false
			}
			return bi.Main.Version, true
		})
		build = Info{
			Version:   resolved,
			GoVersion: runtime.Version(),
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		}
		if ok {
			build.Commit, build.BuildDate = vcsInfo(bi.Settings)
		}
		if c := strings.TrimSpace(Commit); c != "" {
			build.Commit = c
		}
		if d := strings.TrimSpace(Date); d != "" {
			build.BuildDate = d
		}
	})
}

func vcsInfo(settings []debug.BuildSetting) (commit, date string) {
	dirty := false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.time":
			date = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if commit != "" && dirty {
		commit += "-dirty"
	}
	return commit, date
}

func resolve(stamped string, buildInfo func() (string, bool)) string {
	if v := normalize(stamped); v != "" {
		return v
	}
	if buildInfo != nil {
		if v, ok := buildInfo(); ok && v != "(devel)" {
			if n := normalize(v); n != "" {
				return n
			}
		}
	}
	return fallback
}

func normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
