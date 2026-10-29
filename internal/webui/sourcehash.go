package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// ComputeSourceHash returns a deterministic hex-encoded SHA-256 digest over
// every file under webDir except its node_modules/ and dist/ subdirectories
// (the only parts of web/ this repository's .gitignore excludes -- so this
// covers exactly the git-tracked frontend source). It is what
// scripts/build-frontend.sh writes to internal/webui/source-hash.txt every
// time it regenerates dist/, and what TestSourceHashMatchesCommittedDist
// recomputes to detect a stale committed dist/ (one that no longer
// corresponds to the current web/ source) without needing Node.js/npm at
// test time -- see that test and source-hash.txt's own comment for why this
// exists.
//
// The hash covers each file's path (relative to webDir, using forward
// slashes) and its contents; files are visited in sorted path order so the
// result does not depend on directory-listing order.
func ComputeSourceHash(webDir string) (string, error) {
	var paths []string
	err := filepath.WalkDir(webDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(webDir, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel == "node_modules" || rel == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking %s: %w", webDir, err)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, rel := range paths {
		slashRel := filepath.ToSlash(rel)
		f, openErr := os.Open(filepath.Join(webDir, rel))
		if openErr != nil {
			return "", fmt.Errorf("opening %s: %w", rel, openErr)
		}
		fmt.Fprintf(h, "path:%s\n", slashRel)
		if _, copyErr := io.Copy(h, f); copyErr != nil {
			f.Close()
			return "", fmt.Errorf("reading %s: %w", rel, copyErr)
		}
		f.Close()
		fmt.Fprint(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
