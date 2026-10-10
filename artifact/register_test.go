package artifact

import (
	"testing"
	"time"
)

// CodeRabbit r4238379157: non-canonical spellings of the same file are
// duplicates, and the stored path is canonical.
func TestAppendRegisteredRejectsNonCanonicalDuplicate(t *testing.T) {
	m := &Manifest{}
	mk := func(id, file string) Artifact {
		return Artifact{ID: id, Type: "report", Title: id, File: file, Created: time.Now()}
	}
	got, err := AppendRegistered(m, mk("a1", "attachments/./a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got.File != "attachments/a.md" || m.Artifacts[0].File != "attachments/a.md" {
		t.Fatalf("stored path must be canonical: %q", got.File)
	}
	for _, dup := range []string{"attachments/a.md", "attachments//a.md", "attachments/x/../a.md"} {
		if _, err := AppendRegistered(m, mk("a-"+dup, dup)); err == nil {
			t.Fatalf("%q names an already registered file", dup)
		}
	}
	// A pre-existing non-canonical entry also matches.
	m.Artifacts = append(m.Artifacts, mk("legacy", "logs//b.txt"))
	if _, err := AppendRegistered(m, mk("b2", "logs/b.txt")); err == nil {
		t.Fatal("must match a pre-existing non-canonical entry")
	}
}
