package trace

import (
	"sync"
	"testing"
)

// Streaming must extend one line rather than emit a new one per token, or the
// UI would show a column of single characters.
func TestStreamCoalescesIntoOneEntry(t *testing.T) {
	b := NewBuffer(nil)

	b.Stream("analyze", KindThought, "Melihat ")
	b.Stream("analyze", KindThought, "perubahan ")
	b.Stream("analyze", KindThought, "jangka waktu.")

	got := b.Entries()
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(got), got)
	}
	if got[0].Text != "Melihat perubahan jangka waktu." {
		t.Errorf("text = %q", got[0].Text)
	}
}

// Listeners receive the cumulative line, not the chunk: a browser that misses
// one frame still ends up correct.
func TestListenerSeesCumulativeText(t *testing.T) {
	var last string
	b := NewBuffer(func(e Entry) { last = e.Text })

	b.Stream("n", KindThought, "abc")
	b.Stream("n", KindThought, "def")

	if last != "abcdef" {
		t.Errorf("listener got %q, want the whole line", last)
	}
}

// A note must not land inside streamed reasoning, and must start a new line
// after it.
func TestNoteClosesAnOpenStream(t *testing.T) {
	b := NewBuffer(nil)

	b.Stream("n", KindThought, "berpikir")
	b.Note("n", KindNote, "selesai")
	b.Stream("n", KindThought, "lagi")

	got := b.Entries()
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(got), got)
	}
	if got[0].Text != "berpikir" || got[1].Text != "selesai" || got[2].Text != "lagi" {
		t.Errorf("entries merged wrongly: %+v", got)
	}
}

func TestEndStreamStartsANewLine(t *testing.T) {
	b := NewBuffer(nil)

	b.Stream("n", KindThought, "satu")
	b.EndStream("n")
	b.Stream("n", KindThought, "dua")

	if got := b.Entries(); len(got) != 2 {
		t.Errorf("EndStream did not split the lines: %+v", got)
	}
}

// Two nodes stream concurrently — the ingest branches do exactly this — and
// must not interleave into each other's lines.
func TestNodesStreamIndependently(t *testing.T) {
	b := NewBuffer(nil)

	b.Stream("a", KindThought, "aa")
	b.Stream("b", KindThought, "bb")
	b.Stream("a", KindThought, "cc")

	got := b.Entries()
	if len(got) != 2 {
		t.Fatalf("expected one entry per node, got %d: %+v", len(got), got)
	}
	byNode := map[string]string{}
	for _, e := range got {
		byNode[e.Node] = e.Text
	}
	if byNode["a"] != "aacc" || byNode["b"] != "bb" {
		t.Errorf("nodes bled into each other: %+v", byNode)
	}
}

// Seq must be stable and unique: the browser uses it to decide append versus
// replace.
func TestSeqIsUniqueAndStable(t *testing.T) {
	b := NewBuffer(nil)

	b.Note("n", KindNote, "satu")
	b.Stream("n", KindThought, "x")
	b.Stream("n", KindThought, "y")
	b.Note("n", KindNote, "dua")

	seen := map[int]bool{}
	for _, e := range b.Entries() {
		if seen[e.Seq] {
			t.Errorf("duplicate seq %d", e.Seq)
		}
		seen[e.Seq] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 distinct entries, got %d", len(seen))
	}
}

func TestLoadRestoresAndContinues(t *testing.T) {
	b := NewBuffer(nil)
	b.Load([]Entry{{Seq: 0, Node: "n", Kind: KindNote, Text: "lama"}})

	b.Note("n", KindNote, "baru")

	got := b.Entries()
	if len(got) != 2 {
		t.Fatalf("expected restored + new, got %+v", got)
	}
	// A new entry must not reuse a seq already loaded, or it would overwrite
	// the restored line in the browser and in the database.
	if got[1].Seq == got[0].Seq {
		t.Errorf("new entry reused seq %d", got[1].Seq)
	}
}

func TestConcurrentWritersAreSafe(t *testing.T) {
	b := NewBuffer(func(Entry) {})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			node := string(rune('a' + n))
			for j := 0; j < 50; j++ {
				b.Stream(node, KindThought, "x")
				b.Note(node, KindNote, "y")
			}
		}(i)
	}
	wg.Wait()

	if len(b.Entries()) == 0 {
		t.Error("no entries recorded")
	}
}

func TestNopDiscards(t *testing.T) {
	var r Recorder = Nop{}
	r.Note("n", KindNote, "x")
	r.Stream("n", KindThought, "y")
	r.EndStream("n")
}
