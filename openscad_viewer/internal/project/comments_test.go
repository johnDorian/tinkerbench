package project

import (
	"errors"
	"testing"
)

func TestComments(t *testing.T) {
	s, _ := Open(t.TempDir())
	src := "a = 1;\nb = 2;\ncube(a);\n"
	if _, err := s.Create("p", "", src, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment("p", MainFile, 9, 0, "x", nil, nil, "web"); err == nil {
		t.Fatal("want error for a line outside the file")
	}
	sug := "b = 3;\n"
	c, err := s.AddComment("p", MainFile, 2, 0, "bigger", &sug, nil, "web")
	if err != nil {
		t.Fatal(err)
	}
	if c.Quote != "b = 2;" || *c.Suggestion != "b = 3;" {
		t.Fatalf("comment = %+v", c)
	}
	other, _ := s.AddComment("p", MainFile, 3, 0, "use b?", nil, nil, "web")

	// Lines inserted above: both comments move down.
	if _, err := s.WriteFile("p", MainFile, "// hi\n// there\n"+src, "agent", ""); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.Comments("p", MainFile, false)
	if cs[0].Line != 4 || cs[1].Line != 5 || cs[0].Outdated {
		t.Fatalf("after insert: %+v", cs)
	}
	if info, _ := s.Get("p"); info.OpenComments != 2 {
		t.Fatalf("open comments = %d", info.OpenComments)
	}

	// Apply the suggestion: line replaced, comment resolved, still anchored.
	ac, v, err := s.ApplySuggestion("p", c.ID, "web")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ReadFile("p", MainFile); got != "// hi\n// there\na = 1;\nb = 3;\ncube(a);\n" {
		t.Fatalf("content = %q", got)
	}
	if !ac.Resolved || v.Note != "applied suggestion #1" {
		t.Fatalf("applied = %+v, version = %+v", ac, v)
	}
	if _, _, err := s.ApplySuggestion("p", c.ID, "web"); err == nil {
		t.Fatal("resolved suggestion must not apply twice")
	}

	// The commented line is rewritten: the other comment becomes outdated, and a
	// suggestion on outdated lines can't be applied.
	sug2 := "sphere(a);"
	c3, _ := s.AddComment("p", MainFile, 5, 0, "round", &sug2, nil, "web")
	if _, err := s.EditFile("p", MainFile, "cube(a);", "cube(a * 2);", false, "agent", ""); err != nil {
		t.Fatal(err)
	}
	cs, _ = s.Comments("p", "", false)
	for _, c := range cs {
		if c.ID == other.ID && !c.Outdated {
			t.Fatalf("comment on a changed line should be outdated: %+v", c)
		}
	}
	if _, _, err := s.ApplySuggestion("p", c3.ID, "web"); !errors.Is(err, ErrOutdated) {
		t.Fatalf("want ErrOutdated, got %v", err)
	}

	// Replies, resolve, delete.
	if r, err := s.ReplyComment("p", other.ID, "agent", "done"); err != nil || len(r.Replies) != 1 {
		t.Fatalf("reply = %+v, %v", r, err)
	}
	if _, err := s.ResolveComment("p", other.ID, true, "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteComment("p", c3.ID); err != nil {
		t.Fatal(err)
	}
	if open, _ := s.Comments("p", "", false); len(open) != 0 {
		t.Fatalf("open = %+v", open)
	}
	if all, _ := s.Comments("p", "", true); len(all) != 2 {
		t.Fatalf("all = %+v", all)
	}
}
