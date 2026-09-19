// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llmloop

import (
	"context"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/tool"
)

func msg(role, text string) llm.Message {
	return llm.NewTextMessage(role, text)
}

func TestCountMessagesTokens(t *testing.T) {
	msgs := []llm.Message{
		msg("user", "hello world"),
		msg("assistant", "hi there"),
	}
	got := CountMessagesTokens(msgs)
	if got <= 0 {
		t.Errorf("expected positive token count, got %d", got)
	}
}

func TestCountMessagesTokens_Empty(t *testing.T) {
	got := CountMessagesTokens(nil)
	if got != 0 {
		t.Errorf("expected 0 for nil, got %d", got)
	}
}

// TestCountMessagesTokens_IncludesNativePayload guards a gap found in review
// of the #805 fix: once an assistant turn's Native payload (thinking blocks,
// reasoning items, encrypted_content, reasoning_content) actually replays on
// the wire, a token budget computed only from ExtractText() would
// systematically under-count reasoning-heavy conversations and could let
// compression's threshold checks fire too late.
func TestCountMessagesTokens_IncludesNativePayload(t *testing.T) {
	withoutNative := []llm.Message{msg("user", "hello world")}
	withNative := []llm.Message{
		msg("user", "hello world"),
		llm.NewToolCallMessage("", nil, llm.NativeTurn{
			Family:  "openai-chat-completions",
			Payload: llm.ReasoningPayload(strings.Repeat("reasoning ", 200)),
		}, ""),
	}

	base := CountMessagesTokens(withoutNative)
	got := CountMessagesTokens(withNative)
	if got <= base {
		t.Errorf("CountMessagesTokens with a Native payload = %d, want more than the base count %d", got, base)
	}
}

func TestGroupIntoRounds(t *testing.T) {
	messages := []llm.Message{
		msg("system", "sys"),
		msg("user", "prompt"),
		msg("assistant", "resp1"),
		msg("tool", "result1"),
		msg("tool", "result2"),
		msg("assistant", "resp2"),
		msg("tool", "result3"),
		msg("assistant", "resp3"),
	}

	rounds := groupIntoRounds(messages, 2)
	if len(rounds) != 3 {
		t.Fatalf("expected 3 rounds, got %d", len(rounds))
	}

	if rounds[0].assistantIdx != 2 {
		t.Errorf("round[0].assistantIdx = %d, want 2", rounds[0].assistantIdx)
	}
	if len(rounds[0].toolIdxs) != 2 {
		t.Errorf("round[0] should have 2 tool messages, got %d", len(rounds[0].toolIdxs))
	}
	if rounds[1].assistantIdx != 5 {
		t.Errorf("round[1].assistantIdx = %d, want 5", rounds[1].assistantIdx)
	}
	if rounds[2].assistantIdx != 7 {
		t.Errorf("round[2].assistantIdx = %d, want 7", rounds[2].assistantIdx)
	}
	if len(rounds[2].toolIdxs) != 0 {
		t.Errorf("round[2] should have 0 tool messages")
	}
}

func TestGroupIntoRounds_NoAssistant(t *testing.T) {
	messages := []llm.Message{
		msg("system", "sys"),
		msg("user", "prompt"),
		msg("user", "another"),
	}
	rounds := groupIntoRounds(messages, 2)
	if len(rounds) != 0 {
		t.Errorf("expected 0 rounds, got %d", len(rounds))
	}
}

func TestPartitionMessages_ShortConversation(t *testing.T) {
	messages := []llm.Message{
		msg("system", "sys"),
		msg("user", "prompt"),
	}
	result := partitionMessages(messages, 100000, 0)
	if result.frozenEnd != 2 {
		t.Errorf("frozenEnd = %d, want 2", result.frozenEnd)
	}
	if result.compressEnd != 2 {
		t.Errorf("compressEnd = %d, want 2", result.compressEnd)
	}
}

func TestPartitionMessages_EverythingFits(t *testing.T) {
	messages := []llm.Message{
		msg("system", "sys"),
		msg("user", "prompt"),
		msg("assistant", "short reply"),
		msg("tool", "ok"),
	}
	result := partitionMessages(messages, 100000, 0)
	// The only round is the most recent one, so nothing may be compressed.
	if result.activeCount != 1 {
		t.Errorf("activeCount = %d, want 1 (most recent round kept)", result.activeCount)
	}
	if result.compressEnd != result.frozenEnd {
		t.Errorf("compressEnd = %d, want %d (nothing to compress)", result.compressEnd, result.frozenEnd)
	}
}

func TestPartitionMessages_EverythingFitsKeepsMostRecentRound(t *testing.T) {
	messages := []llm.Message{
		msg("system", "sys"),
		msg("user", "prompt"),
		msg("assistant", "first"),
		msg("tool", "ok"),
		msg("assistant", "second"),
		msg("tool", "ok"),
	}
	result := partitionMessages(messages, 100000, 0)
	if result.activeCount != 1 {
		t.Errorf("activeCount = %d, want 1", result.activeCount)
	}
	if result.compressEnd != 4 {
		t.Errorf("compressEnd = %d, want 4 (older round only)", result.compressEnd)
	}
}

