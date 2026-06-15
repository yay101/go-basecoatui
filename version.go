package basecoat

import (
	"fmt"
	"strconv"
	"strings"
)

// versionEntry maps a single supported basecoat major.minor release to its
// exact version and download URL. The pre-compiled CSS at BasecoatURL
// already includes the Tailwind v4 preflight and theme layer, so no
// separate Tailwind download is needed.
type versionEntry struct {
	BasecoatVersion string
	BasecoatURL     string
}

// basecoatVersions is the canonical version table. The key is "major.minor".
// When basecoat cuts a new release that bumps the required Tailwind version,
// a new entry is added here and the old one remains for backward compat.
//
// BasecoatURL points at basecoat-cdn.min.css, the pre-compiled build that
// contains only the basecoat component classes (it is built with
// @source(none) so Tailwind utility classes are not generated). Projects
// load Tailwind v4 utility classes separately via the @tailwindcss/browser
// script (see example/public/index.html).
var basecoatVersions = map[string]versionEntry{
	"0.3": {
		BasecoatVersion: "0.3.11",
		BasecoatURL:     "https://unpkg.com/basecoat-css@0.3.11/dist/basecoat.cdn.min.css",
	},
}

// resolvedVersion is produced by resolveVersion and holds the concrete
// download URLs after matching a semver constraint.
type resolvedVersion struct {
	entry versionEntry
	ver   string
}

// parseConstraint strips a leading ^/~/>=/= and extracts major.minor.
// It does not attempt full semver parsing — only the first two segments
// matter for looking up the version entry.
func parseConstraint(s string) (major, minor int, err error) {
	s = strings.TrimLeft(s, "^~=> ")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid version constraint: %q", s)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid major version in %q: %w", s, err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minor version in %q: %w", s, err)
	}
	return major, minor, nil
}

// resolveVersion matches a user-supplied constraint like "^0.3.11" against
// the version table and returns the concrete resolvedVersion.
func resolveVersion(constraint string) (*resolvedVersion, error) {
	major, minor, err := parseConstraint(constraint)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d.%d", major, minor)
	entry, ok := basecoatVersions[key]
	if !ok {
		return nil, fmt.Errorf("basecoat: unsupported version %q (no entry for %s)", constraint, key)
	}
	parts := strings.SplitN(entry.BasecoatVersion, ".", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("basecoat: bad version entry %q", entry.BasecoatVersion)
	}
	eMajor, _ := strconv.Atoi(parts[0])
	eMinor, _ := strconv.Atoi(parts[1])
	if eMajor != major || eMinor != minor {
		return nil, fmt.Errorf("basecoat: entry %s doesn't match constraint %q", entry.BasecoatVersion, constraint)
	}
	return &resolvedVersion{entry: entry, ver: entry.BasecoatVersion}, nil
}
