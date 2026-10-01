package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Shared git plumbing for diff views: pure functions with no ADK context,
// used by the agent tools (via runGit) and by the TUI diff viewer directly.
// The exec core lives here so there is exactly one place that spawns git; the
// parsers live here so the tool handlers and the viewer read the same diff
// shape and format logic is never duplicated.

// DiffScope selects which pair of tree states a diff covers. The labels are
// honest about what the first version shows: the working tree against HEAD,
// and HEAD against its parent — not "last agent turn", which needs turn
// boundaries the session does not record yet.
type DiffScope int

const (
	// DiffWorkingTree is the uncommitted state: worktree + index vs HEAD.
	DiffWorkingTree DiffScope = iota
	// DiffLastCommit is HEAD vs HEAD~1.
	DiffLastCommit
)

// Label returns the human-readable scope name shown in the viewer header.
func (s DiffScope) Label() string {
	if s == DiffLastCommit {
		return "Last commit"
	}
	return "Working tree"
}

// DiffFileStatus is one changed file: path plus a single-letter git status
// (M, A, D, R, T, ?, …).
type DiffFileStatus struct {
	File   string
	Status string
}

// DiffLineKind classifies one line of a unified diff body.
type DiffLineKind int

const (
	DiffLineContext DiffLineKind = iota
	DiffLineAdd
	DiffLineRemove
	DiffLineMeta // "\ No newline at end of file" and similar service lines
)

// DiffLine is one line of a parsed diff: its kind and the raw text including
// the leading +/-/space prefix.
type DiffLine struct {
	Kind DiffLineKind
	Text string
}

// DiffHunk is one @@-hunk with its body lines pre-classified.
type DiffHunk struct {
	Header  string
	Lines   []DiffLine
	Added   int
	Removed int
}

// gitRunWith executes git in dir with the given parent context and returns
// stdout. This is the one exec site the package's git callers share.
func gitRunWith(parent context.Context, dir string, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(parent, defaultGitTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return stdout.String(), nil
}

// RunGitPlain runs git in dir and returns stdout, with a fresh context and
// the package's default timeout. The context-free entry point for callers
// outside the ADK (the TUI).
func RunGitPlain(dir string, args ...string) (string, error) {
	return gitRunWith(context.Background(), dir, args...)
}

// IsGitRepo reports whether dir is inside a git repository.
func IsGitRepo(dir string) bool {
	_, err := RunGitPlain(dir, "rev-parse", "--git-dir")
	return err == nil
}

// DiffFiles lists the changed files for the scope, in git's own order. For
// DiffWorkingTree this is one entry per file from `git status --porcelain`
// (tracked changes carry the more informative of the two column letters;
// untracked files carry "?"). For DiffLastCommit it is `git diff --name-status
// HEAD~1 HEAD` — a repo with fewer than two commits errors.
func DiffFiles(dir string, scope DiffScope) ([]DiffFileStatus, error) {
	if scope == DiffLastCommit {
		out, err := RunGitPlain(dir, "diff", "--name-status", "HEAD~1", "HEAD")
		if err != nil {
			return nil, err
		}
		return parseNameStatus(out), nil
	}

	out, err := RunGitPlain(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var files []DiffFileStatus
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		x, y := line[0], line[1]
		file := strings.TrimSpace(line[2:])
		status := "?"
		switch {
		case x != ' ' && x != '?':
			status = string(x)
		case y != ' ' && y != '?':
			status = string(y)
		}
		files = append(files, DiffFileStatus{File: file, Status: status})
	}
	return files, nil
}

// parseNameStatus parses `git diff --name-status` output. Rename rows carry
// two paths ("R100\told\tnew"); the new name is the one that exists.
func parseNameStatus(out string) []DiffFileStatus {
	var files []DiffFileStatus
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		if status != "" {
			status = string(status[0])
		}
		files = append(files, DiffFileStatus{File: parts[len(parts)-1], Status: status})
	}
	return files
}

// DiffFilePatch returns the unified diff of one file in the scope. Untracked
// files (Working-tree scope only) come from `git diff --no-index` against
// /dev/null, which exits 1 to mean "differ found" — that exit code is a
// result, not an error.
func DiffFilePatch(dir, file string, scope DiffScope) (string, error) {
	if scope == DiffLastCommit {
		return RunGitPlain(dir, "diff", "HEAD~1", "HEAD", "--", file)
	}
	return RunGitPlain(dir, "diff", "HEAD", "--", file)
}

// DiffPatchFor returns the patch for one listed file, routing untracked
// files to the --no-index path in the working-tree scope.
func DiffPatchFor(dir string, st DiffFileStatus, scope DiffScope) (string, error) {
	if scope == DiffWorkingTree && st.Status == "?" {
		return DiffUntrackedPatch(dir, st.File)
	}
	return DiffFilePatch(dir, st.File, scope)
}

// DiffUntrackedPatch returns the unified diff of an untracked file: every
// line shows as added. git signals "files differ" with exit code 1, which for
// this invocation is the success case.
func DiffUntrackedPatch(dir, file string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(context.Background(), defaultGitTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", "diff", "--no-index", "--", "/dev/null", file)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", fmt.Errorf("git diff --no-index %s: %s: %w", file, strings.TrimSpace(stderr.String()), err)
		}
	}
	return stdout.String(), nil
}

// ParseUnifiedDiff splits a unified diff into hunks of classified lines.
// Header lines before the first hunk are skipped; a trailing newline yields
// one empty context line so hunk content round-trips byte-for-byte. A diff
// with no hunks (binary files, mode-only changes) parses to nil.
func ParseUnifiedDiff(diff string) []DiffHunk {
	if diff == "" {
		return nil
	}

	var hunks []DiffHunk
	var current *DiffHunk

	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "@@") {
			hunks = append(hunks, DiffHunk{Header: line})
			current = &hunks[len(hunks)-1]
			continue
		}
		if current == nil {
			continue // file header before the first hunk
		}
		var kind DiffLineKind
		switch {
		case strings.HasPrefix(line, "+"):
			kind = DiffLineAdd
			current.Added++
		case strings.HasPrefix(line, "-"):
			kind = DiffLineRemove
			current.Removed++
		case strings.HasPrefix(line, "\\"):
			kind = DiffLineMeta
		default:
			kind = DiffLineContext
		}
		current.Lines = append(current.Lines, DiffLine{Kind: kind, Text: line})
	}
	return hunks
}
