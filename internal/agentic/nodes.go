package agentic

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/graph"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/agentic/prompts"
	"github.com/milhamsuryapratama/diff-checker/internal/agentic/tools"
	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
	"github.com/milhamsuryapratama/diff-checker/internal/ground"
	"github.com/milhamsuryapratama/diff-checker/internal/ingest"
	"github.com/milhamsuryapratama/diff-checker/internal/report"
	"github.com/milhamsuryapratama/diff-checker/internal/structure"
)

// Node IDs. Exported so the UI can render a progress checklist that matches the
// graph exactly rather than a hand-maintained copy of it.
const (
	// NodeStart is a no-op entry node that exists so the two ingest branches
	// can fan out from a conditional edge.
	NodeStart      = "start_run"
	NodeIngestPrev = "ingest_prev"
	NodeIngestCurr = "ingest_curr"
	NodeDeterm     = "deterministic"
	NodeTriage     = "triage"
	NodeAnalyze    = "analyze"
	NodeRecommend  = "recommend"
	NodeAssemble   = "assemble"
)

// NodeLabels are the Indonesian captions shown on the progress page.
var NodeLabels = map[string]string{
	NodeIngestPrev: "Membaca dokumen lama",
	NodeIngestCurr: "Membaca dokumen baru",
	NodeDeterm:     "Analisis deterministik (penomoran + rujukan)",
	NodeTriage:     "Penyaringan perubahan",
	NodeAnalyze:    "Klasifikasi makna perubahan",
	NodeRecommend:  "Analisis risiko + rekomendasi",
	NodeAssemble:   "Menyusun laporan",
}

// OrderedNodes is the display order for the progress checklist.
var OrderedNodes = []string{
	NodeIngestPrev, NodeIngestCurr, NodeDeterm,
	NodeTriage, NodeAnalyze, NodeRecommend, NodeAssemble,
}

// startNode is the graph's entry point and does no work of its own.
func startNode(ctx context.Context, s graph.State) (any, error) { return nil, nil }

// ingestNode parses one document. The two ingest nodes run concurrently and
// write disjoint state keys, which is why they can share this implementation.
func ingestNode(side docmodel.Side) graph.NodeFunc {
	return func(ctx context.Context, s graph.State) (any, error) {
		st := Wrap(s)
		path := st.CurrPath()
		if side == docmodel.SidePrev {
			path = st.PrevPath()
		}
		if path == "" {
			return nil, fmt.Errorf("jalur dokumen %s kosong", side)
		}

		doc, err := ingest.Parse(path)
		if err != nil {
			return nil, fmt.Errorf("gagal membaca %s: %w", path, err)
		}
		structure.Build(doc)

		if side == docmodel.SidePrev {
			return setPrevDoc(doc), nil
		}
		return setCurrDoc(doc), nil
	}
}

// deterministicNode runs the entire Fase 1 engine: text diff, numbering
// validation, reference integrity.
//
// Every finding it produces is ClassVerified and none of it costs a token. It
// runs before the LLM tier — and unconditionally, even when the AI tier is
// disabled — because it is the part of the product that must always work.
func deterministicNode(ctx context.Context, s graph.State) (any, error) {
	prev, curr, err := Wrap(s).Docs()
	if err != nil {
		return nil, err
	}
	return setReport(report.Build(prev, curr)), nil
}

