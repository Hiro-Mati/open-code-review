// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import (
	"context"
	"strings"
	"testing"
)

func TestParseDiffText_StripsIndexHeadersFromPromptDiff(t *testing.T) {
	diffText := `diff --git a/first.go b/first.go
index 1234567..89abcde 100644
--- a/first.go
+++ b/first.go
@@ -1,1 +1,2 @@
 first
+index added-content
diff --git a/second.go b/second.go
new file mode 100644
index 0000000..7654321
--- /dev/null
+++ b/second.go
@@ -0,0 +1 @@
+package second
`

	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d", len(diffs))
	}

	for _, d := range diffs {
		if strings.Contains(d.Diff, "\nindex ") {
			t.Errorf("prompt diff contains index header:\n%s", d.Diff)
		}
	}
	if !strings.Contains(diffs[0].Diff, "diff --git a/first.go b/first.go") {
		t.Errorf("prompt diff lost git header:\n%s", diffs[0].Diff)
	}
	if !strings.Contains(diffs[0].Diff, "+index added-content") {
		t.Errorf("prompt diff lost index-prefixed hunk content:\n%s", diffs[0].Diff)
	}
	if !diffs[1].IsNew {
		t.Error("new-file metadata was not preserved")
	}
}

// TestParseDiffText_Rename guards against issue #99: a renamed file must be
// recognized via the "rename from"/"rename to" extended header lines so that
// the parser reads content at the NEW path instead of warning about the old
// path ("cannot read file ... exit status 128").
func TestParseDiffText_Rename(t *testing.T) {
	diffText := `diff --git a/pkg/old name.go b/pkg/new name.go
similarity index 95%
rename from pkg/old name.go
rename to pkg/new name.go
index 1234567..89abcde 100644
--- a/pkg/old name.go
+++ b/pkg/new name.go
@@ -1,3 +1,3 @@
 line1
-line2
+line2 changed
 line3
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	d := diffs[0]
	if !d.IsRenamed {
		t.Errorf("IsRenamed = false, want true")
	}
	if d.OldPath != "pkg/old name.go" {
		t.Errorf("OldPath = %q, want %q", d.OldPath, "pkg/old name.go")
	}
	if d.NewPath != "pkg/new name.go" {
		t.Errorf("NewPath = %q, want %q", d.NewPath, "pkg/new name.go")
	}
	if d.IsNew || d.IsDeleted {
		t.Errorf("IsNew/IsDeleted = %v/%v, want false/false", d.IsNew, d.IsDeleted)
	}
}

// TestParseDiffText_PureRename covers a 100% similarity rename, which carries
// no hunks and no ---/+++ lines at all.
func TestParseDiffText_PureRename(t *testing.T) {
	diffText := `diff --git a/old.go b/new.go
similarity index 100%
rename from old.go
rename to new.go
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	d := diffs[0]
	if !d.IsRenamed || d.OldPath != "old.go" || d.NewPath != "new.go" {
		t.Errorf("got IsRenamed=%v OldPath=%q NewPath=%q, want true/old.go/new.go",
			d.IsRenamed, d.OldPath, d.NewPath)
	}
}

