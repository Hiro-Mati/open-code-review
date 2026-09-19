// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import (
	"context"
	"regexp"
	"testing"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestAmbiguousRefWarningDoesNotPolluteParsedOutput covers a branch and a tag
// sharing a name, which makes git print "warning: refname 'main' is ambiguous"
// on stderr. Parsed values (merge base, resolved base) must come from stdout
// only, with and without a git runner.
func TestAmbiguousRefWarningDoesNotPolluteParsedOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		runner *gitcmd.Runner
	}{{"runner", gitcmd.New(0)}, {"direct", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := initBareRepo(t)
			writeCommit(t, repo, "a.txt", "1\n", "c1")
			runGitTest(t, repo, "branch", "-M", "main")
			runGitTest(t, repo, "tag", "main")
			runGitTest(t, repo, "checkout", "-q", "-b", "feature")
			writeCommit(t, repo, "a.txt", "1\n2\n", "c2")
			ctx := context.Background()

			if mb := NewProvider(repo, "main", "feature", tc.runner).MergeBase(ctx); !shaRe.MatchString(mb) {
				t.Errorf("MergeBase = %q, want a bare SHA", mb)
			}
			in := NewCommitProvider(repo, "feature", tc.runner).ResolveInput(ctx)
			if !shaRe.MatchString(in.ResolvedBase) {
				t.Errorf("ResolvedBase = %q, want a bare SHA", in.ResolvedBase)
			}
			diffs, err := NewProvider(repo, "main", "feature", tc.runner).GetDiff(ctx)
			if err != nil || len(diffs) != 1 {
				t.Fatalf("range main..feature: got %d diffs, err %v; want 1 diff", len(diffs), err)
			}
		})
	}
}
