// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"context"
	"encoding/json"
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

// rawCommentResponse returns a code_comment call carrying the given comment
// objects verbatim, so a test can omit fields such as path.
func rawCommentResponse(comments ...map[string]any) *llm.ChatResponse {
	content := ""
	raw := make([]any, len(comments))
	for i, c := range comments {
		raw[i] = c
	}
	argsJSON, _ := json.Marshal(map[string]any{"comments": raw})
	return &llm.ChatResponse{
		Choices: []llm.Choice{{Message: llm.ResponseMessage{
			Content: &content,
			ToolCalls: []llm.ToolCall{{ID: "call_comment", Type: "function",
				Function: llm.FunctionCall{Name: "code_comment", Arguments: string(argsJSON)}}},
		}}},
		Model: "fake",
	}
}

func hasWarning(ws []AgentWarning, typ string) bool {
	for _, w := range ws {
		if w.Type == typ {
			return true
		}
	}
	return false
}

// A comment without a path in a multi-file group must never carry the group
// key as its path: it is placed by existing_code when that names exactly one
// file of the group, and dropped with a warning otherwise.
func TestExecuteGroupSubtask_PathlessCommentInGroup(t *testing.T) {
	diffs := []model.Diff{
		{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1 +1,2 @@\n x\n+foo := bar.Baz()"},
		{OldPath: "b.go", NewPath: "b.go", Diff: "@@ -1 +1,2 @@\n y\n+qux := 1"},
	}

	t.Run("located by existing_code", func(t *testing.T) {
		a, collector := newPerFileTrackingAgent([]*llm.ChatResponse{
			rawCommentResponse(map[string]any{"content": "nil deref", "existing_code": "foo := bar.Baz()"}),
			agentTaskDoneResponse(),
		}, 5, 1)
		a.diffs = diffs
		if _, _, err := a.executeGroupSubtask(context.Background(), FileGroup{Label: "g", Diffs: a.diffs}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := collector.Comments()
		if len(got) != 1 || got[0].Path != "a.go" || got[0].StartLine != 2 {
			t.Fatalf("want one comment on a.go:2, got %+v", got)
		}
	})

	t.Run("unlocatable is dropped", func(t *testing.T) {
		a, collector := newPerFileTrackingAgent([]*llm.ChatResponse{
			rawCommentResponse(map[string]any{"content": "general issue in this change"}),
			agentTaskDoneResponse(),
		}, 5, 1)
		a.diffs = diffs
		if _, _, err := a.executeGroupSubtask(context.Background(), FileGroup{Label: "g", Diffs: a.diffs}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := collector.Comments(); len(got) != 0 {
			t.Fatalf("want the pathless comment dropped, got %+v", got)
		}
		if !hasWarning(a.Warnings(), "comment_dropped") {
			t.Errorf("expected a comment_dropped warning, got %+v", a.Warnings())
		}
	})

	t.Run("single-file group keeps the fallback path", func(t *testing.T) {
		a, collector := newPerFileTrackingAgent([]*llm.ChatResponse{
			rawCommentResponse(map[string]any{"content": "general issue in this change"}),
			agentTaskDoneResponse(),
		}, 5, 1)
		a.diffs = diffs[:1]
		if _, _, err := a.executeGroupSubtask(context.Background(), FileGroup{Label: "g", Diffs: a.diffs}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := collector.Comments(); len(got) != 1 || got[0].Path != "a.go" {
			t.Fatalf("want one comment on a.go, got %+v", got)
		}
	})
}
