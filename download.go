package basecoat

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// downloadTimeout bounds every CDN HTTP request so a stalled or
// hung CDN connection can never block Init indefinitely. 30s is
// generous for the ~217KB CSS bundle on a slow link while still
// failing fast enough that a wedged CDN surfaces as an error
// rather than a hang.
const downloadTimeout = 30 * time.Second

// httpClient is the shared client used for all CDN downloads. It
// carries a timeout so callers never block forever on a hung
// connection. It is a var (not a const) so tests can swap in a
// client with a custom transport if needed.
var httpClient = &http.Client{Timeout: downloadTimeout}

// Sentinel errors returned by the download helpers. Callers can
// use errors.Is to distinguish a CDN outage from a cache miss.
var (
	// ErrStylesDownload is returned when the basecoat CDN styles
	// bundle could not be fetched and no cached copy is available.
	ErrStylesDownload = errors.New("basecoat styles download failed")
	// ErrJSDownload is returned when the basecoat runtime JS could
	// not be fetched and no cached or embedded copy is available.
	ErrJSDownload = errors.New("basecoat runtime download failed")
	// ErrTailwindBrowserDownload is returned when the Tailwind v4
	// browser build could not be fetched and no cached copy is
	// available. It is not a hard error for Init — the bundle is
	// prepended to basecoat.js only when it can be fetched, so a
	// failure degrades gracefully (the page loses Tailwind utility
	// classes but basecoat components still work).
	ErrTailwindBrowserDownload = errors.New("tailwind browser build download failed")
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

// tailwindBrowserURL is the jsdelivr-hosted Tailwind v4 browser build.
// The basecoat CDN styles bundle is compiled with
// `@import "tailwindcss" source(none)`, so it ships the preflight +
// theme + basecoat component classes but ZERO utility classes. The
// browser build scans the DOM at runtime and generates the utilities
// (flex, grid, p-4, ...) the page uses for layout.
//
// Pinned to @4 so jsdelivr serves the latest 4.x release; the browser
// build is stable across 4.x patch releases.
//
// It is a var (not a const) so tests can override it the same way they
// override basecoatJSURL / basecoatStylesURL.
var tailwindBrowserURL = "https://cdn.jsdelivr.net/npm/@tailwindcss/browser@4"

// drainAndClose reads any remaining bytes from body and closes it.
// Discarding the body before close lets the underlying connection be
// returned to the pool for reuse. Errors are ignored — the response
// is already a failure case by the time this is called.
func drainAndClose(body io.ReadCloser) {
	io.Copy(io.Discard, body)
	body.Close()
}

// downloadFile performs a simple HTTP GET with the shared client's
// timeout and writes the body to dst. Used for download-once assets
// (styles.css) where the caller doesn't need to react to remote
// changes. The timeout prevents a hung CDN connection from blocking
// Init forever.
func downloadFile(url, dst string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer drainAndClose(resp.Body)
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
// A failed download (network error, non-200, timeout, or partial
// write) is wrapped with ErrStylesDownload so callers can detect it
// via errors.Is. There is no automatic embedded fallback — Init
// surfaces the error so a first-run CDN failure is visible. Callers
// that want to proceed with the embedded styles fallback can
// construct a *UnionFS directly, passing EmbeddedBasecoatCSS as the
// embeddedCSS argument.
//
// Returns:
//   - path: the absolute path of the cached file (non-empty on
//     success and on cache hit)
//   - data: the styles bytes read from disk on cache hit, nil on the
//     download path (the caller reads the file back via path)
//   - err:  non-nil only when the download failed and no cache copy
//     exists; wrapped with ErrStylesDownload
func ensureBasecoatStyles(cacheDir string) (path string, data []byte, err error) {
	dst := filepath.Join(cacheDir, "basecoat", "styles.css")
	if b, rerr := os.ReadFile(dst); rerr == nil {
		return dst, b, nil
	}
	if err := downloadFile(basecoatStylesURL, dst); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrStylesDownload, err)
	}
	return dst, nil, nil
}

