package docmodel

import (
	"regexp"
	"strconv"
	"strings"
)

// MarkerKind is the symbol family a list marker is drawn from.
type MarkerKind uint8

const (
	KindUnknown    MarkerKind = iota
	KindDigit                 // 1, 2, 3
	KindDecimal               // 1.1, 1.1.2  (hierarchical numeric, common in contracts)
	KindLowerAlpha            // a, b, c
	KindUpperAlpha            // A, B, C
	KindLowerRoman            // i, ii, iii
	KindUpperRoman            // I, II, III
)

func (k MarkerKind) String() string {
	switch k {
	case KindDigit:
		return "DIGIT"
	case KindDecimal:
		return "DECIMAL"
	case KindLowerAlpha:
		return "LOWER"
	case KindUpperAlpha:
		return "UPPER"
	case KindLowerRoman:
		return "L_ROM"
	case KindUpperRoman:
		return "U_ROM"
	}
	return "UNKNOWN"
}

// MarkerForm is how a marker is delimited from the text that follows it.
type MarkerForm uint8

const (
	FormUnknown MarkerForm = iota
	FormDot                // 1.
	FormParen              // 1)
	FormBracket            // (1)
	FormBare               // 1.1 Definisi  (decimal with no trailing delimiter)
)

func (f MarkerForm) String() string {
	switch f {
	case FormDot:
		return "DOT"
	case FormParen:
		return "PAREN"
	case FormBracket:
		return "BRACKET"
	case FormBare:
		return "BARE"
	}
	return "UNKNOWN"
}

// Marker is a list marker parsed from the start of a paragraph.
//
// Ambiguity is preserved rather than guessed at: "i." reads as both lower-alpha
// 9 and lower-roman 1, and only the surrounding sequence can decide. Callers
// resolve a whole container at once with ResolveMarkerSequence.
type Marker struct {
	// Raw is the marker exactly as it appeared, delimiters included ("(iv)").
	Raw string `json:"raw"`

	// Token is the bare symbol without delimiters ("iv").
	Token string `json:"token"`

	Kind MarkerKind `json:"kind"`
	Form MarkerForm `json:"form"`

	// Ordinal is the numeric value of the marker under Kind: a=1, iv=4, 7=7.
	// For KindDecimal it is the value of the last segment.
	Ordinal int `json:"ordinal"`

	// Segments holds every component of a hierarchical numeric marker
	// ("1.1.2" -> [1 1 2]). Nil for non-decimal markers.
	Segments []int `json:"segments,omitempty"`

	// Ambiguous is true when Token reads validly as both alpha and roman.
	// AltKind/AltOrdinal carry the competing interpretation.
	Ambiguous  bool       `json:"ambiguous,omitempty"`
	AltKind    MarkerKind `json:"alt_kind,omitempty"`
	AltOrdinal int        `json:"alt_ordinal,omitempty"`

	// Length is the byte length consumed from the paragraph, including leading
	// whitespace, delimiters and the separator that follows.
	Length int `json:"-"`
}

// Key returns a stable string form used to build node IDs ("1.1", "iv", "a").
func (m Marker) Key() string {
	if m.Kind == KindDecimal {
		parts := make([]string, len(m.Segments))
		for i, s := range m.Segments {
			parts[i] = strconv.Itoa(s)
		}
		return strings.Join(parts, ".")
	}
	return m.Token
}

var (
	// (1) / ( a ) / (iv)
	reBracket = regexp.MustCompile(`^[\s\p{Zs}]*\(\s*([0-9]{1,3}|[A-Za-z]{1,4})\s*\)`)
	// 1.1 / 1.1.2  — optional trailing dot, must be followed by space or end
	reDecimal = regexp.MustCompile(`^[\s\p{Zs}]*([0-9]{1,3}(?:\.[0-9]{1,3})+)\.?(\s|$)`)
	// 1. / a) / IV.
	reSimple = regexp.MustCompile(`^[\s\p{Zs}]*([0-9]{1,3}|[A-Za-z]{1,4})\s*([.)])(\s|$)`)
)

