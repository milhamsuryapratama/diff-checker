package ground

import (
	"strings"
	"testing"

	"github.com/milhamsuryapratama/diff-checker/internal/docmodel"
)

func doc(paras ...string) *docmodel.IndexedDoc {
	d := &docmodel.IndexedDoc{Source: "test"}
	for i, p := range paras {
		d.Paragraphs = append(d.Paragraphs, docmodel.Paragraph{Index: i, Text: p})
	}
	return d
}

func replacement(idx int, old, new string) docmodel.Action {
	return docmodel.Action{
		Type:           docmodel.ActionReplacement,
		ParagraphIndex: docmodel.IntPtr(idx),
		Old:            old,
		New:            new,
	}
}

// The whole point of this package: an action quoting text that is not in the
// document must not reach a user, however plausible it reads.
func TestRejectsFabricatedQuote(t *testing.T) {
	d := doc("Pasal 1", "Jangka waktu adalah 30 (tiga puluh) hari kerja.")

	res := Actions(d, []docmodel.Action{
		replacement(1, "90 (sembilan puluh) hari", "60 (enam puluh) hari"),
	})

	if len(res.Accepted) != 0 {
		t.Fatalf("aksi dengan kutipan palsu diterima: %+v", res.Accepted)
	}
	if len(res.Rejections) != 1 {
		t.Fatalf("expected 1 rejection, got %d", len(res.Rejections))
	}
	// The reason must quote what the paragraph really says, so the retry
	// prompt gives the model something to correct against.
	if !strings.Contains(res.Rejections[0].Reason, "30 (tiga puluh) hari") {
		t.Errorf("alasan penolakan tidak memuat isi paragraf sebenarnya: %s",
			res.Rejections[0].Reason)
	}
}

func TestAcceptsVerbatimQuote(t *testing.T) {
	d := doc("Pasal 1", "Jangka waktu adalah 30 (tiga puluh) hari kerja.")

	res := Actions(d, []docmodel.Action{
		replacement(1, "30 (tiga puluh) hari", "60 (enam puluh) hari"),
	})

	if !res.OK() {
		t.Fatalf("kutipan yang benar ditolak: %v", res.Rejections)
	}
	if len(res.Accepted) != 1 {
		t.Fatalf("expected 1 accepted action, got %d", len(res.Accepted))
	}
}

// A model that reproduces a sentence but collapses double spaces is describing
// a real edit, not inventing one. Only differing words should be a rejection.
func TestAcceptsWhitespaceVariation(t *testing.T) {
	d := doc("Pasal 1", "Pembayaran  dilakukan   dalam 14 hari.")

	res := Actions(d, []docmodel.Action{
		replacement(1, "Pembayaran dilakukan dalam 14 hari", "Pembayaran dilakukan dalam 30 hari"),
	})

	if !res.OK() {
		t.Errorf("variasi spasi seharusnya diterima, ditolak: %v", res.Rejections)
	}
}

func TestRejectsOutOfRangeParagraph(t *testing.T) {
	d := doc("Pasal 1", "Isi.")

	res := Actions(d, []docmodel.Action{replacement(99, "Isi", "Isi baru")})

	if len(res.Rejections) != 1 {
		t.Fatalf("expected 1 rejection, got %d", len(res.Rejections))
	}
	if !strings.Contains(res.Rejections[0].Reason, "tidak ada di dokumen") {
		t.Errorf("alasan tidak menyebut indeks di luar jangkauan: %s", res.Rejections[0].Reason)
	}
}

func TestRejectsMissingParagraphIndex(t *testing.T) {
	d := doc("Pasal 1", "Isi.")

	res := Actions(d, []docmodel.Action{{
		Type: docmodel.ActionReplacement, Old: "Isi", New: "Isi baru",
	}})

	if len(res.Rejections) != 1 {
		t.Fatalf("aksi tanpa paragraph_index seharusnya ditolak")
	}
}

func TestRejectsNoOpEdit(t *testing.T) {
	d := doc("Pasal 1", "Jangka waktu 30 hari.")

	res := Actions(d, []docmodel.Action{replacement(1, "30 hari", "30 hari")})

	if len(res.Rejections) != 1 {
		t.Fatalf("edit tanpa perubahan seharusnya ditolak")
	}
	if !strings.Contains(res.Rejections[0].Reason, "identik") {
		t.Errorf("alasan tidak menyebut teks identik: %s", res.Rejections[0].Reason)
	}
}

// Manual review carries no text edit, so it only needs a valid location.
func TestManualReviewNeedsOnlyValidIndex(t *testing.T) {
	d := doc("Pasal 1", "Isi.")

	ok := Actions(d, []docmodel.Action{{
		Type: docmodel.ActionManualReview, ParagraphIndex: docmodel.IntPtr(1),
		Rationale: "perlu ditinjau",
	}})
	if !ok.OK() {
		t.Errorf("manual review dengan indeks valid ditolak: %v", ok.Rejections)
	}

	bad := Actions(d, []docmodel.Action{{
		Type: docmodel.ActionManualReview, ParagraphIndex: docmodel.IntPtr(50),
	}})
	if bad.OK() {
		t.Errorf("manual review dengan indeks tidak valid seharusnya ditolak")
	}
}

// A finding whose actions were all rejected keeps its observation but loses the
// fix, and says so — discarding it entirely would hide a possibly sound concern.
func TestFindingSurvivesWithoutItsRejectedAction(t *testing.T) {
	d := doc("Pasal 1", "Jangka waktu adalah 30 hari.")

	in := []docmodel.Finding{{
		Class:      docmodel.ClassAdvisory,
		Category:   docmodel.CatLegalRisk,
		Severity:   docmodel.SeverityMajor,
		Message:    "Jangka waktu terlalu singkat",
		Confidence: 0.9,
		Actions:    []docmodel.Action{replacement(1, "teks yang tidak ada", "x")},
	}}

	out, rejections := Findings(d, in)

	if len(out) != 1 {
		t.Fatalf("expected finding to survive, got %d", len(out))
	}
	if len(out[0].Actions) != 0 {
		t.Errorf("aksi yang ditolak masih terpasang: %+v", out[0].Actions)
	}
	if out[0].Confidence >= 0.9 {
		t.Errorf("confidence tidak diturunkan setelah aksi ditolak: %v", out[0].Confidence)
	}
	if !strings.Contains(out[0].Message, "tinjauan manual") {
		t.Errorf("pesan tidak menandai bahwa usulan ditolak: %q", out[0].Message)
	}
	if len(rejections) != 1 {
		t.Errorf("expected 1 rejection reported, got %d", len(rejections))
	}
}

func TestFeedbackNamesEveryRejection(t *testing.T) {
	d := doc("Pasal 1", "Isi asli.")

	res := Actions(d, []docmodel.Action{
		replacement(1, "tidak ada", "x"),
		replacement(77, "juga tidak ada", "y"),
	})

	fb := res.Feedback()
	if fb == "" {
		t.Fatal("feedback kosong padahal ada penolakan")
	}
	if !strings.Contains(fb, "#0") || !strings.Contains(fb, "#1") {
		t.Errorf("feedback tidak menyebut kedua aksi: %s", fb)
	}
	if !strings.Contains(fb, "jangan mengarang") {
		t.Errorf("feedback tidak melarang mengarang teks: %s", fb)
	}
}
