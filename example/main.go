package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	basecoat "github.com/yay101/go-basecoatui"
)

type apiResponse struct {
	Status  string `json:"status"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
}

func main() {
	basecoat.AutoUpdate = false
	basecoat.Static = false
	basecoat.BasecoatVersion = "^0.3.11"

	ufs, err := basecoat.Init("./cache",
		basecoat.Dir("./public"),
		basecoat.Dir("./elements"),
	)
	if errors.Is(err, basecoat.ErrUpdateAvailable) {
		log.Println("update available:", err)
	} else if err != nil {
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
	mux.Handle("/", http.FileServer(http.FS(ufs)))

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
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
