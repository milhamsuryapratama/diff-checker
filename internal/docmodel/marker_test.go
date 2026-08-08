package docmodel

import "testing"

func TestParseMarker(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantOK  bool
		kind    MarkerKind
		form    MarkerForm
		ordinal int
		token   string
	}{
		// Bracketed — the ayat convention in Indonesian legal drafting.
		{"ayat bracket", "(1) Setiap orang berhak...", true, KindDigit, FormBracket, 1, "1"},
		{"ayat bracket spaced", "( 12 ) Ketentuan...", true, KindDigit, FormBracket, 12, "12"},
		{"huruf bracket", "(a) melakukan kegiatan", true, KindLowerAlpha, FormBracket, 1, "a"},

		// Dot and paren forms.
		{"digit dot", "1. Ketentuan Umum", true, KindDigit, FormDot, 1, "1"},
		{"digit paren", "2) Izin usaha", true, KindDigit, FormParen, 2, "2"},
		{"huruf dot", "a. penambangan", true, KindLowerAlpha, FormDot, 1, "a"},
		{"huruf b", "b. pengolahan", true, KindLowerAlpha, FormDot, 2, "b"},
		{"upper alpha", "B. BAGIAN KEDUA", true, KindUpperAlpha, FormDot, 2, "B"},

		// Hierarchical decimal — the contract convention, e.g. "Pasal 1.1".
		{"decimal two", "1.1 Definisi", true, KindDecimal, FormBare, 1, "1.1"},
		{"decimal three", "2.3.4 Pembayaran", true, KindDecimal, FormBare, 4, "2.3.4"},
		{"decimal dot", "1.2. Ruang Lingkup", true, KindDecimal, FormDot, 2, "1.2"},

		// Roman.
		{"roman upper", "IV. KETENTUAN PERALIHAN", true, KindUpperRoman, FormDot, 4, "IV"},
		{"roman lower multi", "iii. ketiga", true, KindLowerRoman, FormDot, 3, "iii"},

		// False positives that must be rejected — the main risk when parsing prose.
		{"abbrev Dst", "Dst. dan seterusnya", false, 0, 0, 0, ""},
		{"abbrev No mixed case", "No. 3 Tahun 2020", false, 0, 0, 0, ""},
		{"abbrev Dr mixed case", "Dr. Budi Santoso", false, 0, 0, 0, ""},
		{"heading Pasal", "Pasal 12", false, 0, 0, 0, ""},
		{"heading Bab", "BAB I", false, 0, 0, 0, ""},
		{"plain prose", "Perusahaan wajib menyampaikan laporan.", false, 0, 0, 0, ""},
		{"non canonical roman", "iiii. salah", false, 0, 0, 0, ""},
		{"empty", "", false, 0, 0, 0, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseMarker(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ParseMarker(%q) ok = %v, want %v (got %+v)", tc.in, ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got.Kind != tc.kind {
				t.Errorf("kind = %v, want %v", got.Kind, tc.kind)
			}
			if got.Form != tc.form {
				t.Errorf("form = %v, want %v", got.Form, tc.form)
			}
			if got.Ordinal != tc.ordinal {
				t.Errorf("ordinal = %d, want %d", got.Ordinal, tc.ordinal)
			}
			if got.Token != tc.token {
				t.Errorf("token = %q, want %q", got.Token, tc.token)
			}
		})
	}
}

// TestParseMarkerAmbiguity pins the behaviour that mining-legal-backend spends
// roughly 4,800 prompt tokens teaching a model: "i" and "v" read as both alpha
// and roman, and a single marker cannot decide on its own.
func TestParseMarkerAmbiguity(t *testing.T) {
	for _, tok := range []string{"i.", "v.", "x.", "I.", "V."} {
		m, ok := ParseMarker(tok + " sesuatu")
		if !ok {
			t.Fatalf("ParseMarker(%q) failed", tok)
		}
		if !m.Ambiguous {
			t.Errorf("ParseMarker(%q).Ambiguous = false, want true", tok)
		}
	}
	// "ii" is unambiguously roman: "ii" is not a valid 1-2 letter alpha ordinal
	// pair that any list would use, but more importantly it parses as roman 2.
	m, _ := ParseMarker("ii. kedua")
	if m.Kind != KindLowerRoman || m.Ordinal != 2 {
		t.Errorf("ii => kind %v ordinal %d, want L_ROM 2", m.Kind, m.Ordinal)
	}
}

