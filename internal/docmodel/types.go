// Package docmodel defines the core document types shared by every stage of the
// pipeline: ingest, structure, align, textdiff and rules.
//
// The central addressing scheme is the paragraph index N, rendered as "[N]" in
// human-readable output. Every finding, reference and recommendation action
// points at a paragraph by this index, so it stays stable across stages.
package docmodel

// Paragraph is one addressable unit of a document.
//
// A paragraph is produced for every w:p in a DOCX body, including paragraphs
// nested inside tables, and for every line of extracted PDF text. Index is
// assigned sequentially from 0 in document order and never changes afterwards.
type Paragraph struct {
	Index int `json:"index"`

	// Text is the normalized text used for comparison and matching.
	Text string `json:"text"`

	// Raw is the text exactly as extracted, before normalization. Recommendation
	// actions must quote Raw so that a find-and-replace against the original file
	// actually matches.
	Raw string `json:"raw"`

	// Style is the paragraph style id (w:pStyle/@w:val), empty when unstyled.
	Style string `json:"style,omitempty"`

	// ListID and ListLvl mirror w:numPr (w:numId / w:ilvl). ListID is 0 when the
	// paragraph is not part of a Word auto-numbered list. These are a secondary
	// signal only: legal documents usually carry literal markers in the text.
	ListID  int `json:"list_id,omitempty"`
	ListLvl int `json:"list_lvl,omitempty"`

	// InTable marks paragraphs extracted from inside a w:tbl.
	InTable bool `json:"in_table,omitempty"`
}

// IsBlank reports whether the paragraph carries no comparable content.
func (p Paragraph) IsBlank() bool { return p.Text == "" }

// IndexedDoc is a fully parsed document: flat paragraphs plus the structural
// tree and cross-references derived from them.
type IndexedDoc struct {
	// Source is a human-readable origin (file path or name) used in messages.
	Source string `json:"source"`

	// Paragraphs is indexed by Paragraph.Index; Paragraphs[i].Index == i.
	Paragraphs []Paragraph `json:"paragraphs"`

	// Root is the synthetic top of the structure tree. Never nil after parsing.
	Root *Node `json:"root"`

	// Nodes indexes every node in the tree by Node.ID for O(1) resolution.
	Nodes map[string]*Node `json:"-"`

	// References are every cross-reference found in body text, in document order.
	References []Reference `json:"references"`

	// Lang is the dominant detected language ("id", "en", or "" when unknown).
	Lang string `json:"lang,omitempty"`
}

// Para returns the paragraph at index i, or false when i is out of range.
func (d *IndexedDoc) Para(i int) (Paragraph, bool) {
	if d == nil || i < 0 || i >= len(d.Paragraphs) {
		return Paragraph{}, false
	}
	return d.Paragraphs[i], true
}

// Text returns the normalized text at index i, or "" when out of range.
func (d *IndexedDoc) Text(i int) string {
	p, ok := d.Para(i)
	if !ok {
		return ""
	}
	return p.Text
}

// Node returns the structural node with the given ID.
func (d *IndexedDoc) Node(id string) (*Node, bool) {
	if d == nil || d.Nodes == nil {
		return nil, false
	}
	n, ok := d.Nodes[id]
	return n, ok
}

// FullText renders the document in the "[N]text" line format. This is the same
// shape mining-legal-backend feeds its prompts, kept for interoperability and
// for debugging output.
func (d *IndexedDoc) FullText() string {
	if d == nil {
		return ""
	}
	var b []byte
	for _, p := range d.Paragraphs {
		b = append(b, '[')
		b = appendInt(b, p.Index)
		b = append(b, ']')
		b = append(b, p.Raw...)
		b = append(b, '\n')
	}
	return string(b)
}

func appendInt(b []byte, n int) []byte {
	if n == 0 {
		return append(b, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	return append(b, tmp[i:]...)
}
