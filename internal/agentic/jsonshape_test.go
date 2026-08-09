package agentic

import (
	"strings"
	"testing"
)

// The shape block is the only thing telling an Anthropic-backed model what the
// field names are, so it must actually contain them.
func TestShapeNamesEveryField(t *testing.T) {
	got := shapeOf(&TriageVerdict{})
	for _, want := range []string{`"substantive"`, `"change_ids"`, `"reason"`} {
		if !strings.Contains(got, want) {
			t.Errorf("shape missing %s:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "true|false") {
		t.Errorf("bool field not rendered as a choice:\n%s", got)
	}
	// jsonschema descriptions carry the per-field guidance and should survive.
	if !strings.Contains(got, "//") {
		t.Errorf("field notes dropped:\n%s", got)
	}
}

func TestShapeHandlesNestedSlices(t *testing.T) {
	got := shapeOf(&AnalysisResult{})
	for _, want := range []string{`"changes"`, `"change_id"`, `"kind"`, `"severity"`, `"confidence"`} {
		if !strings.Contains(got, want) {
			t.Errorf("nested shape missing %s:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "[") || !strings.Contains(got, "...]") {
		t.Errorf("slice not rendered as an array:\n%s", got)
	}
}

func TestContractDemandsExactNames(t *testing.T) {
	c := contractFor(&RecommendationResult{})
	if !strings.Contains(c, "nama field yang sama persis") {
		t.Errorf("contract does not pin field names:\n%s", c)
	}
	if !strings.Contains(c, `"recommendations"`) {
		t.Errorf("contract omits the shape:\n%s", c)
	}
}

// Descriptions enumerate allowed values with commas; truncating at the first
// one hid exactly the choices the model needs.
func TestFieldNoteKeepsCommas(t *testing.T) {
	got := shapeOf(&AnalysisResult{})
	if !strings.Contains(got, "critical, major, minor, or info") {
		t.Errorf("severity values truncated:\n%s", got)
	}
}

// A comma after the comment reads as though the comment text ends in one.
func TestShapeCommaPrecedesComment(t *testing.T) {
	for _, line := range strings.Split(shapeOf(&TriageVerdict{}), "\n") {
		if i := strings.Index(line, "//"); i >= 0 && strings.HasSuffix(line, ",") {
			t.Errorf("comma trails the comment: %q", line)
		}
	}
}