// ParseMarker extracts a leading list marker from a paragraph's text.
//
// It is deliberately conservative: a token must be a plausible ordinal (digits,
// a canonical roman numeral, or one-to-two uniformly cased letters) before it is
// accepted. This rejects prose that merely looks like a marker, such as the
// Indonesian abbreviations "Dst." or "No." — the single most common source of
// false positives when parsing legal text.
func ParseMarker(s string) (Marker, bool) {
	// Bracketed form binds tightest: "(1)" is never anything but a marker.
	if m := reBracket.FindStringSubmatch(s); m != nil {
		if mk, ok := classifyToken(m[1]); ok {
			mk.Raw = strings.TrimLeft(m[0], " \t ")
			mk.Form = FormBracket
			mk.Length = len(m[0])
			return mk, true
		}
		return Marker{}, false
	}

	// Hierarchical decimal: "1.1", "2.3.4". Checked before the simple form so
	// that "1.1" is not mis-read as digit 1 followed by a dot.
	if m := reDecimal.FindStringSubmatch(s); m != nil {
		segs := splitSegments(m[1])
		if len(segs) > 0 {
			form := FormBare
			if strings.Contains(strings.TrimSuffix(m[0], m[2]), m[1]+".") {
				form = FormDot
			}
			return Marker{
				Raw:      strings.TrimSpace(strings.TrimSuffix(m[0], m[2])),
				Token:    m[1],
				Kind:     KindDecimal,
				Form:     form,
				Ordinal:  segs[len(segs)-1],
				Segments: segs,
				Length:   len(m[0]) - len(m[2]),
			}, true
		}
		return Marker{}, false
	}

	if m := reSimple.FindStringSubmatch(s); m != nil {
		if mk, ok := classifyToken(m[1]); ok {
			mk.Raw = strings.TrimSpace(strings.TrimSuffix(m[0], m[3]))
			if m[2] == "." {
				mk.Form = FormDot
			} else {
				mk.Form = FormParen
			}
			mk.Length = len(m[0]) - len(m[3])
			return mk, true
		}
	}
	return Marker{}, false
}

