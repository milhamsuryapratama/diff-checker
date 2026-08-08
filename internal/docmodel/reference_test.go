package docmodel

import "testing"

func TestExtractReferences(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []Reference
	}{
		{
			name: "simple pasal",
			text: "Ketentuan sebagaimana dimaksud dalam Pasal 12 berlaku.",
			want: []Reference{{Pasal: "12", Kind: RefAbsolute, Lang: "id"}},
		},
		{
			name: "pasal with ayat",
			text: "sebagaimana dimaksud dalam Pasal 12 ayat (3) wajib dipenuhi",
			want: []Reference{{Pasal: "12", Ayat: "3", Kind: RefAbsolute, Lang: "id"}},
		},
		{
			name: "full citation chain",
			text: "Pasal 5 ayat (2) huruf b angka 1 mengatur hal tersebut.",
			want: []Reference{{Pasal: "5", Ayat: "2", Huruf: "b", Angka: "1", Kind: RefAbsolute, Lang: "id"}},
		},
		{
			name: "decimal clause reference",
			text: "Sesuai dengan Pasal 1.1, para pihak sepakat.",
			want: []Reference{{Pasal: "1.1", Kind: RefAbsolute, Lang: "id"}},
		},
		{
			name: "deep decimal",
			text: "diatur dalam Pasal 2.3.4 perjanjian ini",
			want: []Reference{{Pasal: "2.3.4", Kind: RefAbsolute, Lang: "id"}},
		},
		{
			name: "roman article",
			text: "Sebagaimana Pasal IV tersebut di atas.",
			want: []Reference{{Pasal: "IV", Kind: RefAbsolute, Lang: "id"}},
		},
		{
			name: "english article",
			text: "as set out in Article 12 of this Agreement",
			want: []Reference{{Pasal: "12", Kind: RefAbsolute, Lang: "en"}},
		},
		{
			name: "english section with bare suffix",
			text: "pursuant to Section 5(a) hereof",
			want: []Reference{{Pasal: "5", Huruf: "a", Kind: RefAbsolute, Lang: "en"}},
		},
		{
			name: "two references in one paragraph",
			text: "Pasal 12 dan Pasal 13 dicabut.",
			want: []Reference{
				{Pasal: "12", Kind: RefAbsolute, Lang: "id"},
				{Pasal: "13", Kind: RefAbsolute, Lang: "id"},
			},
		},
		{
			name: "range endpoints both captured",
			text: "Pasal 3 sampai dengan Pasal 7 tetap berlaku.",
			want: []Reference{
				{Pasal: "3", Kind: RefAbsolute, Lang: "id"},
				{Pasal: "7", Kind: RefAbsolute, Lang: "id"},
			},
		},
		{
			name: "relative ayat only",
			text: "Dalam hal sebagaimana dimaksud pada ayat (2), pemegang izin wajib...",
			want: []Reference{{Ayat: "2", Kind: RefRelative, Lang: "id"}},
		},
		{
			name: "relative huruf only",
			text: "kecuali huruf c di atas",
			want: []Reference{{Huruf: "c", Kind: RefRelative, Lang: "id"}},
		},
		{
			name: "ayat inside absolute is not double counted",
			text: "Pasal 9 ayat (4) dan ayat (5) berlaku.",
			want: []Reference{
				{Pasal: "9", Ayat: "4", Kind: RefAbsolute, Lang: "id"},
				{Ayat: "5", Kind: RefRelative, Lang: "id"},
			},
		},
		{
			name: "prose reference is deliberately not matched",
			text: "sebagaimana disebutkan di atas, ketentuan tersebut berlaku",
			want: nil,
		},
		{
			name: "no reference",
			text: "Perusahaan wajib menyampaikan laporan tahunan.",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractReferences(7, tc.text, 0)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d refs, want %d\ngot: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				w := tc.want[i]
				if got[i].Pasal != w.Pasal || got[i].Ayat != w.Ayat ||
					got[i].Huruf != w.Huruf || got[i].Angka != w.Angka ||
					got[i].Kind != w.Kind || got[i].Lang != w.Lang {
					t.Errorf("ref[%d] = %+v, want pasal=%q ayat=%q huruf=%q angka=%q kind=%v lang=%q",
						i, got[i], w.Pasal, w.Ayat, w.Huruf, w.Angka, w.Kind, w.Lang)
				}
				if got[i].ParaIndex != 7 {
					t.Errorf("ref[%d].ParaIndex = %d, want 7", i, got[i].ParaIndex)
				}
				// Offsets must slice back to exactly the raw citation.
				if got[i].Raw != tc.text[got[i].Start:got[i].End] {
					t.Errorf("ref[%d] offsets do not match Raw: %q vs %q",
						i, got[i].Raw, tc.text[got[i].Start:got[i].End])
				}
			}
		})
	}
}

// TestExtractReferencesIgnorePrefix covers the heading case: a paragraph that
// *is* "Pasal 12" must not be recorded as referencing itself.
func TestExtractReferencesIgnorePrefix(t *testing.T) {
	text := "Pasal 12"
	if got := ExtractReferences(0, text, len(text)); len(got) != 0 {
		t.Errorf("heading paragraph yielded %d refs, want 0: %+v", len(got), got)
	}
	// But a heading line that also cites another article still reports that one.
	text2 := "Pasal 12 sebagaimana dimaksud dalam Pasal 30"
	got := ExtractReferences(0, text2, len("Pasal 12"))
	if len(got) != 1 || got[0].Pasal != "30" {
		t.Errorf("got %+v, want a single reference to Pasal 30", got)
	}
}

func TestReferenceTargetID(t *testing.T) {
	tests := []struct {
		ref  Reference
		want string
	}{
		{Reference{Pasal: "12", Lang: "id"}, "pasal:12"},
		{Reference{Pasal: "12", Ayat: "3", Lang: "id"}, "pasal:12/ayat:3"},
		{Reference{Pasal: "5", Ayat: "2", Huruf: "b", Lang: "id"}, "pasal:5/ayat:2/huruf:b"},
		{Reference{Pasal: "12", Lang: "en"}, "article:12"},
		{Reference{Pasal: "1.1", Lang: "id"}, "pasal:1.1"},
		{Reference{}, ""},
	}
	for _, tc := range tests {
		if got := tc.ref.TargetID(); got != tc.want {
			t.Errorf("TargetID(%+v) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

func TestReferenceString(t *testing.T) {
	r := Reference{Pasal: "12", Ayat: "3", Huruf: "a", Lang: "id"}
	if got, want := r.String(), "Pasal 12 ayat (3) huruf a"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	r2 := Reference{Pasal: "7", Lang: "en"}
	if got, want := r2.String(), "Article 7"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