// TestParseDiffText_DeletedFile guards the /dev/null detection: git emits
// "+++ /dev/null" WITHOUT the b/ prefix, which the old regexes required, so
// deletions were misclassified and triggered a doomed `git show ref:path`.
func TestParseDiffText_DeletedFile(t *testing.T) {
	diffText := `diff --git a/gone.go b/gone.go
deleted file mode 100644
index 1234567..0000000
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-line1
-line2
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	d := diffs[0]
	if !d.IsDeleted {
		t.Errorf("IsDeleted = false, want true")
	}
	if d.NewPath != "/dev/null" {
		t.Errorf("NewPath = %q, want /dev/null", d.NewPath)
	}
	if d.OldPath != "gone.go" {
		t.Errorf("OldPath = %q, want gone.go", d.OldPath)
	}
}

// TestParseDiffText_NewFile covers "--- /dev/null" (no a/ prefix).
func TestParseDiffText_NewFile(t *testing.T) {
	diffText := `diff --git a/fresh.go b/fresh.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/fresh.go
@@ -0,0 +1,2 @@
+line1
+line2
`
	repo := t.TempDir()
	diffs, err := ParseDiffText(context.Background(), diffText, repo, "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	d := diffs[0]
	if !d.IsNew {
		t.Errorf("IsNew = false, want true")
	}
	if d.IsDeleted {
		t.Errorf("IsDeleted = true, want false")
	}
	if d.Insertions != 2 {
		t.Errorf("Insertions = %d, want 2", d.Insertions)
	}
}

// TestParseDiffText_BinaryMarkerAnchored guards two binary-detection cases:
// a text file whose CONTENT mentions "Binary files " must not be classified
// as binary (the unanchored regex used to match any line in the section and
// the file was silently excluded from review), while a real binary diff must
// still be detected.
func TestParseDiffText_BinaryMarkerAnchored(t *testing.T) {
	diffText := `diff --git a/docs.md b/docs.md
index 1234567..89abcde 100644
--- a/docs.md
+++ b/docs.md
@@ -1,2 +1,3 @@
 line1
+Note: Binary files are handled specially by git.
 line2
diff --git a/blob.bin b/blob.bin
index 1234567..89abcde 100644
Binary files a/blob.bin and b/blob.bin differ
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d", len(diffs))
	}
	if diffs[0].IsBinary {
		t.Errorf("docs.md IsBinary = true, want false (content line mentioning "+
			"'Binary files ' must not mark the file binary); diff:\n%s", diffText)
	}
	if diffs[0].Insertions != 1 {
		t.Errorf("docs.md Insertions = %d, want 1", diffs[0].Insertions)
	}
	if !diffs[1].IsBinary {
		t.Errorf("blob.bin IsBinary = false, want true")
	}
}

// TestParseDiffText_CountsContentLinesWithPlusMinusPrefix covers content
// lines that themselves begin with "++"/"--": an added line "++i" renders in
// the diff as "+++i", and the old "exclude +++/--- header" guard used to drop
// it from the insertion count (same for deletions), skewing per-file stats
// and the changeLines threshold that gates the plan phase.
func TestParseDiffText_CountsContentLinesWithPlusMinusPrefix(t *testing.T) {
	diffText := `diff --git a/counter.go b/counter.go
index 1234567..89abcde 100644
--- a/counter.go
+++ b/counter.go
@@ -1,3 +1,3 @@
 func inc() {
---oldFlag
+++newFlag
 }
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	d := diffs[0]
	if d.Insertions != 1 {
		t.Errorf("Insertions = %d, want 1 (added content line \"+++newFlag\")", d.Insertions)
	}
	if d.Deletions != 1 {
		t.Errorf("Deletions = %d, want 1 (deleted content line \"---oldFlag\")", d.Deletions)
	}
}

// TestParseDiffText_DevNullStringInsideHunk ensures an added content line
// whose rendered form is exactly "+++ /dev/null" (i.e. the file gained a line
// reading "++ /dev/null") is treated as hunk content, not as the deleted-file
// header marker.
func TestParseDiffText_DevNullStringInsideHunk(t *testing.T) {
	diffText := `diff --git a/paths.txt b/paths.txt
index 1234567..89abcde 100644
--- a/paths.txt
+++ b/paths.txt
@@ -1,1 +1,2 @@
 first
+++ /dev/null
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	d := diffs[0]
	if d.IsDeleted {
		t.Errorf("IsDeleted = true, want false (\"+++ /dev/null\" inside a hunk is content)")
	}
	if d.Insertions != 1 {
		t.Errorf("Insertions = %d, want 1", d.Insertions)
	}
}

