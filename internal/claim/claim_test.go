package claim

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func setup(t *testing.T) (root, state string) {
	root = t.TempDir()
	state = filepath.Join(root, StateDirName, "bt-abc")
	if err := os.MkdirAll(state, 0755); err != nil {
		t.Fatal(err)
	}
	return root, state
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
}

func wantConflict(t *testing.T, err error, paths ...string) {
	t.Helper()
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("want conflict on %v, got %v", paths, err)
	}
	if !slices.Equal(conflict.Paths, paths) {
		t.Fatalf("conflict paths = %v, want %v", conflict.Paths, paths)
	}
}

func TestClaimFreshTargets(t *testing.T) {
	root, state := setup(t)
	targets := []string{filepath.Join(root, "a.iso")}
	if err := Claim(root, state, targets); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, markerName)); err != nil {
		t.Fatal("marker not written")
	}
}

func TestClaimExistingFileConflictsAndStaysUnclaimed(t *testing.T) {
	root, state := setup(t)
	target := filepath.Join(root, "a.iso")
	writeFile(t, target)
	for range 2 { // A failed run leaves state behind; the next run must still see the conflict.
		wantConflict(t, Claim(root, state, []string{target}), target)
	}
	if _, err := os.Stat(filepath.Join(state, markerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("conflict wrote claim marker")
	}
	if raw, _ := os.ReadFile(target); string(raw) != "keep" {
		t.Fatal("existing file changed")
	}
}

func TestClaimedTaskResumesOverItsOwnFiles(t *testing.T) {
	root, state := setup(t)
	target := filepath.Join(root, "a.iso")
	if err := Claim(root, state, []string{target}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, target)
	if err := Claim(root, state, []string{target}); err != nil {
		t.Fatalf("resume: %v", err)
	}
}

func TestClaimDirectoryRules(t *testing.T) {
	root, state := setup(t)
	if err := os.MkdirAll(filepath.Join(root, "pack", "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "pack", "docs")
	writeFile(t, blocker)
	dirTarget := filepath.Join(root, "dir.iso")
	if err := os.Mkdir(dirTarget, 0755); err != nil {
		t.Fatal(err)
	}
	targets := []string{
		filepath.Join(root, "pack", "sub", "ok.bin"), // existing parent directories are fine
		filepath.Join(root, "pack", "docs", "a.txt"), // parent is a file
		filepath.Join(root, "pack", "docs", "b.txt"), // same blocker reported once
		dirTarget, // target is a directory
	}
	wantConflict(t, Claim(root, state, targets), blocker, dirTarget)
}

func TestClaimRejectsPathsOutsideRootOrInState(t *testing.T) {
	root, state := setup(t)
	for _, target := range []string{filepath.Join(root, "..", "x"), filepath.Join(root, StateDirName, "x")} {
		err := Claim(root, state, []string{target})
		var conflict *ConflictError
		if err == nil || errors.As(err, &conflict) {
			t.Fatalf("%s: want rejection, got %v", target, err)
		}
	}
}
