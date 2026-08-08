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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/joho/godotenv"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic"
	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
	"github.com/milhamsuryapratama/diff-checker/internal/report"
	"github.com/milhamsuryapratama/diff-checker/internal/structure"
)

func main() {
	// --ai reads ANTHROPIC_API_KEY / DIFF_* the same way the server does; load
	// .env before anything else so both entry points behave identically. A
	// missing .env is not an error — it is optional.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "gagal membaca .env:", err)
		os.Exit(3)
	}

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
  --ai             jalankan lapisan AI (butuh ANTHROPIC_API_KEY / OPENAI_API_KEY)
  --cost           tampilkan rincian biaya token per node (dengan --ai)

Tanpa --ai, diffctl hanya menjalankan mesin deterministik: tanpa API key,
tanpa panggilan jaringan, dan hasilnya dapat direproduksi 100%.

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
	withAI := fs.Bool("ai", false, "jalankan lapisan AI (butuh API key)")
	showCost := fs.Bool("cost", false, "tampilkan rincian biaya token per node")
	_ = fs.Parse(reorderArgs(fs, args))

	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "compare membutuhkan tepat dua berkas")
		return 2
	}

	if *withAI {
		return runCompareAI(fs.Arg(0), fs.Arg(1), *asJSON, *showChanges, *failOnMajor, *showCost)
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

// runCompareAI runs the full graph, including the LLM tier.
//
// It shares the deterministic engine with the plain path — the graph's first
// nodes are exactly report.Build — so turning the AI tier on can add advisory
// findings but can never change a verified one.
func runCompareAI(prevPath, currPath string, asJSON, showChanges, failOnMajor, showCost bool) int {
	registry := models.FromEnv()
	if !registry.Configured() {
		fmt.Fprintf(os.Stderr,
			"lapisan AI butuh API key yang belum diset untuk: %s\n"+
				"set ANTHROPIC_API_KEY (atau OPENAI_API_KEY dengan DIFF_PROVIDER=openai),\n"+
				"atau jalankan tanpa --ai untuk mesin deterministik saja.\n",
			strings.Join(registry.Missing(), ", "))
		return 4
	}

	pipeline, err := agentic.New(registry)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gagal menyiapkan pipeline:", err)
		return 3
	}

	// Progress goes to stderr so that --json on stdout stays machine-readable.
	onProgress := func(p agentic.Progress) {
		if p.Status == "done" {
			fmt.Fprintf(os.Stderr, "  ✓ %s\n", p.Label)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// The CLI shows step completion, not reasoning; a terminal is the wrong
	// place for a token stream competing with the report on stdout.
	res, err := pipeline.Run(ctx, prevPath, currPath, agentic.DefaultOptions(), onProgress, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pipeline gagal:", err)
		return 3
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res.Report); err != nil {
			fmt.Fprintln(os.Stderr, "gagal menulis JSON:", err)
			return 3
		}
	} else {
		fmt.Fprintln(os.Stderr)
		report.RenderText(os.Stdout, res.Report, showChanges)
	}

	if showCost {
		printCost(os.Stderr, res.Usage)
	}
	if failOnMajor {
		return report.ExitCode(res.Report)
	}
	if res.Report.Summary.Critical > 0 {
		return 2
	}
	return 0
}

// printCost renders the per-node token spend.
//
// Cache reads are shown explicitly because the cost model assumes caching
// works: a run whose second and later calls report zero cache reads is a run
// whose prompt prefix is varying, and the budget for it is wrong.
func printCost(w io.Writer, u *agentic.Usage) {
	nodes := u.Nodes()
	if len(nodes) == 0 {
		fmt.Fprintln(w, "\nBiaya: tidak ada panggilan LLM (semua tahap deterministik).")
		return
	}
	fmt.Fprintf(w, "\nBiaya token per node\n")
	fmt.Fprintf(w, "  %-12s %-24s %5s %9s %9s %9s %8s\n",
		"node", "model", "call", "input", "cache-rd", "output", "USD")
	for _, n := range nodes {
		fmt.Fprintf(w, "  %-12s %-24s %5d %9d %9d %9d %8.4f\n",
			n.Node, n.Model, n.Calls, n.Input, n.CacheRead, n.Output, n.USD)
	}
	t := u.Totals()
	fmt.Fprintf(w, "  %-12s %-24s %5d %9d %9d %9d %8.4f\n",
		"TOTAL", "", t.Calls, t.Input, t.CacheRead, t.Output, t.USD)
}

// runInspect prints the parsed structure of a single document. It exists because
// the structure parser is the component most worth eyeballing on a real file
// before trusting anything built on top of it.
func runInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "keluarkan struktur sebagai JSON")
	showRefs := fs.Bool("refs", false, "tampilkan seluruh referensi silang")
	_ = fs.Parse(reorderArgs(fs, args))

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

// reorderArgs moves every flag — and its value, if it takes one — ahead of the
// positional arguments, so a flag can be written anywhere on the command line:
// "compare a.docx b.docx --json" works exactly like "compare --json a.docx
// b.docx". Without this, Go's flag package stops parsing at the first
// non-flag argument and treats everything after it, flags included, as
// positional — which reads as a confusing "membutuhkan tepat dua berkas" error
// when a flag was simply placed after the filenames.
//
// fs must already have every flag registered (via fs.Bool, fs.String, ...)
// before this is called, so boolean flags — which take no following value —
// can be told apart from ones that do.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)

		name := strings.TrimLeft(a, "-")
		if strings.ContainsRune(name, '=') {
			continue // value is embedded ("--json=true"); nothing more to consume
		}
		fl := fs.Lookup(name)
		if fl == nil {
			continue // unknown flag; let fs.Parse report it
		}
		if bf, ok := fl.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue // boolean flags never take a following value
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func load(path string) (*docmodel.IndexedDoc, error) {
	doc, err := ingest.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca %s: %w", path, err)
	}
	structure.Build(doc)
	return doc, nil
}
