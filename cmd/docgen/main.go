// Command docgen regenerates the documentation data extracted from code.
//
//	go run ./cmd/docgen          write docs/.vitepress/data/generated and samples
//	go run ./cmd/docgen -check   exit 1 if the committed files are stale
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"tadmin/internal/docgen"
)

func main() {
	check := flag.Bool("check", false, "only report whether generated files are up to date")
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	files, err := docgen.Generate(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
	stale := docgen.Stale(*root, files)
	if *check {
		if len(stale) > 0 {
			fmt.Fprintln(os.Stderr, "docgen: stale files (run `go run ./cmd/docgen`):")
			for _, s := range stale {
				fmt.Fprintln(os.Stderr, "  ", s)
			}
			os.Exit(1)
		}
		fmt.Println("docgen: documentation data is up to date")
		return
	}
	for _, name := range stale {
		path := filepath.Join(*root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "docgen:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "docgen:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", name)
	}
	if len(stale) == 0 {
		fmt.Println("docgen: nothing to do")
	}
}