func splitSegments(s string) []int {
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// classifyToken decides which symbol family a bare token belongs to, recording
// both readings when the token is ambiguous between alpha and roman.
func classifyToken(tok string) (Marker, bool) {
	if tok == "" {
		return Marker{}, false
	}
	m := Marker{Token: tok}

	if isAllDigits(tok) {
		n, err := strconv.Atoi(tok)
		if err != nil || n <= 0 {
			return Marker{}, false
		}
		m.Kind, m.Ordinal = KindDigit, n
		return m, true
	}

	if !isUniformCase(tok) {
		// "No", "Ir", "Dr" — mixed case is prose, not a marker.
		return Marker{}, false
	}
	upper := tok == strings.ToUpper(tok)

	romanVal, isRoman := parseRoman(tok)
	// Alpha markers stay short; "abc." in prose is not a list marker.
	alphaVal, isAlpha := parseAlpha(tok)
	if len(tok) > 2 {
		isAlpha = false
	}

	switch {
	case isRoman && isAlpha:
		m.Ambiguous = true
		if upper {
			m.Kind, m.Ordinal = KindUpperRoman, romanVal
			m.AltKind, m.AltOrdinal = KindUpperAlpha, alphaVal
		} else {
			m.Kind, m.Ordinal = KindLowerRoman, romanVal
			m.AltKind, m.AltOrdinal = KindLowerAlpha, alphaVal
		}
		return m, true
	case isRoman:
		if upper {
			m.Kind, m.Ordinal = KindUpperRoman, romanVal
		} else {
			m.Kind, m.Ordinal = KindLowerRoman, romanVal
		}
		return m, true
	case isAlpha:
		if upper {
			m.Kind, m.Ordinal = KindUpperAlpha, alphaVal
		} else {
			m.Kind, m.Ordinal = KindLowerAlpha, alphaVal
		}
		return m, true
	}
	return Marker{}, false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func isUniformCase(s string) bool {
	return s == strings.ToLower(s) || s == strings.ToUpper(s)
}

// parseAlpha maps a..z to 1..26 and aa..zz to 27..702 (spreadsheet-column style).
func parseAlpha(s string) (int, bool) {
	l := strings.ToLower(s)
	n := 0
	for _, r := range l {
		if r < 'a' || r > 'z' {
			return 0, false
		}
		n = n*26 + int(r-'a') + 1
	}
	if n == 0 {
		return 0, false
	}
	return n, true
}

var romanVals = map[byte]int{'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000}

// parseRoman accepts only canonical roman numerals: the value is re-rendered and
// compared, so "iiii" and "ic" are rejected while "iv" and "xxiv" are accepted.
func parseRoman(s string) (int, bool) {
	l := strings.ToLower(s)
	if l == "" {
		return 0, false
	}
	total, prev := 0, 0
	for i := len(l) - 1; i >= 0; i-- {
		v, ok := romanVals[l[i]]
		if !ok {
			return 0, false
		}
		if v < prev {
			total -= v
		} else {
			total += v
			prev = v
		}
	}
	if total <= 0 || formatRoman(total) != l {
		return 0, false
	}
	return total, true
}

var romanSteps = []struct {
	val int
	sym string
}{
	{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"},
	{100, "c"}, {90, "xc"}, {50, "l"}, {40, "xl"},
	{10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"},
}

func formatRoman(n int) string {
	var b strings.Builder
	for _, st := range romanSteps {
		for n >= st.val {
			b.WriteString(st.sym)
			n -= st.val
		}
	}
	return b.String()
}

// FormatOrdinal renders n back into the symbol family of kind. It is the inverse
// of parsing and is what renumber recommendations use to compute target values.
func FormatOrdinal(kind MarkerKind, n int) string {
	switch kind {
	case KindDigit, KindDecimal:
		return strconv.Itoa(n)
	case KindLowerRoman:
		return formatRoman(n)
	case KindUpperRoman:
		return strings.ToUpper(formatRoman(n))
	case KindLowerAlpha:
		return formatAlpha(n)
	case KindUpperAlpha:
		return strings.ToUpper(formatAlpha(n))
	}
	return strconv.Itoa(n)
}

func formatAlpha(n int) string {
	if n <= 0 {
		return ""
	}
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{byte('a' + n%26)}, out...)
		n /= 26
	}
	return string(out)
}

// Render rebuilds the full marker text for a value, preserving the original form
// so a renumber action writes "(3)" where the document used "(2)".
func Render(kind MarkerKind, form MarkerForm, n int) string {
	sym := FormatOrdinal(kind, n)
	switch form {
	case FormBracket:
		return "(" + sym + ")"
	case FormParen:
		return sym + ")"
	case FormDot:
		return sym + "."
	}
	return sym
}

// ResolveMarkerSequence decides the symbol family for a run of sibling markers.
//
// This is the deterministic replacement for the ~4,800-token prompt that
// mining-legal-backend spends teaching a model to disambiguate roman numerals.
// The rule is simply that a sequence is coherent: markers that count 1,2,3,...
// under one reading and jump erratically under the other resolve to the reading
// that counts. Unambiguous members decide the family for their ambiguous
// siblings; when every member is ambiguous (a lone "i.", or "i., v., x.") the
// sequence is read as roman, which is the convention in Indonesian legal
// drafting for that position in the hierarchy.
//
// The markers are modified in place.
func ResolveMarkerSequence(ms []*Marker) {
	if len(ms) == 0 {
		return
	}

	// A non-ambiguous member is decisive: alpha "b" or roman "ix" pins the family.
	decided := KindUnknown
	for _, m := range ms {
		if m != nil && !m.Ambiguous && m.Kind != KindUnknown {
			decided = m.Kind
			break
		}
	}

	if decided == KindUnknown {
		// Every member is ambiguous. Prefer the reading whose ordinals form the
		// longest ascending run starting at 1 — that is what a real list looks
		// like. On a tie (including the single-marker case) the primary reading
		// wins, which classifyToken sets to roman: an isolated "i." at this level
		// is roman by convention in Indonesian legal drafting.
		if scoreSequence(ms, true) > scoreSequence(ms, false) {
			decided = ms[0].AltKind
		} else {
			decided = ms[0].Kind
		}
	}

	wantAlpha := decided == KindLowerAlpha || decided == KindUpperAlpha
	for _, m := range ms {
		if m == nil || !m.Ambiguous {
			continue
		}
		isAlpha := m.Kind == KindLowerAlpha || m.Kind == KindUpperAlpha
		if wantAlpha != isAlpha {
			m.Kind, m.AltKind = m.AltKind, m.Kind
			m.Ordinal, m.AltOrdinal = m.AltOrdinal, m.Ordinal
		}
		m.Ambiguous = false
	}
}

// scoreSequence counts how many markers continue a 1,2,3 run under one reading.
func scoreSequence(ms []*Marker, useAlt bool) int {
	score, want := 0, 1
	for _, m := range ms {
		if m == nil {
			continue
		}
		v := m.Ordinal
		if useAlt {
			v = m.AltOrdinal
		}
		if v == want {
			score++
			want++
		}
	}
	return score
}
