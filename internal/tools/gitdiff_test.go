package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeAndStage writes a file in dir and stages it.
func writeAndStage(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", name).CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %s: %v", name, out, err)
	}
}

func TestDiffFiles_WorkingTree(t *testing.T) {
	dir := initGitRepo(t)

	writeAndStage(t, dir, "a.txt", "one\n")
	writeAndStage(t, dir, "c.txt", "staged new\n")
	gitCommit(t, dir, "c1")

	// Modify a.txt (unstaged M) and add an untracked b.txt.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := DiffFiles(dir, DiffWorkingTree)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.File] = f.Status
	}
	if got["a.txt"] != "M" {
		t.Errorf("a.txt status = %q, want M", got["a.txt"])
	}
	if got["b.txt"] != "?" {
		t.Errorf("b.txt status = %q, want ? (untracked)", got["b.txt"])
	}
	if got["c.txt"] != "" {
		t.Errorf("c.txt committed and clean, should be absent, got status %q", got["c.txt"])
	}
}

func TestDiffFiles_LastCommit(t *testing.T) {
	dir := initGitRepo(t)

	writeAndStage(t, dir, "a.txt", "one\n")
	gitCommit(t, dir, "c1")
	writeAndStage(t, dir, "a.txt", "one\ntwo\n")
	writeAndStage(t, dir, "b.txt", "brand new\n")
	gitCommit(t, dir, "c2")

	files, err := DiffFiles(dir, DiffLastCommit)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.File] = f.Status
	}
	if got["a.txt"] != "M" {
		t.Errorf("a.txt status = %q, want M", got["a.txt"])
	}
	if got["b.txt"] != "A" {
		t.Errorf("b.txt status = %q, want A", got["b.txt"])
	}
}

func TestDiffFiles_LastCommit_SingleCommitErrors(t *testing.T) {
	dir := initGitRepo(t)
	writeAndStage(t, dir, "a.txt", "one\n")
	gitCommit(t, dir, "c1")

	if _, err := DiffFiles(dir, DiffLastCommit); err == nil {
		t.Error("expected an error for a repo with a single commit, got nil")
	}
}

func TestDiffPatchFor_WorkingTreeAndUntracked(t *testing.T) {
	dir := initGitRepo(t)

	writeAndStage(t, dir, "a.txt", "one\n")
	gitCommit(t, dir, "c1")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := DiffFiles(dir, DiffWorkingTree)
	if err != nil {
		t.Fatal(err)
	}
	patches := map[string][]DiffHunk{}
	for _, st := range files {
		patch, err := DiffPatchFor(dir, st, DiffWorkingTree)
		if err != nil {
			t.Fatalf("patch %s: %v", st.File, err)
		}
		patches[st.File] = ParseUnifiedDiff(patch)
	}

	// Modified tracked file: one removed, one added.
	if len(patches["a.txt"]) != 1 || patches["a.txt"][0].Added != 1 || patches["a.txt"][0].Removed != 1 {
		t.Errorf("a.txt hunks = %+v, want 1 hunk with +1/−1", patches["a.txt"])
	}

	// Untracked file: every line shows as added (the --no-index route).
	if len(patches["b.txt"]) != 1 || patches["b.txt"][0].Added != 2 || patches["b.txt"][0].Removed != 0 {
		t.Errorf("b.txt hunks = %+v, want 1 hunk with +2/−0", patches["b.txt"])
	}
}

func TestDiffFilePatch_LastCommit(t *testing.T) {
	dir := initGitRepo(t)

	writeAndStage(t, dir, "a.txt", "old line\n")
	gitCommit(t, dir, "c1")
	writeAndStage(t, dir, "a.txt", "new line\n")
	gitCommit(t, dir, "c2")

	patch, err := DiffFilePatch(dir, "a.txt", DiffLastCommit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "-old line") || !strings.Contains(patch, "+new line") {
		t.Errorf("last-commit patch should carry both sides, got:\n%s", patch)
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\n" +
		"--- a/f.go\n" +
		"+++ b/f.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" ctx\n" +
		"+added\n" +
		"@@ -10,2 +11,2 @@\n" +
		"-removed\n" +
		" ctx2\n"

	hunks := ParseUnifiedDiff(diff)
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}

	h0 := hunks[0]
	if h0.Header != "@@ -1,3 +1,4 @@" {
		t.Errorf("hunk 0 header = %q", h0.Header)
	}
	if h0.Added != 1 || h0.Removed != 0 {
		t.Errorf("hunk 0 counts = +%d/−%d, want +1/−0", h0.Added, h0.Removed)
	}
	if len(h0.Lines) != 2 ||
		h0.Lines[0].Kind != DiffLineContext || h0.Lines[0].Text != " ctx" ||
		h0.Lines[1].Kind != DiffLineAdd || h0.Lines[1].Text != "+added" {
		t.Errorf("hunk 0 lines = %+v", h0.Lines)
	}

	h1 := hunks[1]
	if h1.Added != 0 || h1.Removed != 1 {
		t.Errorf("hunk 1 counts = +%d/−%d, want +0/−1", h1.Added, h1.Removed)
	}
	if h1.Lines[0].Kind != DiffLineRemove {
		t.Errorf("hunk 1 line 0 kind = %v, want remove", h1.Lines[0].Kind)
	}
}

func TestParseUnifiedDiff_Edges(t *testing.T) {
	tests := []struct {
		name string
		diff string
	}{
		{"empty", ""},
		{"headers only", "diff --git a/f b/f\n--- a/f\n+++ b/f\n"},
		{"binary", "Binary files a/img and b/img differ\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseUnifiedDiff(tt.diff); got != nil {
				t.Errorf("ParseUnifiedDiff(%q) = %+v, want nil", tt.diff, got)
			}
		})
	}

	// "\ No newline at end of file" classifies as meta and counts nothing.
	hunks := ParseUnifiedDiff("@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n")
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	if h.Added != 1 || h.Removed != 1 {
		t.Errorf("counts = +%d/−%d, want +1/−1", h.Added, h.Removed)
	}
	// Lines: -old, the backslash line, +new, and the empty context line the
	// trailing newline yields.
	if len(h.Lines) != 4 {
		t.Fatalf("lines = %+v, want 4", h.Lines)
	}
	if h.Lines[1].Kind != DiffLineMeta {
		t.Errorf("lines[1] = %+v, want the backslash line as meta", h.Lines[1])
	}
}

func TestParseHunks_ContentRoundTrip(t *testing.T) {
	// The adapter over ParseUnifiedDiff must keep Hunk.Content byte-for-byte
	// identical to what the standalone parser produced before the refactor:
	// every non-header line, joined with \n, trailing newline included.
	diff := "@@ -1,2 +1,2 @@\n ctx\n-old\n+new\n"
	hunks := parseHunks(diff)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	want := " ctx\n-old\n+new\n"
	if hunks[0].Content != want {
		t.Errorf("Content = %q, want %q", hunks[0].Content, want)
	}
}

func TestRunGitPlain_And_IsGitRepo(t *testing.T) {
	dir := initGitRepo(t)
	if !IsGitRepo(dir) {
		t.Fatal("expected a git repo")
	}
	if IsGitRepo(t.TempDir()) {
		t.Error("a fresh temp dir is not a repo")
	}
	out, err := RunGitPlain(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("expected a branch name")
	}
}
