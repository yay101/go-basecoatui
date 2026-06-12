package basecoat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cachedFile returns the on-disk path for a cached CSS asset.
// Layout: {cacheDir}/{kind}/v{version}/{kind}.css
func cachedFile(cacheDir, kind, version string) string {
	return filepath.Join(cacheDir, kind, "v"+version, kind+".css")
}

// ensureCached checks whether the file exists on disk; if not it downloads
// the given URL and writes it to the cache path. Returns the local path.
func ensureCached(cacheDir, kind, version, url string) (string, error) {
	dst := cachedFile(cacheDir, kind, version)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", err
	}
	if err := downloadFile(url, dst); err != nil {
		return "", fmt.Errorf("downloading %s v%s: %w", kind, version, err)
	}
	return dst, nil
}

// downloadFile performs a simple HTTP GET and writes the body to dst.
func downloadFile(url, dst string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// checkLatest fetches basecoat's package.json from unpkg and returns the
// latest published version string. Only called when AutoUpdate is true.
func checkLatest() (string, error) {
	resp, err := http.Get("https://unpkg.com/basecoat/package.json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pkg); err != nil {
		return "", err
	}
	return pkg.Version, nil
}

// isNewerVersion returns true if latest > current via numeric semver comparison.
func isNewerVersion(latest, current string) bool {
	la := parseVersion(latest)
	ca := parseVersion(current)
	for i := 0; i < 3; i++ {
		if la[i] != ca[i] {
			return la[i] > ca[i]
		}
	}
	return false
}

// parseVersion splits "X.Y.Z" into [3]int. Missing segments become 0.
func parseVersion(v string) [3]int {
	var out [3]int
	parts := strings.SplitN(v, ".", 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
