// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"context"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/tool"
)

// newPerFileTrackingAgent builds an Agent whose main task can call code_comment
// and task_done against the scripted responses.
func newPerFileTrackingAgent(responses []*llm.ChatResponse, maxToolRequests, maxRounds int) (*Agent, *tool.CommentCollector) {
	collector := tool.NewCommentCollector()
	reg := tool.NewRegistry()
	reg.Register(&tool.CodeCommentProvider{Collector: collector})
	a := New(Args{
		From: "main", To: "feature",
		LLMClient: &fakeAgentClient{responses: responses}, Model: "fake",
		CommentCollector: collector, Tools: reg, MaxConcurrency: 1,
		Template: template.Template{
			MaxTokens: 100000, MaxToolRequestTimes: maxToolRequests, MaxReviewRounds: maxRounds,
			MainTask: template.LlmConversation{Messages: []template.ChatMessage{{Role: "user", Content: "Review {{diffs}}"}}},
		},
		MainToolDefs: []llm.ToolDef{
			{Type: "function", Function: llm.FunctionDef{Name: "task_done", Description: "done"}},
			{Type: "function", Function: llm.FunctionDef{Name: "code_comment", Description: "comment"}},
		},
	})
	a.currentDate = "2025-06-26 10:00"
	return a, collector
}

func hasWarning(ws []AgentWarning, typ string) bool {
	for _, w := range ws {
		if w.Type == typ {
			return true
		}
	}
	return false
}

// A comment filed under a renamed file's old path is resolved against that
// diff; it must be re-keyed to the new path, which is what all per-file
// tracking (filter, confirmed block, coverage) uses.
func TestDispatchSubtasks_RenamedFileCommentOnOldPath(t *testing.T) {
	a, collector := newPerFileTrackingAgent([]*llm.ChatResponse{codeCommentResponse("old.go")}, 1, 0)
	a.diffs = []model.Diff{{OldPath: "old.go", NewPath: "new.go", IsRenamed: true,
		Diff: "@@ -1,1 +1,2 @@\n x\n+foo := bar.Baz()", Insertions: 1}}

	comments, err := a.dispatchSubtasks(context.Background())
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("want 1 comment, got %d: %+v", len(comments), comments)
	}
	if comments[0].Path != "new.go" {
		t.Errorf("comment Path = %q, want new.go", comments[0].Path)
	}
	if comments[0].StartLine != 2 {
		t.Errorf("comment StartLine = %d, want 2", comments[0].StartLine)
	}
	if got := len(collector.CommentsForPath("new.go")); got != 1 {
		t.Errorf("CommentsForPath(new.go) = %d, want 1", got)
	}
	if hasWarning(a.Warnings(), "subtask_error") {
		t.Errorf("new.go reported failed despite having a comment: %+v", a.Warnings())
	}
}
