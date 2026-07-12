package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
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

// pages maps URL path → fragment template name + human-readable title.
// Adding a page is a one-liner here; the template must be defined in
// one of the basecoat/html/*.html fragments picked up by ParseFS.
var pages = map[string]struct {
	Fragment string
	Title    string
	Subtitle string
}{
	"/": {
		Fragment: "page-home",
		Title:    "Home",
		Subtitle: "Team, cookie, payment and chat cards. Use the menu to swap pages without a reload.",
	},
	"/dashboard": {
		Fragment: "page-dashboard",
		Title:    "Dashboard",
		Subtitle: "A distinct dashboard view — stats, the live clock, and the report form. Navigating here from the menu fetches only this fragment from the server.",
	},
}

func main() {
	basecoat.Static = false
	// Parent mode: downloads the basecoat CDN bundle + runtime from
	// jsdelivr. Fragments live under basecoat/html/ (masked from
	// serving), so template.ParseFS runs against ufs.Unmasked().
	ufs, err := basecoat.Init("./cache",
		basecoat.Watch("./public"),
		basecoat.Watch("./elements"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer ufs.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/team-roles", handleTeamRoles)
	mux.HandleFunc("POST /api/cookie-settings", handleCookieSettings)
	mux.HandleFunc("POST /api/payment-method", handlePaymentMethod)
	mux.HandleFunc("POST /api/chat", handleChat)
	mux.HandleFunc("POST /api/report-issue", handleReportIssue)
	mux.HandleFunc("GET /{$}", handlePage(ufs, "/"))
	mux.HandleFunc("GET /dashboard", handlePage(ufs, "/dashboard"))
	mux.Handle("/basecoat/", http.NotFoundHandler())
	mux.Handle("/", http.FileServer(http.FS(ufs)))

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// handlePage serves either the full HTML shell (index.html with the
// page fragment server-rendered into #app) or, when ?fragment=1 is
// present, just the rendered page fragment body — the SPA navigator
// swaps that into #app on the client. Templates are parsed once
// against ufs.Unmasked() (two globs: root pages + basecoat/html/
// fragments) and cached; a Clone per request keeps it race-free.
func handlePage(ufs basecoat.FS, path string) http.HandlerFunc {
	funcs := template.FuncMap{
		"dict": dict,
		"icon": iconHTML,
	}
	var (
		once     sync.Once
		parsed   *template.Template
		parseErr error
	)
	load := func() {
		t := template.New("").Funcs(funcs)
		t, err := t.ParseFS(ufs.Unmasked(), "*.html", "basecoat/html/*.html")
		if err != nil {
			parseErr = err
			return
		}
		parsed = t
	}
	return func(w http.ResponseWriter, r *http.Request) {
		once.Do(load)
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusInternalServerError)
			return
		}
		t, err := parsed.Clone()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		page, ok := pages[path]
		if !ok {
			http.NotFound(w, r)
			return
		}

		// ?fragment=1 → render only the page fragment body (no shell).
		if r.URL.Query().Get("fragment") == "1" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := t.ExecuteTemplate(w, page.Fragment, nil); err != nil {
				log.Printf("fragment execute: %v", err)
			}
			return
		}

		// Full page: render index.html (the shell), which embeds the
		// home fragment into #app via {{template "page-home" .}}.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := t.ExecuteTemplate(w, "index.html", nil); err != nil {
			log.Printf("page execute: %v", err)
		}
	}
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

// iconHTML wraps an inline SVG string as template.HTML so
// html/template renders it verbatim instead of escaping the markup.
// Use via the "icon" funcmap entry: {{icon "path d=..."}}
func iconHTML(s string) template.HTML {
	return template.HTML(s)
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