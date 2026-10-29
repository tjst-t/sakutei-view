// Command gensourcehash computes internal/webui.ComputeSourceHash over the
// web/ frontend source and writes it to internal/webui/source-hash.txt.
// scripts/build-frontend.sh invokes this after every frontend rebuild, so
// source-hash.txt always reflects exactly the web/ source dist/ was built
// from; internal/webui/sourcehash_test.go reads it back to catch a dist/
// that has gone stale relative to web/.
//
// Usage: go run ./scripts/gensourcehash [web-dir output-path]
// With no arguments, defaults to "web" and "internal/webui/source-hash.txt"
// (both relative to the current directory -- run from the repository
// root, same as scripts/build-frontend.sh does).
package main

import (
	"fmt"
	"os"

	"github.com/tjst-t/sakutei-view/internal/webui"
)

func main() {
	webDir := "web"
	outPath := "internal/webui/source-hash.txt"
	switch len(os.Args) {
	case 1:
		// use defaults
	case 3:
		webDir, outPath = os.Args[1], os.Args[2]
	default:
		fmt.Fprintln(os.Stderr, "usage: gensourcehash [web-dir output-path]")
		os.Exit(2)
	}

	hash, err := webui.ComputeSourceHash(webDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gensourcehash: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, []byte(hash+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gensourcehash: writing %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%s)\n", outPath, hash)
}