// bigFrozenZone returns a system + user head that alone uses most of a
// 10000-token budget, like a group prompt carrying large diffs.
func bigFrozenZone() []llm.Message {
	return []llm.Message{msg("system", "sys"), msg("user", strings.Repeat("word ", 5000))}
}

func TestPartitionMessages_ChargesFrozenZone(t *testing.T) {
	const maxTokens = 10000
	messages := bigFrozenZone()
	for i := 0; i < 8; i++ {
		messages = append(messages, msg("assistant", "a"))
		messages = append(messages, llm.NewToolResultMessage("c", strings.Repeat("data ", 1400)))
	}

	p := partitionMessages(messages, maxTokens, 0)
	kept := CountMessagesTokens(messages[:p.frozenEnd]) + CountMessagesTokens(messages[p.compressEnd:])
	if kept > PromptTokenLimit(maxTokens) {
		t.Fatalf("frozen + active zone = %d tokens, over the %d limit (compressEnd=%d activeCount=%d)",
			kept, PromptTokenLimit(maxTokens), p.compressEnd, p.activeCount)
	}
	if p.activeCount < 1 || p.compressEnd >= len(messages) {
		t.Fatalf("most recent round must stay active: compressEnd=%d activeCount=%d", p.compressEnd, p.activeCount)
	}
}

// A most recent round too large to fit next to the frozen zone falls back to
// being summarized, so the review continues instead of stopping.
func TestPartitionMessages_OversizedLatestRoundIsCompressible(t *testing.T) {
	messages := bigFrozenZone()
	messages = append(messages, msg("assistant", "a"), msg("tool", "small"))
	messages = append(messages, msg("assistant", "b"), msg("tool", strings.Repeat("data ", 4000)))

	p := partitionMessages(messages, 10000, 0)
	if p.activeCount != 0 {
		t.Errorf("activeCount = %d, want 0", p.activeCount)
	}
	if p.compressEnd != len(messages) {
		t.Errorf("compressEnd = %d, want %d (oversized latest round is compressible)", p.compressEnd, len(messages))
	}
}

func compressionTestRunner(client *fakeClient) *Runner {
	summary := "short summary"
	for i := 0; i < 2; i++ {
		client.responses = append(client.responses, &llm.ChatResponse{
			Choices: []llm.Choice{{Message: llm.ResponseMessage{Content: &summary}}},
		})
	}
	deps := newTestDeps(client)
	deps.Template = template.Template{MaxTokens: 10000, MaxToolRequestTimes: 10,
		MemoryCompressionTask: template.LlmConversation{
			Messages: []template.ChatMessage{{Role: "user", Content: "summarize {{context}}"}},
		}}
	return NewRunner(deps)
}

func fileReadRound(id string) ([]llm.ToolCall, []tool.ToolCallResult) {
	calls := []llm.ToolCall{{ID: id, Type: "function", Function: llm.FunctionCall{Name: "file_read", Arguments: "{}"}}}
	results := []tool.ToolCallResult{{ToolCallID: id, Name: "file_read", Result: strings.Repeat("data ", 1400)}}
	return calls, results
}

// A large frozen zone must shrink the kept history enough for the
// conversation to continue instead of stopping with StopCompression.
func TestAddNextMessage_CompressionAccountsForFrozenZone(t *testing.T) {
	client := &fakeClient{}
	r := compressionTestRunner(client)
	msgs := bigFrozenZone()
	for i := 0; i < 7; i++ {
		calls, results := fileReadRound("c")
		msgs = append(msgs, llm.NewToolCallMessage("", calls, llm.NativeTurn{}, ""))
		msgs = append(msgs, llm.NewToolResultMessage("c", results[0].Result))
	}

	calls, results := fileReadRound("c9")
	ok := r.addNextMessage(context.Background(), "", calls, llm.NativeTurn{}, "", results, &msgs, "k", &compressionState{})
	if !ok {
		t.Fatalf("conversation stopped at %d tokens although compressing older rounds fits the limit",
			CountMessagesTokens(msgs))
	}
	if got := CountMessagesTokens(msgs); got >= PromptTokenLimit(10000) {
		t.Fatalf("tokens after compression = %d, want < %d", got, PromptTokenLimit(10000))
	}
}

// The round just executed (tool call + result) fits next to the frozen zone,
// so it must survive compression; only older rounds are summarized.
func TestAddNextMessage_KeepsCurrentRound(t *testing.T) {
	client := &fakeClient{}
	r := compressionTestRunner(client)
	msgs := bigFrozenZone()
	for i := 0; i < 2; i++ {
		calls, results := fileReadRound("c")
		msgs = append(msgs, llm.NewToolCallMessage("", calls, llm.NativeTurn{}, ""))
		msgs = append(msgs, llm.NewToolResultMessage("c", results[0].Result))
	}

	calls, results := fileReadRound("c9")
	ok := r.addNextMessage(context.Background(), "", calls, llm.NativeTurn{}, "", results, &msgs, "k", &compressionState{})
	if !ok {
		t.Fatalf("conversation stopped at %d tokens", CountMessagesTokens(msgs))
	}
	n := len(msgs)
	if n < 4 || msgs[n-2].Role != "assistant" || msgs[n-1].Role != "tool" || msgs[n-1].ToolCallID != "c9" {
		t.Fatalf("current round (tool call c9 + result) was summarized away; %d messages left", n)
	}
}