// TestParseDiffText_QuotedHeaderStartsNewFile guards against a C-quoted
// "diff --git" header being missed. Git quotes a path containing '"', '\' or
// a control character even under core.quotepath=false; an unmatched header
// folds the file's hunks into the previous file's diff.
func TestParseDiffText_QuotedHeaderStartsNewFile(t *testing.T) {
	diffText := `diff --git a/a.go b/a.go
index 1234567..89abcde 100644
--- a/a.go
+++ b/a.go
@@ -1 +1,2 @@
 package a
+var A = 1
diff --git "a/b\"q.go" "b/b\"q.go"
new file mode 100644
index 0000000..7654321
--- /dev/null
+++ "b/b\"q.go"
@@ -0,0 +1,2 @@
+package q
+func Secret() {}
diff --git "a/tab\there\\x.go" "b/tab\there\\x.go"
index 1234567..89abcde 100644
--- "a/tab\there\\x.go"
+++ "b/tab\there\\x.go"
@@ -1 +1 @@
-old
+new
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs, got %d", len(diffs))
	}
	if diffs[0].NewPath != "a.go" || diffs[0].Insertions != 1 {
		t.Errorf("diffs[0] = %q with %d insertions, want a.go with 1", diffs[0].NewPath, diffs[0].Insertions)
	}
	if strings.Contains(diffs[0].Diff, "Secret") {
		t.Errorf("a.go diff absorbed the quoted file's hunk:\n%s", diffs[0].Diff)
	}
	if d := diffs[1]; d.OldPath != `b"q.go` || d.NewPath != `b"q.go` || !d.IsNew || d.Insertions != 2 {
		t.Errorf("diffs[1] = old %q new %q IsNew=%v ins=%d, want b\"q.go new with 2 insertions",
			d.OldPath, d.NewPath, d.IsNew, d.Insertions)
	}
	if d := diffs[2]; d.NewPath != "tab\there\\x.go" {
		t.Errorf("diffs[2].NewPath = %q, want %q", d.NewPath, "tab\there\\x.go")
	}
}

// TestParseDiffText_QuotedRename covers renames where only one side needs
// quoting, including octal escapes in the "rename to" line.
func TestParseDiffText_QuotedRename(t *testing.T) {
	diffText := `diff --git a/plain.go "b/new\"\303\251.go"
similarity index 100%
rename from plain.go
rename to "new\"\303\251.go"
diff --git "a/old\\x.go" b/renamed.go
similarity index 100%
rename from "old\\x.go"
rename to renamed.go
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d", len(diffs))
	}
	if d := diffs[0]; d.OldPath != "plain.go" || d.NewPath != "new\"\xc3\xa9.go" || !d.IsRenamed {
		t.Errorf("diffs[0] = old %q new %q renamed=%v", d.OldPath, d.NewPath, d.IsRenamed)
	}
	if d := diffs[1]; d.OldPath != `old\x.go` || d.NewPath != "renamed.go" || !d.IsRenamed {
		t.Errorf("diffs[1] = old %q new %q renamed=%v", d.OldPath, d.NewPath, d.IsRenamed)
	}
}

// TestParseDiffText_PathContainingSpaceB guards against splitting an
// unquoted header at a " b/" that belongs to the path itself. For a
// non-rename both halves are the same path, which fixes the split.
func TestParseDiffText_PathContainingSpaceB(t *testing.T) {
	diffText := `diff --git a/x b/y.go b/x b/y.go
index 1234567..89abcde 100644
--- a/x b/y.go
+++ b/x b/y.go
@@ -1 +1,2 @@
 package y
+var V = 1
`
	diffs, err := ParseDiffText(context.Background(), diffText, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("ParseDiffText: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if d := diffs[0]; d.OldPath != "x b/y.go" || d.NewPath != "x b/y.go" {
		t.Errorf("paths = old %q new %q, want both %q", d.OldPath, d.NewPath, "x b/y.go")
	}
}

// TestParseDiffHeader_Malformed checks that lines which only resemble a
// header are rejected rather than producing empty or partial paths.
func TestParseDiffHeader_Malformed(t *testing.T) {
	for _, line := range []string{
		"diff --git ",
		"diff --git a/",
		"diff --git a/x",
		"diff --git a/ b/x",
		"diff --git a/x b/",
		"diff --git x/y b/y",
		`diff --git "a/x`,
		`diff --git "a/x""b/x"`,
		`diff --git "a/x" "b/x" trailing`,
		`diff --git "a/\q" "b/x"`,
		`diff --git "a/\1" "b/x"`,
		`diff --git a/x "b/x`,
		`diff --git "a/x" c/x`,
		"diff --cc file.go",
	} {
		if o, n, ok := parseDiffHeader(line); ok {
			t.Errorf("parseDiffHeader(%q) = %q, %q, true; want not ok", line, o, n)
		}
	}
	if s := unquoteGitPath(`"a\a\b\t\n\v\f\r\"\\\101"`); s != "a\a\b\t\n\v\f\r\"\\A" {
		t.Errorf("unquoteGitPath decoded escapes to %q", s)
	}
	if s := unquoteGitPath(`"broken`); s != `"broken` {
		t.Errorf("unquoteGitPath kept %q, want input unchanged", s)
	}
}