// triageNode is the cheap gate in front of the expensive tiers.
func (p *Pipeline) triageNode(ctx context.Context, s graph.State) (any, error) {
	st := Wrap(s)
	rep := st.Report()
	if rep == nil {
		return nil, fmt.Errorf("laporan deterministik belum tersedia")
	}

	opts := st.Options()
	if opts.NoLLM {
		// Deterministic-only run. The graph still executes end to end — which
		// is what makes this path worth having as more than a flag: the same
		// wiring is exercised, just without reaching a provider.
		return setTriage(&TriageVerdict{
			Substantive: false,
			Reason:      "Mode deterministik: lapisan AI dimatikan.",
		}), nil
	}

	substantive := substantiveChanges(rep.Changes, opts.MaxAdvisory)
	if len(substantive) == 0 {
		// Nothing for a model to look at. Skip the call entirely rather than
		// paying to be told there is nothing to do.
		return setTriage(&TriageVerdict{
			Substantive: false,
			Reason:      "Tidak ada perubahan teks substantif; hanya perbedaan kosmetik atau tidak ada perubahan sama sekali.",
		}), nil
	}

	c, err := p.caller(models.TierTriage, NodeTriage, st.Usage())
	if err != nil {
		return nil, err
	}

	var out TriageVerdict
	user := renderChangeList(substantive, 220)
	if err := c.completeJSON(ctx, prompts.Triage, user, &out,
		"Verdict apakah ada perubahan substantif"); err != nil {
		return nil, err
	}

	// A reply that decoded into an entirely empty verdict is not a verdict.
	// Go's json.Unmarshal ignores unknown fields, so a model answering with
	// different field names yields exactly this: Substantive=false with no
	// reason and no IDs — indistinguishable, until you look, from a confident
	// "nothing substantive here". Treating it as a real answer is how the whole
	// AI tier silently no-ops. Fail open instead: analyse everything, since the
	// cost of examining a cosmetic change is money and the cost of skipping a
	// substantive one is a missed legal risk.
	if !out.Substantive && out.Reason == "" && len(out.ChangeIDs) == 0 {
		return setTriage(&TriageVerdict{
			Substantive: true,
			ChangeIDs:   changeIDs(substantive),
			Reason:      "Hasil penyaringan tidak dapat dibaca; seluruh perubahan dianalisis untuk keamanan.",
		}), nil
	}

	// A model that says "substantive" but names no changes has contradicted
	// itself. Fall back to every candidate rather than silently analysing none.
	if out.Substantive && len(out.ChangeIDs) == 0 {
		out.ChangeIDs = changeIDs(substantive)
	}
	out.ChangeIDs = intersectIDs(out.ChangeIDs, substantive)
	return setTriage(&out), nil
}

// analyzeNode classifies what each change means. This is the agentic node: it
// receives a summary and pulls its own context through the tool set.
func (p *Pipeline) analyzeNode(ctx context.Context, s graph.State) (any, error) {
	st := Wrap(s)
	prev, curr, err := st.Docs()
	if err != nil {
		return nil, err
	}
	rep, tri := st.Report(), st.Triage()
	if rep == nil || tri == nil {
		return nil, fmt.Errorf("triage belum dijalankan")
	}

	selected := selectChanges(rep.Changes, tri.ChangeIDs)
	if len(selected) == 0 {
		return setAnalysis(&AnalysisResult{}), nil
	}

	c, err := p.caller(models.TierAnalyze, NodeAnalyze, st.Usage())
	if err != nil {
		return nil, err
	}

	toolSet := tools.New(prev, curr)
	user := strings.Join([]string{
		verifiedFactsBlock(rep),
		"",
		"Perubahan yang perlu dianalisis:",
		renderChangeList(selected, 600),
	}, "\n")

	var out AnalysisResult
	if err := c.completeJSONWithTools(ctx, prompts.Analyze, user, toolSet.Tools(), &out,
		"Klasifikasi makna setiap perubahan"); err != nil {
		return nil, err
	}
	out.Changes = keepValidAnalyses(out.Changes, selected)
	return setAnalysis(&out), nil
}