// ensureBasecoatJS fetches the latest basecoat.js runtime from
// jsdelivr and writes it to {cacheDir}/basecoat/basecoat.js on every
// Init. The file is small (~30-60KB) so re-downloading is cheap; the
// alternative is an ETag round-trip per Init, which adds a network
// hop to save a few KB. We pick simplicity.
//
// Fallback chain on download failure (network error, non-200, or
// timeout):
//  1. If a previous cache copy exists at {cacheDir}/basecoat/basecoat.js
//     it is served and err is nil — the runtime is just a version
//     behind, which is acceptable.
//  2. Otherwise the caller is expected to supply the embedded
//     //go:embed byte slice. The error is wrapped with ErrJSDownload
//     so the caller can detect the failure via errors.Is and decide
//     whether to abort or proceed with the embedded fallback. The
//     returned data is nil in this case; the caller owns the fallback.
//
// Returns:
//   - path: the absolute path of the cached file (non-empty on success
//     and on cache-only fallback after a transient network failure)
//   - data: the runtime bytes, ready to prepend to basecoat.js
//   - err:  non-nil only when the download failed AND no cache copy
//     exists; wrapped with ErrJSDownload
func ensureBasecoatJS(cacheDir string) (path string, data []byte, err error) {
	dst := filepath.Join(cacheDir, "basecoat", "basecoat.js")

	resp, err := httpClient.Get(basecoatJSURL)
	if err != nil {
		// Network failure: serve whatever's on disk, if anything.
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("%w: %v", ErrJSDownload, err)
	}
	defer drainAndClose(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Non-200 (e.g. CDN outage): serve cache, no error.
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("%w: HTTP %d fetching %s", ErrJSDownload, resp.StatusCode, basecoatJSURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("%w: reading body: %v", ErrJSDownload, err)
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

// ensureTailwindBrowser fetches the Tailwind v4 browser build from
// jsdelivr and writes it to {cacheDir}/basecoat/tailwind-browser.js on
// every Init (when IncludeTailwindBrowser is true). The file is
// re-downloaded on each Init the same way ensureBasecoatJS is, so a
// refreshed build is picked up without a server restart. The file is
// larger (~270KB) than the basecoat runtime but still cheap to fetch
// once per Init.
//
// Fallback chain on download failure (network error, non-200, or
// timeout):
//  1. If a previous cache copy exists at
//     {cacheDir}/basecoat/tailwind-browser.js it is served and err is
//     nil — the browser build is just a version behind, which is
//     acceptable.
//  2. Otherwise the error is wrapped with ErrTailwindBrowserDownload
//     and returned. Init treats this as a soft failure: it proceeds
//     without the browser build rather than aborting, so a CDN outage
//     on the tailwind endpoint does not take the server down. The
//     page loses Tailwind utility classes but basecoat components
//     still render.
//
// Returns:
//   - path: the absolute path of the cached file (non-empty on success
//     and on cache-only fallback after a transient network failure)
//   - data: the browser build bytes, ready to prepend to basecoat.js
//   - err:  non-nil only when the download failed AND no cache copy
//     exists; wrapped with ErrTailwindBrowserDownload
func ensureTailwindBrowser(cacheDir string) (path string, data []byte, err error) {
	dst := filepath.Join(cacheDir, "basecoat", "tailwind-browser.js")

	resp, err := httpClient.Get(tailwindBrowserURL)
	if err != nil {
		// Network failure: serve whatever's on disk, if anything.
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("%w: %v", ErrTailwindBrowserDownload, err)
	}
	defer drainAndClose(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Non-200 (e.g. CDN outage): serve cache, no error.
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("%w: HTTP %d fetching %s", ErrTailwindBrowserDownload, resp.StatusCode, tailwindBrowserURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if b, rerr := os.ReadFile(dst); rerr == nil {
			return dst, b, nil
		}
		return "", nil, fmt.Errorf("%w: reading body: %v", ErrTailwindBrowserDownload, err)
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