func TestResolveMarkerSequence(t *testing.T) {
	parse := func(ss ...string) []*Marker {
		out := make([]*Marker, 0, len(ss))
		for _, s := range ss {
			m, ok := ParseMarker(s)
			if !ok {
				t.Fatalf("ParseMarker(%q) failed", s)
			}
			out = append(out, &m)
		}
		return out
	}

	t.Run("alpha sequence pins ambiguous i", func(t *testing.T) {
		// a, b, c, ... i  -> the "i" here is alpha 9, not roman 1.
		ms := parse("a. x", "b. x", "c. x", "d. x", "e. x", "f. x", "g. x", "h. x", "i. x")
		ResolveMarkerSequence(ms)
		last := ms[len(ms)-1]
		if last.Kind != KindLowerAlpha || last.Ordinal != 9 {
			t.Errorf("trailing i resolved to %v/%d, want LOWER/9", last.Kind, last.Ordinal)
		}
	})

	t.Run("roman sequence pins ambiguous i", func(t *testing.T) {
		// i, ii, iii -> the "i" is roman 1.
		ms := parse("i. x", "ii. x", "iii. x")
		ResolveMarkerSequence(ms)
		if ms[0].Kind != KindLowerRoman || ms[0].Ordinal != 1 {
			t.Errorf("leading i resolved to %v/%d, want L_ROM/1", ms[0].Kind, ms[0].Ordinal)
		}
	})

	t.Run("all ambiguous defaults to roman", func(t *testing.T) {
		ms := parse("i. x")
		ResolveMarkerSequence(ms)
		if ms[0].Kind != KindLowerRoman {
			t.Errorf("lone i resolved to %v, want L_ROM", ms[0].Kind)
		}
		if ms[0].Ambiguous {
			t.Error("ambiguity should be resolved after ResolveMarkerSequence")
		}
	})
}

func TestRomanRoundTrip(t *testing.T) {
	for n := 1; n <= 200; n++ {
		s := formatRoman(n)
		got, ok := parseRoman(s)
		if !ok || got != n {
			t.Fatalf("roman round-trip failed at %d: %q -> %d ok=%v", n, s, got, ok)
		}
	}
	// Non-canonical forms must be rejected.
	for _, bad := range []string{"iiii", "vv", "ic", "xxxx", "abc", ""} {
		if _, ok := parseRoman(bad); ok {
			t.Errorf("parseRoman(%q) accepted a non-canonical numeral", bad)
		}
	}
}

func TestFormatOrdinalAndRender(t *testing.T) {
	tests := []struct {
		kind MarkerKind
		form MarkerForm
		n    int
		want string
	}{
		{KindDigit, FormBracket, 3, "(3)"},
		{KindDigit, FormDot, 12, "12."},
		{KindLowerAlpha, FormDot, 1, "a."},
		{KindLowerAlpha, FormParen, 27, "aa)"},
		{KindUpperRoman, FormDot, 4, "IV."},
		{KindLowerRoman, FormBracket, 9, "(ix)"},
	}
	for _, tc := range tests {
		if got := Render(tc.kind, tc.form, tc.n); got != tc.want {
			t.Errorf("Render(%v,%v,%d) = %q, want %q", tc.kind, tc.form, tc.n, got, tc.want)
		}
	}
}

func TestParseAlpha(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{{"a", 1}, {"b", 2}, {"z", 26}, {"aa", 27}, {"ab", 28}}
	for _, tc := range tests {
		got, ok := parseAlpha(tc.in)
		if !ok || got != tc.want {
			t.Errorf("parseAlpha(%q) = %d,%v want %d,true", tc.in, got, ok, tc.want)
		}
	}
}
