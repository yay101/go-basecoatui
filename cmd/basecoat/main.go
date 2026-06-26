package main

import (
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
		mode    = flag.String("mode", "parent", "init mode: parent (downloads styles + js runtime) or child (no network, user content only)")
		cache   = flag.String("cache", "./.basecoat-cache", "download cache directory (parent mode only)")
		sources = multiFlag{}
		output  = flag.String("output", "./dist", "output directory")
		static  = flag.Bool("static", true, "disable file watching (default true for cli)")
	)
	flag.Var(&sources, "source", "source directory (repeatable)")
	flag.Parse()

	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "usage: basecoat --mode=parent|child --source DIR [--source DIR...] [--output DIR] [--cache DIR]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if *mode != "parent" && *mode != "child" {
		fmt.Fprintf(os.Stderr, "invalid --mode %q (want parent or child)\n", *mode)
		os.Exit(1)
	}

	basecoat.Static = *static

	var fses []fs.FS
	for _, s := range sources {
		fses = append(fses, basecoat.Dir(s))
	}

	var (
		ufs basecoat.FS
		err error
	)
	switch *mode {
	case "parent":
		ufs, err = basecoat.Init(*cache, fses...)
	case "child":
		ufs, err = basecoat.InitChild(fses...)
	}
	if err != nil {
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
