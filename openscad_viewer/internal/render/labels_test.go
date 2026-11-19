package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const labelLog = `ECHO: "PART:lid"
ECHO: "WORKBENCH WARNING: part("inner") is inside part("lid"), which it belongs to; the inner part is ignored"
ECHO: "PART:body"
ECHO: "PART:lid"
ECHO: "FEATURE:M3 hole:[1.5, -2, 3.25]"
ECHO: "FEATURE:x:[0.333333, 100000, 0]"
ECHO: "PART:it's "q""
ECHO: "FEATURE:broken:[1, 2]"
ECHO: "hello"
WARNING: something else
`

func TestParseLabels(t *testing.T) {
	lbl, warns := parseLabels(labelLog)
	if got := strings.Join(lbl.Parts, "|"); got != `lid|body|it's "q"` {
		t.Fatalf("parts = %q", got)
	}
	if len(lbl.Features) != 2 {
		t.Fatalf("features = %+v", lbl.Features)
	}
	if f := lbl.Features[0]; f.Name != "M3 hole" || f.At != [3]float64{1.5, -2, 3.25} {
		t.Fatalf("feature 0 = %+v", f)
	}
	if f := lbl.Features[1]; f.Name != "x" || f.At[1] != 100000 {
		t.Fatalf("feature 1 = %+v", f)
	}
	if len(warns) != 2 {
		t.Fatalf("warns = %q", warns)
	}
	if !strings.HasPrefix(warns[0], `WARNING: part("inner") is inside part("lid")`) || !strings.Contains(warns[1], "unreadable") && !strings.Contains(warns[1], "3 numbers") && !strings.Contains(warns[1], "want 3") {
		t.Fatalf("warns = %q", warns)
	}
}

func TestLabelEchoesAreNotOrdinaryEcho(t *testing.T) {
	_, warns, echo := parseLog(labelLog, "/d/p")
	if len(echo) != 1 || echo[0] != `ECHO: "hello"` {
		t.Fatalf("echo = %q", echo)
	}
	if len(warns) != 1 || warns[0] != "WARNING: something else" {
		t.Fatalf("warns = %q (workbench warnings are parsed by parseLabels)", warns)
	}
}

func TestDefineArgsRejectsBadNames(t *testing.T) {
	if _, err := defineArgs(map[string]string{"bad name": "1"}); err == nil {
		t.Fatal("want error")
	}
	args, err := defineArgs(map[string]string{"w": "5"})
	if err != nil || len(args) != 2 || args[0] != "-D" || args[1] != "w=5" {
		t.Fatalf("args = %q, %v", args, err)
	}
}

func TestEnvironPutsLibraryFirst(t *testing.T) {
	t.Setenv("OPENSCADPATH", "/usr/share/lib")
	r := New("openscad", 0, 1)
	if r.environ() != nil {
		t.Fatal("no LibDir: environment is inherited")
	}
	r.LibDir = "/data/lib"
	env := r.environ()
	var got []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "OPENSCADPATH=") {
			got = append(got, kv)
		}
	}
	if len(got) != 1 || got[0] != "OPENSCADPATH=/data/lib"+string(os.PathListSeparator)+"/usr/share/lib" {
		t.Fatalf("OPENSCADPATH entries = %q", got)
	}
}

func TestInstallLibrary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lib")
	if err := InstallLibrary(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "workbench.scad"))
	if err != nil || !strings.Contains(string(b), "module part(name)") || !strings.Contains(string(b), "module feature(name, at)") {
		t.Fatalf("library = %v", err)
	}
}