// recommendNode produces the legal-risk findings, then re-asks once if the
// grounding validator rejected every action it proposed.
func (p *Pipeline) recommendNode(ctx context.Context, s graph.State) (any, error) {
	st := Wrap(s)
	_, curr, err := st.Docs()
	if err != nil {
		return nil, err
	}
	rep, an := st.Report(), st.Analysis()
	if rep == nil {
		return nil, fmt.Errorf("laporan deterministik belum tersedia")
	}
	if an == nil || len(an.Changes) == 0 {
		return setRecommendations(&RecommendationResult{}), nil
	}

	// Cosmetic classifications never reach this tier: the whole point of
	// classifying first is to avoid paying the most expensive model to read a
	// typo fix.
	risky := riskyAnalyses(an.Changes)
	if len(risky) == 0 {
		return setRecommendations(&RecommendationResult{}), nil
	}

	c, err := p.caller(models.TierRecommend, NodeRecommend, st.Usage())
	if err != nil {
		return nil, err
	}

	base := strings.Join([]string{
		verifiedFactsBlock(rep),
		"",
		"Perubahan yang sudah diklasifikasikan:",
		renderAnalyses(risky, rep.Changes, curr),
	}, "\n")

	var out RecommendationResult
	if err := c.completeJSON(ctx, prompts.Recommend, base, &out,
		"Risiko hukum dan rekomendasi tindakan"); err != nil {
		return nil, err
	}

	// Layer 2. An action the validator cannot locate in the document is not
	// shown; when every action fails, the model is told exactly what was wrong
	// and asked once more. mining-legal-backend drops bad actions silently, so
	// it makes the same mistake on every run.
	res := ground.Actions(curr, actionsOf(out.Recommendations, curr))
	if !res.OK() && len(res.Accepted) == 0 {
		var retry RecommendationResult
		retryUser := base + "\n\n" + res.Feedback()
		if err := c.completeJSON(ctx, prompts.Recommend, retryUser, &retry,
			"Risiko hukum dan rekomendasi tindakan"); err == nil {
			out = retry
		}
	}
	return setRecommendations(&out), nil
}

// assembleNode folds the advisory findings into the verified report.
//
// The two classes stay distinguishable on every finding: this node may append
// ClassAdvisory findings, and may never touch a ClassVerified one.
func (p *Pipeline) assembleNode(ctx context.Context, s graph.State) (any, error) {
	st := Wrap(s)
	rep := st.Report()
	if rep == nil {
		return nil, fmt.Errorf("laporan deterministik belum tersedia")
	}
	_, curr, err := st.Docs()
	if err != nil {
		return nil, err
	}

	if tri := st.Triage(); tri != nil && !tri.Substantive {
		// Only claim the changes were examined and found harmless when they
		// actually were. With the AI tier switched off nothing looked at
		// meaning at all, and saying otherwise would overstate what the report
		// covers — the opposite of what the verified/advisory split is for.
		if !st.Options().NoLLM {
			rep.AddFinding(docmodel.Finding{
				Class:      docmodel.ClassAdvisory,
				Category:   docmodel.CatMeaningChange,
				Severity:   docmodel.SeverityInfo,
				Message:    "Tidak ada perubahan substantif terdeteksi. " + tri.Reason,
				Confidence: 1,
			})
		}
		docmodel.SortFindings(rep.Findings)
		st.Sink().setReport(rep)
		return setReport(rep), nil
	}

	advisory := buildAdvisory(st.Analysis(), st.Recommendations(), rep.Changes)

	// Every advisory finding passes the grounding validator before it lands in
	// the report a user reads.
	validated, _ := ground.Findings(curr, advisory)
	for _, f := range validated {
		rep.AddFinding(f)
	}
	docmodel.SortFindings(rep.Findings)
	st.Sink().setReport(rep)
	return setReport(rep), nil
}

// --- helpers ---------------------------------------------------------------

