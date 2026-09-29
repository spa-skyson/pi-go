package provider

// Tests for LLMOptions.Temperature: the value must reach the wire on the
// OpenAI chat, OpenAI Responses and Anthropic paths, and stay off the wire
// entirely when unset — a test against the params struct cannot tell those
// apart (see the reasoning-effort tests for the same reasoning).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func temperatureOpt(t float64) *float64 { return &t }

// captureOpenAITemperature runs one non-streaming turn against a stub endpoint
// and returns the decoded request body (see captureOpenAIRequest for the
// response shape, which answers both endpoints).
func captureOpenAITemperature(t *testing.T, modelID string, llmOpts *LLMOptions) map[string]any {
	t.Helper()
	return captureOpenAIRequest(t, modelID, llmOpts, nil)
}

func TestOpenAIChatSendsTemperature(t *testing.T) {
	// gpt-4.1 is neither Responses-only nor codex, so this turn rides the
	// Chat Completions path.
	body := captureOpenAITemperature(t, "gpt-4.1", &LLMOptions{Temperature: temperatureOpt(0.3)})
	if got := body["temperature"]; got != float64(0.3) {
		t.Errorf("temperature = %v, want 0.3", got)
	}
}

func TestOpenAIResponsesSendsTemperature(t *testing.T) {
	body := captureOpenAITemperature(t, "gpt-6-astra", &LLMOptions{Temperature: temperatureOpt(0.3)})
	if got := body["temperature"]; got != float64(0.3) {
		t.Errorf("temperature = %v, want 0.3", got)
	}
}

func TestOpenAITemperatureUnsetStaysOffTheWire(t *testing.T) {
	for _, modelID := range []string{"gpt-4.1", "gpt-6-astra"} {
		body := captureOpenAITemperature(t, modelID, nil)
		if _, ok := body["temperature"]; ok {
			t.Errorf("%s: temperature sent without an explicit value: %v", modelID, body["temperature"])
		}
	}
}

// captureAnthropicTemperature runs one non-streaming Anthropic turn against a
// stub endpoint and returns the decoded request body.
func captureAnthropicTemperature(t *testing.T, thinkingLevel string, llmOpts *LLMOptions) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := readAll(r)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-sonnet-5",
			"content":     []map[string]any{{"type": "text", "text": "hi"}},
			"stop_reason": "end_turn", "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	llm, err := NewAnthropic(context.Background(), "claude-sonnet-5", "sk-test", srv.URL, thinkingLevel, llmOpts)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}
	return body
}

func TestAnthropicSendsTemperature(t *testing.T) {
	body := captureAnthropicTemperature(t, "none", &LLMOptions{Temperature: temperatureOpt(0.3)})
	if got := body["temperature"]; got != float64(0.3) {
		t.Errorf("temperature = %v, want 0.3", got)
	}
}

func TestAnthropicTemperatureUnsetStaysOffTheWire(t *testing.T) {
	body := captureAnthropicTemperature(t, "none", nil)
	if _, ok := body["temperature"]; ok {
		t.Errorf("temperature sent without an explicit value: %v", body["temperature"])
	}
}

// Anthropic rejects any temperature other than 1 alongside extended thinking,
// so the caller's value must not ride a thinking request.
func TestAnthropicTemperatureDroppedWhenThinking(t *testing.T) {
	body := captureAnthropicTemperature(t, "medium", &LLMOptions{Temperature: temperatureOpt(0.3)})
	if _, ok := body["temperature"]; ok {
		t.Errorf("temperature sent with thinking enabled: %v", body["temperature"])
	}
}
