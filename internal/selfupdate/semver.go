package selfupdate

// semver.go — minimal semantic-version parsing and ordering for release
// tags ("v1.2.3", "1.2.3-rc.1", "v2.0.0-beta.3+build"). Only what the
// update checker needs: is candidate NEWER than current?
//
// Rules (semver 2.0.0, simplified):
//   - an optional leading "v" is stripped;
//   - numeric parts compare numerically (1.10.0 > 1.9.0);
//   - a missing part counts as 0 (1.2 == 1.2.0);
//   - a prerelease sorts BEFORE the release with the same numeric parts
//     (1.0.0-rc.1 < 1.0.0), and prerelease identifiers compare per semver
//     (numeric < alphanumeric, dot-separated);
//   - build metadata (+...) is ignored;
//   - anything unparseable sorts as the LOWEST possible version so a
//     malformed remote tag can never force an "update" onto a good build.

import (
	"strconv"
	"strings"
)

type semver struct {
	major, minor, patch uint64
	pre                 []string // prerelease identifiers; nil = release
	ok                  bool
}

// parseSemver parses a version tag; ok=false when it does not look like a
// semver at all.
func parseSemver(v string) semver {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i] // build metadata ignored
	}
	var s semver
	if i := strings.IndexByte(v, '-'); i >= 0 {
		s.pre = splitPre(v[i+1:])
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return semver{}
	}
	var nums [3]uint64
	for i, p := range parts {
		if p == "" {
			return semver{}
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return semver{}
		}
		nums[i] = n
	}
	s.major, s.minor, s.patch = nums[0], nums[1], nums[2]
	s.ok = true
	return s
}

// splitPre splits a prerelease string into identifiers; an empty
// identifier marks the whole thing invalid (handled by compare as "lowest").
func splitPre(p string) []string {
	return strings.Split(p, ".")
}

// compare returns -1/0/+1 for a<b, a==b, a>b. Invalid versions sort lowest.
func compare(a, b semver) int {
	if a.ok != b.ok {
		if a.ok {
			return 1
		}
		return -1
	}
	if !a.ok {
		return 0
	}
	if c := cmpU64(a.major, b.major); c != 0 {
		return c
	}
	if c := cmpU64(a.minor, b.minor); c != 0 {
		return c
	}
	if c := cmpU64(a.patch, b.patch); c != 0 {
		return c
	}
	// equal numeric parts: release (no prerelease) > prerelease
	switch {
	case a.pre == nil && b.pre == nil:
		return 0
	case a.pre == nil:
		return 1
	case b.pre == nil:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, xerr := strconv.ParseUint(x, 10, 64)
		yn, yerr := strconv.ParseUint(y, 10, 64)
		switch {
		case xerr == nil && yerr == nil:
			if c := cmpU64(xn, yn); c != 0 {
				return c
			}
		case xerr == nil:
			return -1 // numeric identifiers < alphanumeric
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return cmpU64(uint64(len(a.pre)), uint64(len(b.pre)))
}

func cmpU64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// isNewer reports whether candidate is strictly newer than current.
func isNewer(current, candidate string) bool {
	return compare(parseSemver(candidate), parseSemver(current)) > 0
}
