package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientUsesExactConversationAndResponseContracts(t *testing.T) {
	t.Helper()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if got := r.Header.Get("Content-Type"); r.Method == http.MethodPost && got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/conversations":
			var body map[string]any
			decodeRequest(t, r, &body)
			metadata, _ := body["metadata"].(map[string]any)
			if metadata["docs_commit"] != "abc" || len(body) != 1 {
				t.Fatalf("create conversation body = %#v", body)
			}
			writeJSON(t, w, map[string]any{
				"id": "conv_123", "object": "conversation", "created_at": 123,
				"metadata": map[string]string{"docs_commit": "abc"},
			})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/conversations/conv_123":
			writeJSON(t, w, map[string]any{
				"id": "conv_123", "object": "conversation", "created_at": 123,
				"metadata": map[string]string{"docs_commit": "abc"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/conversations/conv_123/items":
			if r.URL.Query().Get("order") != "asc" || r.URL.Query().Get("limit") != "100" {
				t.Fatalf("list query = %q", r.URL.RawQuery)
			}
			if r.URL.Query().Get("after") == "" {
				writeJSON(t, w, map[string]any{
					"object": "list", "has_more": true, "last_id": "msg_1",
					"data": []any{map[string]any{
						"id": "msg_1", "type": "message", "role": "user", "status": "completed",
						"content": []any{map[string]any{"type": "input_text", "text": "问题"}},
					}},
				})
				return
			}
			if r.URL.Query().Get("after") != "msg_1" {
				t.Fatalf("pagination after = %q", r.URL.Query().Get("after"))
			}
			writeJSON(t, w, map[string]any{
				"object": "list", "has_more": false, "last_id": "msg_2",
				"data": []any{map[string]any{
					"id": "msg_2", "type": "message", "role": "assistant", "status": "completed",
					"content": []any{map[string]any{"type": "output_text", "text": "答案"}},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			var body map[string]any
			decodeRequest(t, r, &body)
			if body["model"] != "gpt-test" || body["conversation"] != "conv_123" {
				t.Fatalf("response identity = %#v", body)
			}
			if body["instructions"] != "retrieve first" || body["parallel_tool_calls"] != false {
				t.Fatalf("response policy = %#v", body)
			}
			input, _ := body["input"].([]any)
			if len(input) != 2 {
				t.Fatalf("response input = %#v", input)
			}
			toolOutput, _ := input[1].(map[string]any)
			if toolOutput["type"] != "function_call_output" || toolOutput["call_id"] != "call_1" {
				t.Fatalf("tool output = %#v", toolOutput)
			}
			management, _ := body["context_management"].([]any)
			if len(management) != 1 || management[0].(map[string]any)["type"] != "compaction" {
				t.Fatalf("context_management = %#v", management)
			}
			text, _ := body["text"].(map[string]any)
			format, _ := text["format"].(map[string]any)
			if format["type"] != "json_schema" || format["name"] != "hushine_docs_answer" || format["strict"] != true {
				t.Fatalf("structured output = %#v", text)
			}
			schema, _ := format["schema"].(map[string]any)
			if schema["type"] != "object" || schema["additionalProperties"] != false {
				t.Fatalf("structured output schema = %#v", schema)
			}
			writeJSON(t, w, map[string]any{
				"id": "resp_1", "object": "response", "status": "completed", "output_text": "答案",
				"conversation": map[string]string{"id": "conv_123"},
				"output": []any{
					map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call_2", "name": "search_docs", "arguments": `{"query":"钱包"}`, "status": "completed"},
					map[string]any{"id": "msg_3", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "答案"}}},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Options{
		BaseURL: server.URL + "/v1", APIKey: "test-key", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conversation, err := client.CreateConversation(ctx, map[string]string{"docs_commit": "abc"})
	if err != nil || conversation.ID != "conv_123" || conversation.Metadata["docs_commit"] != "abc" {
		t.Fatalf("CreateConversation = %+v, %v", conversation, err)
	}
	conversation, err = client.RetrieveConversation(ctx, "conv_123")
	if err != nil || conversation.CreatedAt != 123 {
		t.Fatalf("RetrieveConversation = %+v, %v", conversation, err)
	}
	items, err := client.ListConversationItems(ctx, "conv_123")
	if err != nil || len(items) != 2 || items[0].Content[0].Text != "问题" || items[1].Content[0].Text != "答案" {
		t.Fatalf("ListConversationItems = %+v, %v", items, err)
	}
	response, err := client.CreateResponse(ctx, ResponseRequest{
		Model: "gpt-test", ConversationID: "conv_123", Instructions: "retrieve first",
		Input: []InputItem{{
			Type: "message", Role: "user",
			Content: []Content{{Type: "input_text", Text: "问题"}},
		}},
		Tools: []Tool{{
			Type: "function", Name: "search_docs", Description: "search",
			Parameters: map[string]any{"type": "object"}, Strict: true,
		}},
		ToolOutputs:       []ToolOutput{{CallID: "call_1", Output: `{"hits":[]}`}},
		ContextManagement: []ContextManagement{{Type: "compaction", CompactThreshold: 20000}},
		TextFormat: &TextFormat{
			Type: "json_schema", Name: "hushine_docs_answer", Strict: true,
			Schema: map[string]any{"type": "object", "additionalProperties": false},
		},
	})
	if err != nil || response.ID != "resp_1" || response.OutputText != "答案" {
		t.Fatalf("CreateResponse = %+v, %v", response, err)
	}
	if len(response.Output) != 2 || response.Output[0].Name != "search_docs" || response.Output[0].CallID != "call_2" {
		t.Fatalf("response output = %+v", response.Output)
	}
	if requests != 5 {
		t.Fatalf("requests = %d, want 5", requests)
	}
}

func TestClientEscapesOpaqueConversationIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/conversations/conv_a%2Fb%20c" {
			t.Fatalf("escaped path = %q", r.URL.EscapedPath())
		}
		writeJSON(t, w, map[string]any{
			"id": "conv_a/b c", "object": "conversation", "created_at": 1, "metadata": map[string]string{},
		})
	}))
	defer server.Close()
	client, err := NewClient(Options{BaseURL: server.URL + "/v1", APIKey: "key", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RetrieveConversation(context.Background(), "conv_a/b c"); err != nil {
		t.Fatal(err)
	}
}

func TestClientClassifiesSafeUpstreamErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		retryAfter string
		check      func(error) bool
	}{
		{name: "not-found", status: http.StatusNotFound, check: func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{name: "rate-limit", status: http.StatusTooManyRequests, retryAfter: "7", check: func(err error) bool {
			var rate *RateLimitError
			return errors.As(err, &rate) && rate.RetryAfter == 7*time.Second
		}},
		{name: "server", status: http.StatusBadGateway, check: func(err error) bool {
			var upstream *UpstreamError
			return errors.As(err, &upstream) && upstream.Status == http.StatusBadGateway && upstream.RequestID == "req_safe"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			secret := "sk-test-never-log"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("x-request-id", "req_safe")
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
				_, _ = fmt.Fprintf(w, `{"error":{"message":"body contains %s"}}`, secret)
			}))
			defer server.Close()
			client, err := NewClient(Options{BaseURL: server.URL, APIKey: secret, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.RetrieveConversation(context.Background(), "conv_1")
			if err == nil || !test.check(err) {
				t.Fatalf("error = %T %v", err, err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "body contains") {
				t.Fatalf("error leaked upstream body or key: %v", err)
			}
		})
	}
}

func TestClientRejectsMalformedOversizedAndTimedOutResponses(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not-json"))
		}))
		defer server.Close()
		client, err := NewClient(Options{BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.RetrieveConversation(context.Background(), "conv_1"); !errors.Is(err, ErrMalformedResponse) {
			t.Fatalf("error = %T %v", err, err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"` + strings.Repeat("x", 512) + `"}`))
		}))
		defer server.Close()
		client, err := NewClient(Options{
			BaseURL: server.URL, APIKey: "key", HTTPClient: server.Client(), MaxResponseBodyBytes: 128,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.RetrieveConversation(context.Background(), "conv_1"); !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("error = %T %v", err, err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			writeJSON(t, w, map[string]any{"id": "conv_late"})
		}))
		defer server.Close()
		client, err := NewClient(Options{
			BaseURL: server.URL, APIKey: "key", HTTPClient: &http.Client{Timeout: 10 * time.Millisecond},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.RetrieveConversation(context.Background(), "conv_1")
		if err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %T %v", err, err)
		}
	})
}

func decodeRequest(t *testing.T, r *http.Request, destination any) {
	t.Helper()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
