// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

func writeRepoFile(t *testing.T, repo, rel, content string) {
	t.Helper()
	full := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// commitRepoFiles writes files, then stages and commits everything,
// .gitignore'd paths included, so the test controls what is tracked.
func commitRepoFiles(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		writeRepoFile(t, repo, rel, content)
	}
	runGitTest(t, repo, "add", "-f", ".")
	runGitTest(t, repo, "commit", "-q", "-m", "seed")
}

func workspaceDiffPaths(t *testing.T, repo string) []string {
	t.Helper()
	diffs, err := NewWorkspaceProvider(repo, gitcmd.New(0)).GetDiff(context.Background())
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	var paths []string
	for _, d := range diffs {
		paths = append(paths, d.NewPath)
	}
	slices.Sort(paths)
	return paths
}

// TestGetDiffGitignoreStarStopsAtSlash pins that "*" never crosses "/". On
// Windows filepath.Match treats "\" as the separator, so "/*.go" used to drop
// every .go file below the root and "docs/*.md" dropped docs/a/b.md.
func TestGetDiffGitignoreStarStopsAtSlash(t *testing.T) {
	repo := initBareRepo(t)
	commitRepoFiles(t, repo, map[string]string{
		".gitignore": "/*.go\ndocs/*.md\nbuild/\n",
		"root.go":    "package p\n",
		"pkg/x.go":   "package pkg\n",
		"build/a.go": "package build\n",
	})
	writeRepoFile(t, repo, "root.go", "package p\n\nvar R = 1\n")
	writeRepoFile(t, repo, "pkg/x.go", "package pkg\n\nvar X = 1\n")
	writeRepoFile(t, repo, "build/a.go", "package build\n\nvar B = 1\n")
	writeRepoFile(t, repo, "pkg/new.go", "package pkg\n")
	writeRepoFile(t, repo, "docs/top.md", "top\n")
	writeRepoFile(t, repo, "docs/a/b.md", "nested\n")

	// root.go and build/a.go are tracked but ignored, so they are dropped;
	// docs/top.md is ignored and untracked, so git never lists it.
	want := []string{"docs/a/b.md", "pkg/new.go", "pkg/x.go"}
	if got := workspaceDiffPaths(t, repo); !slices.Equal(got, want) {
		t.Errorf("diff paths = %v, want %v", got, want)
	}
}

