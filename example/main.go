package main

import (
	"errors"
	"log"
	"net/http"

	basecoat "github.com/yay101/go-basecoatui"
)

func main() {
	// Set a version constraint to download + cache basecoat + tailwind CSS.
	// basecoat.BasecoatVersion = "^0.3.11"

	basecoat.AutoUpdate = false
	basecoat.Static = false

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

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", http.FileServer(http.FS(ufs))))
}
