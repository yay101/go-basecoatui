package basecoat

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// basecoatStylesURL is the jsdelivr-hosted basecoat CDN bundle. The
// URL is pinned to @1 so jsdelivr serves the latest 1.x release —
// basecoat's 1.0 line is the first to ship the complete bundle
// (Tailwind v4 preflight + theme + component classes) as a single
// CDN file. The pre-1.0 styles.css previously hosted at
// basecoatui.com/assets/styles.css no longer exists; this URL
// replaces it.
//
// It is a var (not a const) so tests can override it the same way
// they override basecoatJSURL.
var basecoatStylesURL = "https://cdn.jsdelivr.net/npm/basecoat-css@1/dist/basecoat.cdn.min.css"

// basecoatJSURL is the jsdelivr-hosted basecoat runtime bundle. The
// URL is unpinned (no @version) so jsdelivr always serves the latest
// published version of the file. The file is small (~30-60KB minified),
// so we re-download on every Init rather than tracking ETags.
var basecoatJSURL = "https://cdn.jsdelivr.net/npm/basecoat-css/dist/js/all.min.js"

// downloadFile performs a simple HTTP GET and writes the body to dst.
// Used for download-once assets (styles.css) where the caller doesn't
// need to react to remote changes.
func downloadFile(url, dst string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// ensureBasecoatStyles downloads basecoat's CDN bundle
// (basecoat.cdn.min.css) from jsdelivr into
// {cacheDir}/basecoat/styles.css if it isn't already cached. The
// file is downloaded once and never refreshed — the library doesn't
// track ETag or Last-Modified for it. If the cache is already
// populated the existing copy is reused without a network call.
//
// Returns the absolute path of the cached file.
func ensureBasecoatStyles(cacheDir string) (string, error) {
	dst := filepath.Join(cacheDir, "basecoat", "styles.css")
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if err := downloadFile(basecoatStylesURL, dst); err != nil {
		return "", fmt.Errorf("downloading basecoat styles: %w", err)
	}
	return dst, nil
}

// ensureBasecoatJS fetches the latest basecoat.js runtime from
// jsdelivr and writes it to {cacheDir}/basecoat/basecoat.js on every
// Init. The file is small (~30-60KB) so re-downloading is cheap; the
// alternative is an ETag round-trip per Init, which adds a network
// hop to save a few KB. We pick simplicity.
//
// Returns:
//   - path: the absolute path of the cached file (non-empty on success
//     AND on transient network failure if a previous cache exists)
//   - data: the runtime bytes, ready to prepend to basecoat.js
//   - err:  non-nil only if both the download and the cache lookup fail
//
// If the network is down and no cache exists, the caller should fall
// back to the embedded //go:embed byte slice; that fallback is the
// caller's responsibility (Init wraps the failure in the embedded
// bytes itself).
func ensureBasecoatJS(cacheDir string) (path string, data []byte, err error) {
	dst := filepath.Join(cacheDir, "basecoat", "basecoat.js")

	resp, err := http.Get(basecoatJSURL)
	if err != nil {
		// Network failure: serve whatever's on disk, if anything.
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("downloading basecoat runtime: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Non-200 (e.g. CDN outage): serve cache, no error.
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, basecoatJSURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("reading basecoat runtime body: %w", err)
	}

	// Successfully fetched. Persist and return.
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		// Cache write failed but we have fresh bytes; return them
		// without a path so the caller uses the in-memory copy.
		return "", body, nil
	}
	if err := os.WriteFile(dst, body, 0644); err != nil {
		return "", body, nil
	}
	return dst, body, nil
}
