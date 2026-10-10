package filesystem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalizeResolvesTraversalSegmentsOfAnExistingPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), ModeSharedDir); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)

	got, err := Canonicalize(filepath.Join(dir, "a", "b", "..", "..", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, "a"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The spelling is never trusted: this is what a containment check rests on.
func TestCanonicalizeResolvesSymlinksRatherThanTrustingTheSpelling(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	inside := filepath.Join(dir, "root")
	if err := os.MkdirAll(outside, ModeSharedDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(inside, ModeSharedDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(inside, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	realOutside, _ := filepath.EvalSymlinks(outside)

	got, err := Canonicalize(filepath.Join(inside, "link"))
	if err != nil {
		t.Fatal(err)
	}
	if got != realOutside {
		t.Errorf("got %q, want the link's target %q", got, realOutside)
	}
}

func TestCanonicalizeResolvesTheDeepestExistingAncestorOfAPathThatDoesNotExistYet(t *testing.T) {
	dir := t.TempDir()
	real, _ := filepath.EvalSymlinks(dir)

	got, err := Canonicalize(filepath.Join(dir, "not", "here", "yet"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, "not", "here", "yet"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Folding ".." away textually is how a containment check gets talked past.
//
// The path is built by concatenation, not filepath.Join, and that is the
// point: Join calls Clean, which folds the ".." away before Canonicalize ever
// sees it. Written with Join this test passes while asserting nothing, which
// is how it was written first. Anywhere user input reaches filepath.Join in
// this port, Go has already done the thing PathGuard exists to refuse.
func TestCanonicalizeRejectsATraversalSegmentThatCannotBeResolved(t *testing.T) {
	raw := t.TempDir() + "/missing/../elsewhere"

	_, err := Canonicalize(raw)

	if err == nil || !strings.Contains(err.Error(), "traversal segment") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

// And the same spelling below a directory that *does* exist resolves normally,
// because there is something real to resolve it against.
func TestATraversalBelowAnExistingDirectoryIsResolvedNotRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "here"), ModeSharedDir); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)

	got, err := Canonicalize(dir + "/here/../here")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, "here"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCanonicalizeRejectsAnEmptyPath(t *testing.T) {
	if _, err := Canonicalize(""); err == nil {
		t.Fatal("an empty path was accepted")
	}
}

func TestARelativePathIsResolvedAgainstTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	real, _ := filepath.EvalSymlinks(dir)

	got, err := Canonicalize("relative/thing")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, "relative", "thing"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIsWithinAcceptsTheRootItselfAndAnythingUnderIt(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects", "widget"), ModeSharedDir); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{root, filepath.Join(root, "projects"), filepath.Join(root, "projects", "widget")} {
		within, err := IsWithin(root, path)
		if err != nil {
			t.Fatal(err)
		}
		if !within {
			t.Errorf("%q reported as outside %q", path, root)
		}
	}
}

func TestIsWithinRejectsATraversalEscapeAndASiblingWithASharedPrefix(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "cockpit")
	sibling := filepath.Join(base, "cockpit-elsewhere")
	for _, dir := range []string{root, sibling} {
		if err := os.MkdirAll(dir, ModeSharedDir); err != nil {
			t.Fatal(err)
		}
	}

	for _, path := range []string{filepath.Join(root, ".."), sibling} {
		within, err := IsWithin(root, path)
		if err != nil {
			t.Fatal(err)
		}
		if within {
			t.Errorf("%q reported as inside %q", path, root)
		}
	}
}

// The case no pattern match on ".." would catch.
func TestIsWithinRejectsASymlinkEscapeThatNoPatternMatchOnDotDotWouldCatch(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "cockpit")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{root, outside} {
		if err := os.MkdirAll(dir, ModeSharedDir); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	within, err := IsWithin(root, filepath.Join(root, "escape"))
	if err != nil {
		t.Fatal(err)
	}
	if within {
		t.Error("a symlink out of the root reported as inside it")
	}
}

// A "." segment resolves to the directory it sits in, so a path carrying one
// points at the same place as the path without it — and containment has to
// agree, or a protected root fails to match a path spelled through itself.
//
// Only reachable for a path that does not exist yet: an existing one is
// normalised when the symlinks are evaluated. The PHP side keeps the "." and
// so answers "not contained" here; this is a deliberate, strictly-safer
// divergence, because unlike ".." a "." cannot move a path anywhere.
func TestADotSegmentDoesNotDefeatContainment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "protected")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, spelling := range []string{
		filepath.Join(root, "missing", "child"),
		root + "/./missing/child",
		root + "/missing/./child",
		filepath.Dir(root) + "/./protected/missing/child",
	} {
		within, err := IsWithin(root, spelling)
		if err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if !within {
			t.Errorf("%q read as outside %q", spelling, root)
		}
	}
}

// And a ".." is still the escape it always was: it is not folded away, so a
// path that climbs out of the root reads as outside.
func TestADotDotStillEscapes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "protected")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	within, err := IsWithin(root, root+"/../elsewhere/file")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if within {
		t.Error("a path climbing out of the root read as contained")
	}
}
