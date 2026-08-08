package docmodel

import (
	"regexp"
	"strings"
)

// RefKind distinguishes a fully-qualified citation from a relative one.
type RefKind uint8

const (
	// RefAbsolute names the article level explicitly: "Pasal 12 ayat (3)".
	RefAbsolute RefKind = iota
	// RefRelative omits it: "ayat (3)", meaning the enclosing Pasal.
	RefRelative
)

// Reference is a cross-reference found in body text.
//
// Extraction is deliberately restricted to the closed citation grammar used in
// Indonesian legal drafting (UU 12/2011) and its English equivalent. Prose
// references such as "sebagaimana disebutkan di atas" carry no parseable target
// and are intentionally not matched here; they are left to the LLM adjudication
// tier rather than being guessed at.
type Reference struct {
	// ParaIndex is the paragraph the reference appears in.
	ParaIndex int `json:"para_index"`

	// Raw is the citation exactly as written.
	Raw string `json:"raw"`

	// Start and End are byte offsets of Raw within the paragraph's Text.
	Start int `json:"start"`
	End   int `json:"end"`

	Kind RefKind `json:"kind"`

	// Citation components. Empty string means "not specified".
	Pasal string `json:"pasal,omitempty"`
	Ayat  string `json:"ayat,omitempty"`
	Huruf string `json:"huruf,omitempty"`
	Angka string `json:"angka,omitempty"`

	// Lang is "id" or "en", taken from the keyword that introduced the citation.
	Lang string `json:"lang,omitempty"`
}

// TargetID renders the node ID this reference points at. For relative
// references the article component is filled in by the resolver, so callers
// should set Pasal first.
func (r Reference) TargetID() string {
	if r.Pasal == "" {
		return ""
	}
	kind := "pasal"
	if r.Lang == "en" {
		kind = "article"
	}
	id := kind + ":" + r.Pasal
	if r.Ayat != "" {
		id += "/ayat:" + r.Ayat
	}
	if r.Huruf != "" {
		id += "/huruf:" + r.Huruf
	}
	if r.Angka != "" {
		id += "/angka:" + r.Angka
	}
	return id
}

// String renders a normalized citation, used in finding messages.
func (r Reference) String() string {
	var b strings.Builder
	if r.Pasal != "" {
		if r.Lang == "en" {
			b.WriteString("Article " + r.Pasal)
		} else {
			b.WriteString("Pasal " + r.Pasal)
		}
	}
	if r.Ayat != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("ayat (" + r.Ayat + ")")
	}
	if r.Huruf != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("huruf " + r.Huruf)
	}
	if r.Angka != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("angka " + r.Angka)
	}
	return b.String()
}

var (
	// Absolute citation: an article keyword, its number, and optional sub-levels.
	// Number accepts digits, dotted decimals ("1.1") and roman numerals.
	reRefAbsolute = regexp.MustCompile(
		`(?i)\b(Pasal|Article|Clause|Section)\s+` +
			`(\d{1,4}(?:\.\d{1,3})*|[IVXLCDM]{1,7})` +
			`(?:\s*(?:ayat|paragraph|para\.?)\s*\(\s*(\d{1,3})\s*\))?` +
			`(?:\s*(?:huruf|letter)\s+([a-zA-Z]{1,2})\b)?` +
			`(?:\s*(?:angka|number|no\.?)\s+(\d{1,3})\b)?`)

	// Bare parenthesised sub-level directly after an absolute citation, the
	// English contract convention: "Section 5(a)" / "Article 12(3)".
	reRefSuffix = regexp.MustCompile(`^\(\s*([0-9]{1,3}|[a-zA-Z]{1,2})\s*\)`)

	// Relative citations, valid only inside an enclosing article.
	reRefRelAyat  = regexp.MustCompile(`(?i)\bayat\s*\(\s*(\d{1,3})\s*\)`)
	reRefRelHuruf = regexp.MustCompile(`(?i)\bhuruf\s+([a-zA-Z]{1,2})\b`)
	reRefRelAngka = regexp.MustCompile(`(?i)\bangka\s+(\d{1,3})\b`)
)

// ExtractReferences finds every parseable cross-reference in a paragraph.
//
// ignorePrefix is the number of leading bytes to skip, used to avoid reading a
// paragraph's own heading ("Pasal 12") as a reference to itself. Pass 0 for
// ordinary body paragraphs.
func ExtractReferences(paraIndex int, text string, ignorePrefix int) []Reference {
	if text == "" {
		return nil
	}
	var out []Reference

	// Byte ranges already claimed by an absolute citation, so the relative pass
	// does not re-report "ayat (3)" that is part of "Pasal 12 ayat (3)".
	type span struct{ start, end int }
	var claimed []span

	for _, loc := range reRefAbsolute.FindAllStringSubmatchIndex(text, -1) {
		start, end := loc[0], loc[1]
		if start < ignorePrefix {
			continue
		}
		group := func(i int) string {
			if loc[2*i] < 0 {
				return ""
			}
			return text[loc[2*i]:loc[2*i+1]]
		}
		keyword := strings.ToLower(group(1))
		ref := Reference{
			ParaIndex: paraIndex,
			Kind:      RefAbsolute,
			Pasal:     group(2),
			Ayat:      group(3),
			Huruf:     strings.ToLower(group(4)),
			Angka:     group(5),
			Lang:      "id",
		}
		if keyword != "pasal" {
			ref.Lang = "en"
		}

		// "Section 5(a)" — absorb a bare parenthesised sub-level.
		if ref.Ayat == "" && ref.Huruf == "" && end < len(text) {
			if m := reRefSuffix.FindStringSubmatch(text[end:]); m != nil {
				tok := m[1]
				if isAllDigits(tok) {
					ref.Ayat = tok
				} else {
					ref.Huruf = strings.ToLower(tok)
				}
				end += len(m[0])
			}
		}

		ref.Raw = text[start:end]
		ref.Start, ref.End = start, end
		out = append(out, ref)
		claimed = append(claimed, span{start, end})
	}

	inClaimed := func(pos int) bool {
		for _, c := range claimed {
			if pos >= c.start && pos < c.end {
				return true
			}
		}
		return false
	}

	addRelative := func(re *regexp.Regexp, set func(*Reference, string)) {
		for _, loc := range re.FindAllStringSubmatchIndex(text, -1) {
			if loc[0] < ignorePrefix || inClaimed(loc[0]) {
				continue
			}
			ref := Reference{
				ParaIndex: paraIndex,
				Kind:      RefRelative,
				Raw:       text[loc[0]:loc[1]],
				Start:     loc[0],
				End:       loc[1],
				Lang:      "id",
			}
			set(&ref, strings.ToLower(text[loc[2]:loc[3]]))
			out = append(out, ref)
			claimed = append(claimed, span{loc[0], loc[1]})
		}
	}

	addRelative(reRefRelAyat, func(r *Reference, v string) { r.Ayat = v })
	addRelative(reRefRelHuruf, func(r *Reference, v string) { r.Huruf = v })
	addRelative(reRefRelAngka, func(r *Reference, v string) { r.Angka = v })

	return out
}
