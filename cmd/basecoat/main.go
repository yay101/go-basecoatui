package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	basecoat "github.com/yay101/go-basecoatui"
)

func main() {
	log.SetFlags(0)

	var (
		cache   = flag.String("cache", "./.basecoat-cache", "download cache directory")
		sources = multiFlag{}
		output  = flag.String("output", "./dist", "output directory")
		version = flag.String("version", "", "basecoat version constraint (e.g. ^0.3.11)")
		static  = flag.Bool("static", true, "disable file watching (default true for cli)")
	)
	flag.Var(&sources, "source", "source directory (repeatable)")
	flag.Parse()

	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "usage: basecoat --source ./components [--source ./public] [--version ^0.3.11]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	basecoat.BasecoatVersion = *version
	basecoat.Static = *static

	var fses []fs.FS
	for _, s := range sources {
		fses = append(fses, basecoat.Dir(s))
	}

	ufs, err := basecoat.Init(*cache, fses...)
	if errors.Is(err, basecoat.ErrUpdateAvailable) {
		log.Println("update available:", err)
	} else if err != nil {
		log.Fatal(err)
	}
	defer ufs.Close()

	if err := os.MkdirAll(*output, 0755); err != nil {
		log.Fatal(err)
	}

	for _, name := range []string{"basecoat.css", "basecoat.js"} {
		src, err := ufs.Open(name)
		if err != nil {
			log.Fatal(err)
		}
		dst, err := os.Create(filepath.Join(*output, name))
		if err != nil {
			log.Fatal(err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			log.Fatal(err)
		}
		src.Close()
		dst.Close()
		log.Printf("wrote %s", filepath.Join(*output, name))
	}
}

// multiFlag implements flag.Value to allow repeated -source flags.
type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprint(*m) }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
