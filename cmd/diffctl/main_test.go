package main

import (
	"flag"
	"reflect"
	"testing"
)

// newCompareFlagSet mirrors the flags runCompare registers, so reorderArgs is
// tested against the exact same bool/value shape it runs against in practice.
func newCompareFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.Bool("json", false, "")
	fs.Bool("changes", false, "")
	fs.Bool("fail-on-major", false, "")
	return fs
}

func TestReorderArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "flag before positionals is unchanged in effect",
			in:   []string{"--json", "a.docx", "b.docx"},
			want: []string{"--json", "a.docx", "b.docx"},
		},
		{
			name: "flag after positionals moves to the front",
			in:   []string{"a.docx", "b.docx", "--json"},
			want: []string{"--json", "a.docx", "b.docx"},
		},
		{
			name: "flag in the middle moves to the front",
			in:   []string{"a.docx", "--json", "b.docx"},
			want: []string{"--json", "a.docx", "b.docx"},
		},
		{
			name: "multiple flags after positionals",
			in:   []string{"a.docx", "b.docx", "--json", "--changes"},
			want: []string{"--json", "--changes", "a.docx", "b.docx"},
		},
		{
			name: "flags scattered on both sides",
			in:   []string{"--changes", "a.docx", "--json", "b.docx"},
			want: []string{"--changes", "--json", "a.docx", "b.docx"},
		},
		{
			name: "embedded value form is left intact",
			in:   []string{"a.docx", "b.docx", "--json=true"},
			want: []string{"--json=true", "a.docx", "b.docx"},
		},
		{
			name: "-- terminates flag scanning",
			in:   []string{"--json", "--", "--not-a-flag.docx", "b.docx"},
			want: []string{"--json", "--not-a-flag.docx", "b.docx"},
		},
		{
			name: "no flags at all",
			in:   []string{"a.docx", "b.docx"},
			want: []string{"a.docx", "b.docx"},
		},
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := reorderArgs(newCompareFlagSet(), tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("reorderArgs(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestReorderArgsThenParse is the regression test for the actual bug: a flag
// placed after the positional filenames must still be recognized once the
// reordered slice reaches flag.FlagSet.Parse.
func TestReorderArgsThenParse(t *testing.T) {
	fs := newCompareFlagSet()
	asJSON := fs.Lookup("json")

	if err := fs.Parse(reorderArgs(fs, []string{"a.docx", "b.docx", "--json"})); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if asJSON.Value.String() != "true" {
		t.Errorf("--json after positionals was not recognized")
	}
	if got := fs.Args(); !reflect.DeepEqual(got, []string{"a.docx", "b.docx"}) {
		t.Errorf("positional args = %v, want [a.docx b.docx]", got)
	}
	if fs.NArg() != 2 {
		t.Errorf("NArg() = %d, want 2", fs.NArg())
	}
}

// TestReorderArgsPreservesNonBoolFlagValue guards a flag that takes a value —
// none exist on diffctl today, but the helper must not swallow a positional
// argument as that flag's value once one is added.
func TestReorderArgsPreservesNonBoolFlagValue(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.String("out", "", "")

	got := reorderArgs(fs, []string{"a.docx", "b.docx", "--out", "report.json"})
	want := []string{"--out", "report.json", "a.docx", "b.docx"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reorderArgs = %v, want %v", got, want)
	}
}
