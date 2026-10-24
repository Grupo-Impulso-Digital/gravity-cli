// Package release computes the next version of the CLI from its tags.
package release

import (
	"fmt"
	"regexp"
	"strconv"
)

// FirstRelease is the version of a repository without a vX.Y.Z tag.
const FirstRelease = "v1.0.0"

var (
	stableRE    = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	candidateRE = regexp.MustCompile(`^(v\d+\.\d+\.\d+)-rc\.(\d+)$`)
)

type semver [3]int

func parse(tag string) (semver, bool) {
	m := stableRE.FindStringSubmatch(tag)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return semver{}, false
		}
		v[i] = n
	}
	return v, true
}

func (v semver) less(o semver) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

func (v semver) String() string {
	return fmt.Sprintf("v%d.%d.%d", v[0], v[1], v[2])
}

// Latest returns the newest vX.Y.Z tag, ignoring release candidates and any other tag.
func Latest(tags []string) string {
	var best semver
	found := false
	for _, t := range tags {
		if v, ok := parse(t); ok && (!found || best.less(v)) {
			best, found = v, true
		}
	}
	if !found {
		return ""
	}
	return best.String()
}

// Next returns the newest vX.Y.Z tag bumped by patch, minor or major, or with candidate its next vX.Y.Z-rc.N.
func Next(tags []string, bump string, candidate bool) (string, error) {
	if bump != "patch" && bump != "minor" && bump != "major" {
		return "", fmt.Errorf("bump must be patch, minor or major, got %q", bump)
	}
	next := FirstRelease
	if latest := Latest(tags); latest != "" {
		v, _ := parse(latest)
		switch bump {
		case "patch":
			v[2]++
		case "minor":
			v = semver{v[0], v[1] + 1, 0}
		default:
			v = semver{v[0] + 1, 0, 0}
		}
		next = v.String()
	}
	if !candidate {
		return next, nil
	}
	rc := 0
	for _, t := range tags {
		if m := candidateRE.FindStringSubmatch(t); len(m) == 3 && m[1] == next {
			if n, err := strconv.Atoi(m[2]); err == nil && n > rc {
				rc = n
			}
		}
	}
	return fmt.Sprintf("%s-rc.%d", next, rc+1), nil
}
