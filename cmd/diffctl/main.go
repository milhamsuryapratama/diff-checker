// Command diffctl compares two legal documents and reports the structural
// differences between them.
//
// Everything it does today is deterministic: no API key is required and no
// network call is made. That is deliberate — the numbering and cross-reference
// checks are the part of the product that must be reproducible, and they are
// useful on their own.
//
//	diffctl compare sebelum.docx sesudah.docx
//	diffctl compare sebelum.docx sesudah.docx --json
//	diffctl inspect dokumen.docx
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
	"github.com/milhamsuryapratama/diff-checker/internal/report"
	"github.com/milhamsuryapratama/diff-checker/internal/structure"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "compare":
		os.Exit(runCompare(os.Args[2:]))
	case "inspect":
		os.Exit(runInspect(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "perintah tidak dikenal: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `diffctl — pembanding dokumen legal (mesin deterministik)

Penggunaan:
  diffctl compare <sebelum> <sesudah> [flag]
  diffctl inspect <dokumen> [flag]

Format yang didukung: .docx, .pdf, .txt, .md

Flag compare:
  --json           keluarkan laporan sebagai JSON
  --changes        tampilkan daftar perubahan teks
  --fail-on-major  keluar dengan status bukan nol jika ada temuan mayor

Status keluar:
  0  tidak ada temuan mayor/kritis
  1  ada temuan mayor
  2  ada temuan kritis
`)
}

func runCompare(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "keluarkan laporan sebagai JSON")
	showChanges := fs.Bool("changes", false, "tampilkan daftar perubahan teks")
	failOnMajor := fs.Bool("fail-on-major", false, "keluar dengan status bukan nol jika ada temuan mayor")
	_ = fs.Parse(args)

	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "compare membutuhkan tepat dua berkas")
		return 2
	}

	prev, err := load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	curr, err := load(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}

	rep := report.Build(prev, curr)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "gagal menulis JSON:", err)
			return 3
		}
	} else {
		report.RenderText(os.Stdout, rep, *showChanges)
	}

	if *failOnMajor {
		return report.ExitCode(rep)
	}
	if rep.Summary.Critical > 0 {
		return 2
	}
	return 0
}

// runInspect prints the parsed structure of a single document. It exists because
// the structure parser is the component most worth eyeballing on a real file
// before trusting anything built on top of it.
func runInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "keluarkan struktur sebagai JSON")
	showRefs := fs.Bool("refs", false, "tampilkan seluruh referensi silang")
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "inspect membutuhkan tepat satu berkas")
		return 2
	}

	doc, err := load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc.Root); err != nil {
			fmt.Fprintln(os.Stderr, "gagal menulis JSON:", err)
			return 3
		}
		return 0
	}

	fmt.Printf("%s — %d paragraf, bahasa %q, %d pasal\n\n",
		doc.Source, len(doc.Paragraphs), doc.Lang, structure.ArticleCount(doc))

	printTree(doc.Root, 0)

	if *showRefs {
		fmt.Printf("\nReferensi silang (%d)\n", len(doc.References))
		for _, r := range doc.References {
			kind := "absolut"
			if r.Kind == docmodel.RefRelative {
				kind = "relatif"
			}
			fmt.Printf("  [%d] %-30s -> %-28s (%s)\n", r.ParaIndex, r.Raw, r.TargetID(), kind)
		}
	}
	return 0
}

func printTree(n *docmodel.Node, depth int) {
	if n == nil {
		return
	}
	if n.Kind != docmodel.KindRoot {
		indent := ""
		for i := 0; i < depth-1; i++ {
			indent += "  "
		}
		label := n.Label
		if label == "" {
			label = n.Kind.String() + " " + n.Number
		}
		title := ""
		if n.Title != "" {
			title = "  — " + n.Title
		}
		fmt.Printf("%s%-34s [%d..%d]  %s%s\n", indent, label, n.Start, n.End, n.ID, title)
	}
	for _, c := range n.Children {
		printTree(c, depth+1)
	}
}

func load(path string) (*docmodel.IndexedDoc, error) {
	doc, err := ingest.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca %s: %w", path, err)
	}
	structure.Build(doc)
	return doc, nil
}