// TestMatchGitignoreBodyStarStopsAtSlash covers the same rule in the root
// .gitignore fallback matcher, which scan and file_find also use.
func TestMatchGitignoreBodyStarStopsAtSlash(t *testing.T) {
	tests := []struct {
		path, pattern string
		want          bool
	}{
		{"sub/x.go", "/*.go", false},
		{"x.go", "/*.go", true},
		{"docs/a/b.md", "docs/*.md", false},
		{"docs/b.md", "docs/*.md", true},
		{"a/b/c.log", "*.log", true},
	}
	for _, tt := range tests {
		if got := matchGitignoreBody(tt.path, tt.pattern); got != tt.want {
			t.Errorf("matchGitignoreBody(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}

// TestGetDiffGitignoreMiddleSlashIsAnchored pins git's rule that a pattern
// with a slash in the middle is relative to its .gitignore: "config/local.json"
// ignores the root file only, not pkg/config/local.json.
func TestGetDiffGitignoreMiddleSlashIsAnchored(t *testing.T) {
	repo := initBareRepo(t)
	commitRepoFiles(t, repo, map[string]string{
		".gitignore":        "config/local.json\n",
		"config/local.json": "{}\n",
	})
	writeRepoFile(t, repo, "config/local.json", "{\"a\":1}\n")
	writeRepoFile(t, repo, "pkg/config/local.json", "{\"b\":1}\n")

	want := []string{"pkg/config/local.json"}
	if got := workspaceDiffPaths(t, repo); !slices.Equal(got, want) {
		t.Errorf("diff paths = %v, want %v", got, want)
	}
}

// TestGetDiffHonoursNestedGitignoreNegation pins that a nested .gitignore is
// applied: api/.gitignore re-includes what the root one ignores, so git lists
// api/types.gen.go and the review must not drop it.
func TestGetDiffHonoursNestedGitignoreNegation(t *testing.T) {
	repo := initBareRepo(t)
	commitRepoFiles(t, repo, map[string]string{
		".gitignore":     "*.gen.go\n",
		"api/.gitignore": "!*.gen.go\n",
		"root.gen.go":    "package p\n",
	})
	writeRepoFile(t, repo, "root.gen.go", "package p\n\nvar G = 1\n")
	writeRepoFile(t, repo, "api/types.gen.go", "package api\n")

	want := []string{"api/types.gen.go"}
	if got := workspaceDiffPaths(t, repo); !slices.Equal(got, want) {
		t.Errorf("diff paths = %v, want %v", got, want)
	}
}

// TestGetDiffGitignoreHonoursInfoExclude pins that .git/info/exclude counts,
// as it does for git itself, including for a tracked change.
func TestGetDiffGitignoreHonoursInfoExclude(t *testing.T) {
	repo := initBareRepo(t)
	commitRepoFiles(t, repo, map[string]string{"local.cfg": "a\n", "main.go": "package main\n"})
	writeRepoFile(t, repo, ".git/info/exclude", "*.cfg\n")
	writeRepoFile(t, repo, "local.cfg", "b\n")
	writeRepoFile(t, repo, "main.go", "package main\n\nvar M = 1\n")

	want := []string{"main.go"}
	if got := workspaceDiffPaths(t, repo); !slices.Equal(got, want) {
		t.Errorf("diff paths = %v, want %v", got, want)
	}
}

// TestGetDiffKeepsUntrackedNameWithLeadingSpace pins that untracked names are
// taken verbatim. Trimming " lead.go" to "lead.go" made the read fail, and
// the file disappeared from the review without a word.
func TestGetDiffKeepsUntrackedNameWithLeadingSpace(t *testing.T) {
	repo := initBareRepo(t)
	writeCommit(t, repo, "a.txt", "1\n", "c1")
	writeRepoFile(t, repo, " lead.go", "package lead\n")

	diffs, err := NewWorkspaceProvider(repo, gitcmd.New(0)).GetDiff(context.Background())
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	var paths []string
	for _, d := range diffs {
		paths = append(paths, d.NewPath)
		if d.Insertions != 1 {
			t.Errorf("%q: insertions = %d, want 1", d.NewPath, d.Insertions)
		}
	}
	want := []string{" lead.go"}
	if !slices.Equal(paths, want) {
		t.Errorf("diff paths = %q, want %q", paths, want)
	}
}

// TestGitIgnoredPathsTakesNamesLiterally pins that one batched check-ignore
// call answers for every name, including ones git could misread: a leading
// ":" (pathspec magic), glob characters and spaces.
func TestGitIgnoredPathsTakesNamesLiterally(t *testing.T) {
	repo := initBareRepo(t)
	writeRepoFile(t, repo, ".gitignore", "*.log\nout/\n")
	p := NewWorkspaceProvider(repo, gitcmd.New(0))

	paths := []string{":(top)x.log", ":keep.go", "*.go", " sp.log", "out/a.go", "src/a.go"}
	ignored, err := p.gitIgnoredPaths(context.Background(), paths)
	if err != nil {
		t.Fatalf("gitIgnoredPaths: %v", err)
	}
	want := map[string]bool{":(top)x.log": true, " sp.log": true, "out/a.go": true}
	for _, rel := range paths {
		if ignored[rel] != want[rel] {
			t.Errorf("ignored[%q] = %v, want %v", rel, ignored[rel], want[rel])
		}
	}
	if len(ignored) != len(want) {
		t.Errorf("ignored = %v, want %v", ignored, want)
	}

	none, err := p.gitIgnoredPaths(context.Background(), []string{"src/a.go"})
	if err != nil || len(none) != 0 {
		t.Errorf("no ignored path: got %v, %v; want empty, nil", none, err)
	}

	// Without a shared runner the provider execs git itself.
	direct, err := NewWorkspaceProvider(repo, nil).gitIgnoredPaths(context.Background(), []string{"a.log", "a.go"})
	if err != nil || !direct["a.log"] || direct["a.go"] {
		t.Errorf("runner-less gitIgnoredPaths = %v, %v; want only a.log", direct, err)
	}
}

// TestIgnoredPathsFilterFallsBackWhenGitFails pins the fallback: outside a
// repository git check-ignore fails, and the root .gitignore still applies.
func TestIgnoredPathsFilterFallsBackWhenGitFails(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, ".gitignore", "*.log\n")
	p := NewWorkspaceProvider(dir, gitcmd.New(0))

	isIgnored, err := p.ignoredPathsFilter(context.Background(), []string{"a.log", "a.go"})
	if err != nil {
		t.Fatalf("ignoredPathsFilter: %v", err)
	}
	if !isIgnored("a.log") || isIgnored("a.go") {
		t.Errorf("fallback verdicts: a.log=%v a.go=%v, want true false", isIgnored("a.log"), isIgnored("a.go"))
	}
}

// TestIgnoredPathsFilterReturnsCancellation pins that a cancelled context is
// reported rather than hidden behind the fallback matcher.
func TestIgnoredPathsFilterReturnsCancellation(t *testing.T) {
	repo := initBareRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewWorkspaceProvider(repo, gitcmd.New(0)).ignoredPathsFilter(ctx, []string{"a.go"}); err == nil {
		t.Fatal("expected a cancellation error")
	}
}