// A single huge tool result must not end the review: the oversized latest
// round is summarized and the conversation continues.
func TestAddNextMessage_OversizedLatestRoundContinues(t *testing.T) {
	client := &fakeClient{}
	r := compressionTestRunner(client)
	msgs := bigFrozenZone()
	calls, results := fileReadRound("c")
	msgs = append(msgs, llm.NewToolCallMessage("", calls, llm.NativeTurn{}, ""))
	msgs = append(msgs, llm.NewToolResultMessage("c", "small"))

	calls, results = fileReadRound("c9")
	results[0].Result = strings.Repeat("data ", 4000)
	ok := r.addNextMessage(context.Background(), "", calls, llm.NativeTurn{}, "", results, &msgs, "k", &compressionState{})
	if !ok {
		t.Fatalf("conversation stopped at %d tokens; oversized latest round should be summarized", CountMessagesTokens(msgs))
	}
	if len(client.requests) == 0 {
		t.Fatal("compression LLM was not called")
	}
}

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fences",
			input: `{"key": "value"}`,
			want:  `{"key": "value"}`,
		},
		{
			name:  "json fence",
			input: "```json\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "plain fence",
			input: "```\ncontent\n```",
			want:  "content",
		},
		{
			name:  "fence with surrounding whitespace",
			input: "  ```json\n{}\n```  ",
			want:  "{}",
		},
		{
			name:  "empty after strip",
			input: "```json\n```",
			want:  "",
		},
		{
			name:  "single-line json fence without newline",
			input: "```json",
			want:  "",
		},
		{
			name:  "bare fence without newline",
			input: "```",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMarkdownFences(tt.input)
			if got != tt.want {
				t.Errorf("StripMarkdownFences(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildMessageXML(t *testing.T) {
	messages := []llm.Message{
		msg("user", "hello"),
		msg("assistant", "world"),
	}
	got := buildMessageXML(messages)
	if !strings.Contains(got, `<message id="0" role="user">`) {
		t.Errorf("missing user message tag: %s", got)
	}
	if !strings.Contains(got, `<message id="1" role="assistant">`) {
		t.Errorf("missing assistant message tag: %s", got)
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Errorf("missing content: %s", got)
	}
}

func TestCopyMessages(t *testing.T) {
	orig := []llm.Message{msg("user", "a"), msg("assistant", "b")}
	cp := copyMessages(orig)
	if len(cp) != 2 {
		t.Fatalf("len = %d, want 2", len(cp))
	}
	cp[0] = msg("system", "mutated")
	if orig[0].Role == "system" {
		t.Error("copyMessages should return independent slice")
	}
}

func TestPromptTokenLimit(t *testing.T) {
	tests := []struct {
		name      string
		maxTokens int
		want      int
	}{
		{name: "zero", maxTokens: 0, want: 0},
		{name: "one truncates to zero", maxTokens: 1, want: 0},
		{name: "four truncates to three", maxTokens: 4, want: 3},
		{name: "five rounds to exact 4.0 via float half-ULP", maxTokens: 5, want: 4},
		{name: "typical 4k context", maxTokens: 4096, want: 3276},
		{name: "default max tokens", maxTokens: 58888, want: 47110},
		{name: "typical 128k context", maxTokens: 128000, want: 102400},
		{name: "typical 200k context", maxTokens: 200000, want: 160000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PromptTokenLimit(tt.maxTokens); got != tt.want {
				t.Errorf("PromptTokenLimit(%d) = %d, want %d", tt.maxTokens, got, tt.want)
			}
		})
	}
}

// TestPromptTokenLimitMatchesReplacedExpression pins the one-time migration:
// PromptTokenLimit replaced a literal `maxTokens*4/5` at four call sites, so the
// float form must agree with the integer form it replaced across the realistic
// max_tokens range. This is specific to tokenWarningThreshold being 0.80 — if the
// threshold ever changes, delete this test rather than "fixing" it.
func TestPromptTokenLimitMatchesReplacedExpression(t *testing.T) {
	for _, maxTokens := range []int{0, 1, 2, 3, 4, 5, 7, 40, 100, 1000, 4096, 8192, 32768, 58888, 128000, 200000, 1_000_000} {
		if got, want := PromptTokenLimit(maxTokens), maxTokens*4/5; got != want {
			t.Errorf("PromptTokenLimit(%d) = %d, want %d (maxTokens*4/5)", maxTokens, got, want)
		}
	}
}
