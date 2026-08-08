// Entry point for the fixture generator. Run with:
//
//	go run ./testdata/gen
//
// For every scenario in scenarios.go, writes prev.docx/curr.docx under
// testdata/scenarios/<name>/ and converts each to a matching PDF alongside
// it, so the fixture set exercises both ingest paths against real binary
// files instead of only the synthetic .txt fixtures.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	outRoot := "testdata/scenarios"

	for _, sc := range scenarios() {
		dir := filepath.Join(outRoot, sc.name)
		fmt.Printf("== %s ==\n%s\n", sc.name, sc.desc)

		prevDocx := filepath.Join(dir, "prev.docx")
		currDocx := filepath.Join(dir, "curr.docx")

		if err := writeDocx(prevDocx, sc.prev); err != nil {
			fatal("write %s: %v", prevDocx, err)
		}
		fmt.Printf("  wrote %s (%d paragraf)\n", prevDocx, len(sc.prev))

		if err := writeDocx(currDocx, sc.curr); err != nil {
			fatal("write %s: %v", currDocx, err)
		}
		fmt.Printf("  wrote %s (%d paragraf)\n", currDocx, len(sc.curr))

		if err := convertToPDF(prevDocx, dir); err != nil {
			fatal("convert %s: %v", prevDocx, err)
		}
		fmt.Printf("  converted -> %s\n", filepath.Join(dir, "prev.pdf"))

		if err := convertToPDF(currDocx, dir); err != nil {
			fatal("convert %s: %v", currDocx, err)
		}
		fmt.Printf("  converted -> %s\n", filepath.Join(dir, "curr.pdf"))

		fmt.Println()
	}

	fmt.Println("selesai.")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: "+format+"\n", args...)
	os.Exit(1)
}
