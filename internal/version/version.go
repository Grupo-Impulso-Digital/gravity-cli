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

const fallback = "dev"

var (
	once     sync.Once
	resolved string
)

// String returns the effective version without a leading "v".
func String() string {
	once.Do(func() { resolved = resolve(Version, readBuildInfo) })
	return resolved
}

// UserAgent returns the HTTP User-Agent the CLI sends.
func UserAgent() string {
	return fmt.Sprintf("gravity-cli/%s (%s/%s)", String(), runtime.GOOS, runtime.GOARCH)
}

func readBuildInfo() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	return info.Main.Version, true
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
