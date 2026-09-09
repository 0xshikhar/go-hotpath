package analyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnnotated(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "annotated")
}

func TestCrossPackageFact(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "crossdep", "crosscall")
}

// TestSpikeBookFixture runs the built tool over the real spike books:
// BookA.Apply (annotated) must produce diagnostics at its allocation sites,
// while BookC's annotated hot path must be clean thanks to one
// //hotpath:allow on the free-list append.
func TestSpikeBookFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("spike integration test builds the binary; skip with -short")
	}

	bin := filepath.Join(t.TempDir(), "hotpathcheck")
	build := exec.Command("go", "-C", "..", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tool: %v\n%s", err, out)
	}

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	vet := exec.Command("go", "vet", "-vettool", bin, "spike/book")
	vet.Dir = root
	out, _ := vet.CombinedOutput()
	text := string(out)

	if !strings.Contains(text, "book_a.go") {
		t.Fatalf("expected diagnostics in book_a.go, got:\n%s", text)
	}
	for _, want := range []string{"composite literal", "append"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected a %q diagnostic, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "book_c.go") {
		t.Fatalf("book_c.go should be clean (allow on freeSlot); got:\n%s", text)
	}
	if strings.Contains(text, "book_b.go") {
		t.Fatalf("book_b.go is unannotated and must be untouched; got:\n%s", text)
	}
}