// substantiveChanges filters out cosmetic diffs and caps the batch size, which
// is the hard ceiling on what a single job can cost.
func substantiveChanges(changes []docmodel.Change, limit int) []docmodel.Change {
	var out []docmodel.Change
	for _, c := range changes {
		if c.Cosmetic {
			continue
		}
		out = append(out, c)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func changeIDs(cs []docmodel.Change) []int {
	out := make([]int, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

// intersectIDs keeps only IDs that name a change actually offered to the model,
// so a hallucinated ID cannot pull an unrelated change into the analysis.
func intersectIDs(ids []int, cs []docmodel.Change) []int {
	valid := map[int]bool{}
	for _, c := range cs {
		valid[c.ID] = true
	}
	var out []int
	for _, id := range ids {
		if valid[id] {
			out = append(out, id)
		}
	}
	return out
}

func selectChanges(all []docmodel.Change, ids []int) []docmodel.Change {
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []docmodel.Change
	for _, c := range all {
		if want[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// keepValidAnalyses drops entries whose change_id or taxonomy value is not
// real. This is grounding applied to classification: a class outside the closed
// enum is not something to coerce into the nearest match, it is a signal the
// answer is unreliable.
func keepValidAnalyses(got []AnalyzedChange, offered []docmodel.Change) []AnalyzedChange {
	valid := map[int]bool{}
	for _, c := range offered {
		valid[c.ID] = true
	}
	var out []AnalyzedChange
	for _, a := range got {
		if !valid[a.ChangeID] || !a.Kind.Valid() {
			continue
		}
		a.Severity = normalizeSeverity(a.Severity)
		out = append(out, a)
	}
	return out
}

func riskyAnalyses(as []AnalyzedChange) []AnalyzedChange {
	var out []AnalyzedChange
	for _, a := range as {
		if a.Kind.Cosmetic() || a.Severity == string(docmodel.SeverityInfo) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// verifiedFactsBlock hands the deterministic findings to the model as settled
// facts.
//
// This is the inversion that keeps the prompts small: the model is not asked to
// find numbering gaps or dangling references, it is told which ones exist so it
// does not waste tokens rediscovering them — or contradict them.
func verifiedFactsBlock(rep *docmodel.Report) string {
	var b strings.Builder
	b.WriteString("Fakta terverifikasi dari mesin deterministik (JANGAN dibantah atau diulang):\n")
	fmt.Fprintf(&b, "- Jumlah pasal: %d -> %d\n", rep.PrevArticleCount, rep.CurrArticleCount)
	fmt.Fprintf(&b, "- Perubahan teks: %d ditambah, %d dihapus, %d diubah\n",
		rep.Summary.Added, rep.Summary.Removed, rep.Summary.Modified)

	// The numbering plan is stated as settled arithmetic. Without it the model
	// sees the individual defects and starts proposing its own numbers, which
	// then contradict the plan the report actually applies.
	for _, line := range rep.Renumbering {
		fmt.Fprintf(&b, "- RENCANA PENOMORAN (sudah final, jangan diubah): %s\n", line)
	}

	n := 0
	for _, f := range rep.Findings {
		if f.Class != docmodel.ClassVerified {
			continue
		}
		if n >= 20 {
			fmt.Fprintf(&b, "- (dan %d temuan terverifikasi lainnya)\n",
				rep.Summary.Verified-n)
			break
		}
		fmt.Fprintf(&b, "- [%s/%s] %s\n", f.Severity, f.Category, f.Message)
		n++
	}
	if n == 0 {
		b.WriteString("- Tidak ada masalah penomoran atau rujukan silang.\n")
	}
	return b.String()
}

func renderChangeList(cs []docmodel.Change, clipAt int) string {
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "\n#%d %s", c.ID, c.Type)
		if c.Context != "" {
			fmt.Fprintf(&b, " — %s", c.Context)
		}
		if len(c.CurrIndexes) > 0 {
			fmt.Fprintf(&b, " [paragraf %d]", c.CurrIndexes[0])
		}
		b.WriteByte('\n')
		if c.PrevText != "" {
			fmt.Fprintf(&b, "  lama: %s\n", clipText(c.PrevText, clipAt))
		}
		if c.CurrText != "" {
			fmt.Fprintf(&b, "  baru: %s\n", clipText(c.CurrText, clipAt))
		}
	}
	return b.String()
}

func renderAnalyses(as []AnalyzedChange, all []docmodel.Change, curr *docmodel.IndexedDoc) string {
	byID := map[int]docmodel.Change{}
	for _, c := range all {
		byID[c.ID] = c
	}
	var b strings.Builder
	for _, a := range as {
		c, ok := byID[a.ChangeID]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "\n#%d [%s / %s] %s\n", a.ChangeID, a.Kind, a.Severity, a.Summary)
		if c.Context != "" {
			fmt.Fprintf(&b, "  lokasi: %s\n", c.Context)
		}
		if len(c.CurrIndexes) > 0 {
			idx := c.CurrIndexes[0]
			fmt.Fprintf(&b, "  paragraf [%d]: %s\n", idx, clipText(paragraphText(curr, idx), 600))
		}
		if c.PrevText != "" {
			fmt.Fprintf(&b, "  redaksi lama: %s\n", clipText(c.PrevText, 400))
		}
	}
	return b.String()
}

func paragraphText(doc *docmodel.IndexedDoc, i int) string {
	if doc == nil || i < 0 || i >= len(doc.Paragraphs) {
		return ""
	}
	return doc.Paragraphs[i].Text
}

// actionsOf collects the literal edits out of a recommendation set so they can
// be validated as a batch.
func actionsOf(recs []Recommendation, curr *docmodel.IndexedDoc) []docmodel.Action {
	var out []docmodel.Action
	for _, r := range recs {
		if r.Old == "" || r.ParagraphIndex == nil {
			continue
		}
		out = append(out, docmodel.Action{
			Type:           docmodel.ActionReplacement,
			ParagraphIndex: r.ParagraphIndex,
			Old:            r.Old,
			New:            r.New,
			Rationale:      r.Recommendation,
		})
	}
	return out
}

// buildAdvisory turns the LLM tier's output into findings.
//
// Recommendations carry the risk analysis, so they take precedence; a change
// that was classified but drew no recommendation is reported at info level so
// the reviewer can still see it was looked at and judged harmless.
func buildAdvisory(an *AnalysisResult, rec *RecommendationResult, changes []docmodel.Change) []docmodel.Finding {
	byID := map[int]docmodel.Change{}
	for _, c := range changes {
		byID[c.ID] = c
	}

	var out []docmodel.Finding
	covered := map[int]bool{}

	if rec != nil {
		for i, r := range rec.Recommendations {
			f := docmodel.Finding{
				ID:         fmt.Sprintf("advisory-risk-%d", i+1),
				Class:      docmodel.ClassAdvisory,
				Category:   docmodel.CatLegalRisk,
				Severity:   docmodel.Severity(normalizeSeverity(r.Severity)),
				Message:    strings.TrimSpace(r.Risk),
				Confidence: clamp01(r.Confidence),
			}
			if c, ok := byID[r.ChangeID]; ok {
				covered[r.ChangeID] = true
				f.NodeID = c.NodeID
				if len(c.CurrIndexes) > 0 {
					f.ParaIndex = docmodel.IntPtr(c.CurrIndexes[0])
				}
			}
			if r.ParagraphIndex != nil {
				f.ParaIndex = r.ParagraphIndex
			}
			if rec := strings.TrimSpace(r.Recommendation); rec != "" {
				f.Evidence = append(f.Evidence, "Rekomendasi: "+rec)
			}
			if r.Old != "" && r.ParagraphIndex != nil {
				f.Actions = append(f.Actions, docmodel.Action{
					Type:           docmodel.ActionReplacement,
					ParagraphIndex: r.ParagraphIndex,
					Old:            r.Old,
					New:            r.New,
					Rationale:      strings.TrimSpace(r.Recommendation),
				})
			}
			out = append(out, f)
		}
	}

	if an != nil {
		for _, a := range an.Changes {
			if covered[a.ChangeID] {
				continue
			}
			f := docmodel.Finding{
				ID:         fmt.Sprintf("advisory-change-%d", a.ChangeID),
				Class:      docmodel.ClassAdvisory,
				Category:   docmodel.CatMeaningChange,
				Severity:   docmodel.Severity(normalizeSeverity(a.Severity)),
				Message:    fmt.Sprintf("[%s] %s", a.Kind, strings.TrimSpace(a.Summary)),
				Confidence: clamp01(a.Confidence),
			}
			if c, ok := byID[a.ChangeID]; ok {
				f.NodeID = c.NodeID
				if len(c.CurrIndexes) > 0 {
					f.ParaIndex = docmodel.IntPtr(c.CurrIndexes[0])
				}
			}
			out = append(out, f)
		}
	}
	return out
}

// normalizeSeverity maps an arbitrary model string onto the closed set. An
// unrecognised value becomes "minor" rather than being trusted: an unknown
// severity must never be able to present itself as critical.
func normalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "kritis":
		return string(docmodel.SeverityCritical)
	case "major", "mayor":
		return string(docmodel.SeverityMajor)
	case "info":
		return string(docmodel.SeverityInfo)
	default:
		return string(docmodel.SeverityMinor)
	}
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
