package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"sync"

	basecoat "github.com/yay101/go-basecoatui"
)

type apiResponse struct {
	Status  string `json:"status"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
}

func main() {
	basecoat.Static = false
	// Parent mode: downloads the prebuilt basecoat styles from
	// basecoatui.com and the latest basecoat.js runtime from jsdelivr.
	// basecoat.css already includes the Tailwind v4 preflight and
	// theme layer, so no Tailwind browser script is needed.
	//
	// We keep references to the raw source fs.FS values we hand to
	// Init so the index handler can glob them directly for fragments
	// — fragments live under each source's basecoat/html/ tree, which
	// the library masks out of the union FS so they never appear at a
	// URL. To still pick them up via template.ParseFS we walk the raw
	// source instead.
	publicFS := basecoat.Dir("./public")
	elementsFS := basecoat.Dir("./elements")
	ufs, err := basecoat.Init("./cache", publicFS, elementsFS)
	if err != nil {
		log.Fatal(err)
	}
	defer ufs.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/team-roles", handleTeamRoles)
	mux.HandleFunc("POST /api/cookie-settings", handleCookieSettings)
	mux.HandleFunc("POST /api/payment-method", handlePaymentMethod)
	mux.HandleFunc("POST /api/chat", handleChat)
	mux.HandleFunc("POST /api/create-account", handleCreateAccount)
	mux.HandleFunc("POST /api/report-issue", handleReportIssue)
	mux.HandleFunc("GET /{$}", handleIndex(ufs, elementsFS))
	// /basecoat/ is the reserved namespace — any request to it that
	// wasn't served as the two virtual files at the root (/basecoat.css
	// and /basecoat.js) must 404. The file server below still serves
	// those two because their paths are at the root, not under /basecoat/.
	mux.Handle("/basecoat/", http.NotFoundHandler())
	mux.Handle("/", http.FileServer(http.FS(ufs)))

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// handleIndex renders the SPA shell. The page is parsed out of the
// union FS (which masks the reserved basecoat/ namespace) and the
// fragments are parsed out of the elements source's raw fs.FS using a
// second glob. The two are composed by re-parsing the fragment files
// into the page template so the page's {{template "name" .}} lookups
// resolve. The result is cached behind a sync.Once and invalidated by
// the next Reload (which the poll watcher triggers on file changes).
func handleIndex(ufs basecoat.FS, elementsFS fs.FS) http.HandlerFunc {
	const (
		pageGlob = "**/*.html"
		fragGlob = "basecoat/html/*.html"
	)
	var (
		once     sync.Once
		parsed   *template.Template
		parseErr error
	)
	load := func() {
		// Page from the union FS (excludes basecoat/...).
		pageTmpl, err := template.ParseFS(ufs, pageGlob)
		if err != nil {
			parseErr = err
			return
		}
		// Fragments from the elements source's raw fs.FS — this is
		// the bit that uses template.ParseFS with another glob. The
		// fragment files live under basecoat/html/ which the union
		// FS masks, but the raw source still has them.
		parsed, parseErr = composeWithFragments(pageTmpl, elementsFS, fragGlob)
	}
	funcs := template.FuncMap{
		"dict": dict,
	}
	return func(w http.ResponseWriter, r *http.Request) {
		once.Do(load)
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusInternalServerError)
			return
		}
		// t.Clone gives us a fresh template with funcs registered
		// without re-parsing the underlying files.
		t, err := parsed.Clone()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		t = t.Funcs(funcs)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := t.Execute(w, nil); err != nil {
			log.Printf("template execute: %v", err)
		}
	}
}

// composeWithFragments returns pageTmpl with the fragment files
// matched by fragGlob re-parsed into it, so {{template "name" .}}
// lookups in the page resolve. html/template has no t.ParseFS, so we
// read each matched file and call t.Parse on the body.
func composeWithFragments(pageTmpl *template.Template, fragmentsFS fs.FS, fragGlob string) (*template.Template, error) {
	matches, err := fs.Glob(fragmentsFS, fragGlob)
	if err != nil {
		return nil, err
	}
	for _, match := range matches {
		f, err := fragmentsFS.Open(match)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		if pageTmpl, err = pageTmpl.Parse(string(data)); err != nil {
			return nil, err
		}
	}
	return pageTmpl, nil
}

// dict builds a map[string]any from alternating key/value pairs, for
// use with {{template "name" dict "Key" "Val" ...}} in html/template.
func dict(values ...any) map[string]any {
	if len(values)%2 != 0 {
		panic("dict: expected key/value pairs")
	}
	m := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		k, ok := values[i].(string)
		if !ok {
			panic(fmt.Sprintf("dict: key at index %d is not a string", i))
		}
		m[k] = values[i+1]
	}
	return m
}

func readJSON(r *http.Request, out interface{}) error {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func writeJSON(w http.ResponseWriter, status int, resp apiResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func handleTeamRoles(w http.ResponseWriter, r *http.Request) {
	var data struct {
		Member string `json:"member"`
		Role   string `json:"role"`
	}
	if err := readJSON(r, &data); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Status: "error", Message: err.Error()})
		return
	}
	log.Printf("team-roles: member=%q role=%q", data.Member, data.Role)
	writeJSON(w, http.StatusOK, apiResponse{Status: "ok", Title: "Role updated"})
}

func handleCookieSettings(w http.ResponseWriter, r *http.Request) {
	var data map[string]bool
	if err := readJSON(r, &data); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Status: "error", Message: err.Error()})
		return
	}
	log.Printf("cookie-settings: %v", data)
	writeJSON(w, http.StatusOK, apiResponse{Status: "ok", Title: "Preferences saved"})
}

func handlePaymentMethod(w http.ResponseWriter, r *http.Request) {
	var data map[string]string
	if err := readJSON(r, &data); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Status: "error", Message: err.Error()})
		return
	}
	log.Printf("payment-method: %v", data)
	writeJSON(w, http.StatusOK, apiResponse{Status: "ok", Message: "Payment methods are not really stored"})
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var data struct {
		Message string `json:"message"`
	}
	if err := readJSON(r, &data); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Status: "error", Message: err.Error()})
		return
	}
	log.Printf("chat: message=%q", data.Message)
	writeJSON(w, http.StatusOK, apiResponse{Status: "ok", Title: "Sent"})
}

func handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var data map[string]interface{}
	if err := readJSON(r, &data); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Status: "error", Message: err.Error()})
		return
	}
	log.Printf("create-account: %v", data)
	writeJSON(w, http.StatusOK, apiResponse{
		Status:  "ok",
		Message: "Account creation is not really wired up in this example",
	})
}

func handleReportIssue(w http.ResponseWriter, r *http.Request) {
	var data map[string]string
	if err := readJSON(r, &data); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Status: "error", Message: err.Error()})
		return
	}
	log.Printf("report-issue: %v", data)
	writeJSON(w, http.StatusOK, apiResponse{
		Status:  "ok",
		Message: "Issue reports are not really sent in this example",
	})
}
