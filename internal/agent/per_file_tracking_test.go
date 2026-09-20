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

// A round that completed with task_done must not be downgraded by a later
// round that stops short (here: max tool rounds), the same way a later round's
// error keeps the earlier findings. Otherwise every file of the group without a
// comment is marked failed and re-reviewed on resume.
func TestExecuteGroupSubtask_LaterRoundStopKeepsCompletion(t *testing.T) {
	empty := ""
	text := &llm.ChatResponse{Choices: []llm.Choice{{Message: llm.ResponseMessage{Content: &empty}}}, Model: "fake"}
	a, _ := newPerFileTrackingAgent([]*llm.ChatResponse{
		codeCommentResponse("a.go"), agentTaskDoneResponse(), // round 1 completes
		text, text, text, // round 2 never calls task_done
	}, 2, 2)
	a.diffs = []model.Diff{
		{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1 +1,2 @@\n x\n+foo := bar.Baz()"},
		{OldPath: "b.go", NewPath: "b.go", Diff: "+y"},
	}

	done, stop, err := a.executeGroupSubtask(context.Background(), FileGroup{Label: "g", Diffs: a.diffs})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done || stop != nil {
		t.Fatalf("round 1 completed; got done=%v stop=%+v, want done=true stop=nil", done, stop)
	}
	if !hasWarning(a.Warnings(), "review_round_failed") {
		t.Errorf("expected a review_round_failed warning for the stopped round, got %+v", a.Warnings())
	}
}
