package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hushine-tech/quant-handler/internal/docsassistant"
	"github.com/hushine-tech/quant-handler/internal/docsstore"
	openaiapi "github.com/hushine-tech/quant-handler/internal/openai"
)

type fakeDocsOpenAI struct {
	createdMetadata  map[string]string
	conversation     openaiapi.Conversation
	items            []openaiapi.Item
	retrieveErr      error
	listErr          error
	responseSteps    []fakeDocsResponseStep
	createCalls      int
	retrieveCalls    int
	listCalls        int
	responseCalls    int
	responseRequests []openaiapi.ResponseRequest
}

type fakeDocsResponseStep struct {
	response openaiapi.Response
	err      error
}

func (f *fakeDocsOpenAI) CreateConversation(_ context.Context, metadata map[string]string) (openaiapi.Conversation, error) {
	f.createCalls++
	f.createdMetadata = cloneStringMap(metadata)
	if f.conversation.ID == "" {
		f.conversation = openaiapi.Conversation{
			ID: "conv_test", Object: "conversation", CreatedAt: 123, Metadata: cloneStringMap(metadata),
		}
	}
	return f.conversation, nil
}

func (f *fakeDocsOpenAI) RetrieveConversation(_ context.Context, _ string) (openaiapi.Conversation, error) {
	f.retrieveCalls++
	if f.retrieveErr != nil {
		return openaiapi.Conversation{}, f.retrieveErr
	}
	return f.conversation, nil
}

func (f *fakeDocsOpenAI) ListConversationItems(_ context.Context, _ string) ([]openaiapi.Item, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]openaiapi.Item(nil), f.items...), nil
}

func (f *fakeDocsOpenAI) CreateResponse(_ context.Context, request openaiapi.ResponseRequest) (openaiapi.Response, error) {
	f.responseCalls++
	f.responseRequests = append(f.responseRequests, request)
	if len(f.responseSteps) == 0 {
		return openaiapi.Response{}, errors.New("unexpected CreateResponse")
	}
	step := f.responseSteps[0]
	f.responseSteps = f.responseSteps[1:]
	return step.response, step.err
}

func TestDocsConversationCreateBindsCurrentUserCommitAndScope(t *testing.T) {
	fake := &fakeDocsOpenAI{}
	s := newDocsConversationServer(t, fake, map[int64]struct{}{9: {}})
	mux := newHTTPMux(s)

	created := docsRequest(t, mux, s, http.MethodPost, "/api/docs/conversations", 1, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("status = %d; body=%s", created.Code, created.Body.String())
	}
	var body struct {
		ConversationID string                `json:"conversation_id"`
		DocsCommit     string                `json:"docs_commit"`
		AccessScope    docsstore.AccessScope `json:"access_scope"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ConversationID != "conv_test" || body.DocsCommit != docsTestCommit || body.AccessScope != docsstore.ScopePublic {
		t.Fatalf("body = %+v", body)
	}
	wantMetadata := docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic)
	if !equalStringMap(fake.createdMetadata, wantMetadata) {
		t.Fatalf("metadata = %#v, want %#v", fake.createdMetadata, wantMetadata)
	}
	if fake.createCalls != 1 {
		t.Fatalf("create calls = %d", fake.createCalls)
	}
}

func TestDocsConversationRestoresOnlyDisplayMessagesAndVerifiedCitations(t *testing.T) {
	fake := &fakeDocsOpenAI{}
	s := newDocsConversationServer(t, fake, map[int64]struct{}{})
	fake.conversation = openaiapi.Conversation{
		ID: "conv_test", Object: "conversation", CreatedAt: 123,
		Metadata: docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic),
	}
	fake.items = []openaiapi.Item{
		{ID: "msg_1", Type: "message", Role: "user", Status: "completed", Content: []openaiapi.Content{{Type: "input_text", Text: "钱包怎么算？"}}},
		{ID: "fc_1", Type: "function_call", CallID: "call_1", Name: "search_docs", Arguments: `{}`},
		{ID: "msg_2", Type: "message", Role: "assistant", Status: "completed", Content: []openaiapi.Content{{Type: "output_text", Text: `{"answer":"按本地账本计算。","citations":[{"kind":"document","title":"Public","document_id":"public","anchor":"wallet"}]}`}}},
	}

	restored := docsRequest(t, newHTTPMux(s), s, http.MethodGet, "/api/docs/conversations/conv_test", 1, "")
	if restored.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", restored.Code, restored.Body.String())
	}
	var body docsassistant.ConversationHistory
	if err := json.Unmarshal(restored.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ConversationID != "conv_test" || body.DocsCommit != docsTestCommit || body.AccessScope != docsstore.ScopePublic {
		t.Fatalf("history identity = %+v", body)
	}
	if len(body.Messages) != 2 || body.Messages[0].Role != "user" || body.Messages[0].Content != "钱包怎么算？" {
		t.Fatalf("messages = %+v", body.Messages)
	}
	if body.Messages[1].Content != "按本地账本计算。" || len(body.Messages[1].Citations) != 1 || body.Messages[1].Citations[0].DocumentID != "public" {
		t.Fatalf("assistant message = %+v", body.Messages[1])
	}
	if fake.retrieveCalls != 1 || fake.listCalls != 1 {
		t.Fatalf("retrieve/list calls = %d/%d", fake.retrieveCalls, fake.listCalls)
	}
}

func TestDocsConversationRejectsStaleOrMissingBeforeListing(t *testing.T) {
	for _, test := range []struct {
		name        string
		uid         int64
		privileged  map[int64]struct{}
		metadata    func(*server) map[string]string
		retrieveErr error
	}{
		{name: "other-user", uid: 2, metadata: func(s *server) map[string]string {
			return docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic)
		}},
		{name: "scope-upgrade", uid: 1, privileged: map[int64]struct{}{1: {}}, metadata: func(s *server) map[string]string {
			return docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic)
		}},
		{name: "stale-commit", uid: 1, metadata: func(s *server) map[string]string {
			return docsassistant.ConversationMetadata(s.jwtSecret, 1, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", docsstore.ScopePublic)
		}},
		{name: "upstream-not-found", uid: 1, retrieveErr: openaiapi.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeDocsOpenAI{retrieveErr: test.retrieveErr}
			s := newDocsConversationServer(t, fake, test.privileged)
			metadata := docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic)
			if test.metadata != nil {
				metadata = test.metadata(s)
			}
			fake.conversation = openaiapi.Conversation{ID: "conv_test", Object: "conversation", Metadata: metadata}

			response := docsRequest(t, newHTTPMux(s), s, http.MethodGet, "/api/docs/conversations/conv_test", test.uid, "")
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "DOCS_CONVERSATION_STALE") {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
			if fake.listCalls != 0 {
				t.Fatalf("list calls = %d, want 0", fake.listCalls)
			}
		})
	}
}

func TestDocsConversationRejectsMalformedHistoryAndDisabledChatSafely(t *testing.T) {
	t.Run("malformed-assistant-message", func(t *testing.T) {
		fake := &fakeDocsOpenAI{}
		s := newDocsConversationServer(t, fake, nil)
		fake.conversation = openaiapi.Conversation{
			ID: "conv_test", Object: "conversation",
			Metadata: docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic),
		}
		fake.items = []openaiapi.Item{{
			ID: "msg_bad", Type: "message", Role: "assistant", Status: "completed",
			Content: []openaiapi.Content{{Type: "output_text", Text: `{"answer":"missing citations"}`}},
		}}
		response := docsRequest(t, newHTTPMux(s), s, http.MethodGet, "/api/docs/conversations/conv_test", 1, "")
		if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "DOCS_CHAT_UNAVAILABLE") {
			t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
		}
	})

	for _, test := range []struct {
		name     string
		citation string
	}{
		{name: "public-source-citation", citation: `{"kind":"source","repository":"core-service","path":"wallet.go","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","start_line":1,"end_line":2}`},
		{name: "hidden-document-citation", citation: `{"kind":"document","title":"Operations","document_id":"private","anchor":"internal"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeDocsOpenAI{}
			s := newDocsConversationServer(t, fake, nil)
			fake.conversation = openaiapi.Conversation{
				ID: "conv_test", Object: "conversation",
				Metadata: docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic),
			}
			fake.items = []openaiapi.Item{{
				ID: "msg_bad", Type: "message", Role: "assistant", Status: "completed",
				Content: []openaiapi.Content{{Type: "output_text", Text: `{"answer":"not authorized","citations":[` + test.citation + `]}`}},
			}}
			response := docsRequest(t, newHTTPMux(s), s, http.MethodGet, "/api/docs/conversations/conv_test", 1, "")
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "DOCS_CHAT_UNAVAILABLE") {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
		})
	}

	t.Run("disabled", func(t *testing.T) {
		s := newDocsTestServer(t, nil)
		response := docsRequest(t, newHTTPMux(s), s, http.MethodPost, "/api/docs/conversations", 1, "")
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "DOCS_CHAT_UNAVAILABLE") {
			t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
		}
	})
}

func TestDocsAskValidatesIdentityInputAndDocumentBeforeResponseOrRateToken(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		metadata   func(*server) map[string]string
		wantStatus int
		wantError  string
	}{
		{name: "empty-question", body: `{"question":" ","current_document_id":"public"}`, wantStatus: http.StatusBadRequest, wantError: "DOCS_QUESTION_INVALID"},
		{name: "oversized-question", body: `{"question":"` + strings.Repeat("界", 8001) + `","current_document_id":"public"}`, wantStatus: http.StatusBadRequest, wantError: "DOCS_QUESTION_INVALID"},
		{name: "hidden-current-document", body: `{"question":"钱包？","current_document_id":"private"}`, wantStatus: http.StatusNotFound, wantError: "document not found"},
		{name: "stale-conversation", body: `{"question":"钱包？","current_document_id":"public"}`, metadata: func(s *server) map[string]string {
			return docsassistant.ConversationMetadata(s.jwtSecret, 2, docsTestCommit, docsstore.ScopePublic)
		}, wantStatus: http.StatusConflict, wantError: "DOCS_CONVERSATION_STALE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeDocsOpenAI{}
			s := newDocsConversationServer(t, fake, nil)
			s.docsAssistantModel = "gpt-test"
			s.docsRateLimiter = newDocsRateLimiter(1, 16, time.Hour, func() time.Time { return time.Unix(100, 0) })
			metadata := docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic)
			if test.metadata != nil {
				metadata = test.metadata(s)
			}
			fake.conversation = openaiapi.Conversation{ID: "conv_test", Object: "conversation", Metadata: metadata}

			response := docsJSONRequest(t, newHTTPMux(s), s, http.MethodPost, "/api/docs/conversations/conv_test/messages", 1, test.body)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantError) {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
			if fake.responseCalls != 0 {
				t.Fatalf("response calls = %d, want 0", fake.responseCalls)
			}
			if allowed, _ := s.docsRateLimiter.Allow(1); !allowed {
				t.Fatal("validation failure consumed a rate token")
			}
		})
	}
}

func TestDocsAskReturnsVerifiedAnswerAndDoesNotRetryUpstream(t *testing.T) {
	fake := &fakeDocsOpenAI{responseSteps: []fakeDocsResponseStep{
		{response: docsToolResponse("resp_1", "call_1", "search_docs", `{"query":"wallet","limit":5}`)},
		{response: docsTextResponse("resp_2", `{"answer":"钱包按本地账本计算。","citations":[{"kind":"document","title":"Public","document_id":"public","anchor":"public"}]}`)},
	}}
	s := newDocsConversationServer(t, fake, nil)
	s.docsAssistantModel = "gpt-test"
	s.docsRateLimiter = newDocsRateLimiter(6, 16, time.Hour, time.Now)
	fake.conversation = openaiapi.Conversation{
		ID: "conv_test", Object: "conversation",
		Metadata: docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic),
	}

	response := docsJSONRequest(t, newHTTPMux(s), s, http.MethodPost, "/api/docs/conversations/conv_test/messages", 1,
		`{"question":"钱包怎么算？","current_document_id":"public"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var body struct {
		docsassistant.Answer
		DocsCommit string `json:"docs_commit"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Answer.Answer != "钱包按本地账本计算。" || body.DocsCommit != docsTestCommit || len(body.Citations) != 1 || body.Citations[0].DocumentID != "public" {
		t.Fatalf("body = %+v", body)
	}
	if fake.responseCalls != 2 || len(fake.responseRequests) != 2 || fake.responseRequests[0].ConversationID != "conv_test" {
		t.Fatalf("response calls/requests = %d/%+v", fake.responseCalls, fake.responseRequests)
	}
	fake.items = []openaiapi.Item{
		{ID: "msg_user", Type: "message", Role: "user", Status: "completed", Content: []openaiapi.Content{{Type: "input_text", Text: "钱包怎么算？"}}},
		{ID: "msg_answer", Type: "message", Role: "assistant", Status: "completed", Content: []openaiapi.Content{{Type: "output_text", Text: `{"answer":"钱包按本地账本计算。","citations":[{"kind":"document","title":"Public","document_id":"public","anchor":"public"}]}`}}},
	}
	restored := docsRequest(t, newHTTPMux(s), s, http.MethodGet, "/api/docs/conversations/conv_test", 1, "")
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), "钱包按本地账本计算") {
		t.Fatalf("restored status=%d body=%s", restored.Code, restored.Body.String())
	}

	upstream := &fakeDocsOpenAI{responseSteps: []fakeDocsResponseStep{{err: context.DeadlineExceeded}}}
	s = newDocsConversationServer(t, upstream, nil)
	s.docsAssistantModel = "gpt-test"
	s.docsRateLimiter = newDocsRateLimiter(6, 16, time.Hour, time.Now)
	upstream.conversation = fake.conversation
	failed := docsJSONRequest(t, newHTTPMux(s), s, http.MethodPost, "/api/docs/conversations/conv_test/messages", 1,
		`{"question":"钱包怎么算？","current_document_id":"public"}`)
	if failed.Code != http.StatusBadGateway || !strings.Contains(failed.Body.String(), "DOCS_CHAT_UNAVAILABLE") || upstream.responseCalls != 1 {
		t.Fatalf("status=%d body=%s calls=%d", failed.Code, failed.Body.String(), upstream.responseCalls)
	}
}

func TestDocsAskMapsRateLimitsAndUnverifiedAnswersWithoutBreakingPhaseOne(t *testing.T) {
	now := time.Unix(100, 0)
	fake := &fakeDocsOpenAI{responseSteps: []fakeDocsResponseStep{{
		response: docsTextResponse("resp_direct", `{"answer":"guess","citations":[{"kind":"document","title":"Public","document_id":"public","anchor":"public"}]}`),
	}}}
	s := newDocsConversationServer(t, fake, nil)
	s.docsAssistantModel = "gpt-test"
	s.docsRateLimiter = newDocsRateLimiter(1, 16, time.Hour, func() time.Time { return now })
	fake.conversation = openaiapi.Conversation{
		ID: "conv_test", Object: "conversation",
		Metadata: docsassistant.ConversationMetadata(s.jwtSecret, 1, docsTestCommit, docsstore.ScopePublic),
	}
	mux := newHTTPMux(s)
	unverified := docsJSONRequest(t, mux, s, http.MethodPost, "/api/docs/conversations/conv_test/messages", 1,
		`{"question":"钱包怎么算？","current_document_id":"public"}`)
	if unverified.Code != http.StatusBadGateway || !strings.Contains(unverified.Body.String(), "DOCS_ANSWER_UNVERIFIED") {
		t.Fatalf("status=%d body=%s", unverified.Code, unverified.Body.String())
	}

	limited := docsJSONRequest(t, mux, s, http.MethodPost, "/api/docs/conversations/conv_test/messages", 1,
		`{"question":"再问一次","current_document_id":"public"}`)
	if limited.Code != http.StatusTooManyRequests || !strings.Contains(limited.Body.String(), "DOCS_RATE_LIMITED") || limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("status=%d retry=%q body=%s", limited.Code, limited.Header().Get("Retry-After"), limited.Body.String())
	}
	manifest := docsRequest(t, mux, s, http.MethodGet, "/api/docs/manifest", 1, "")
	if manifest.Code != http.StatusOK {
		t.Fatalf("phase-one manifest status=%d body=%s", manifest.Code, manifest.Body.String())
	}

	upstream := &fakeDocsOpenAI{responseSteps: []fakeDocsResponseStep{{err: &openaiapi.RateLimitError{RetryAfter: 1500 * time.Millisecond}}}}
	s = newDocsConversationServer(t, upstream, nil)
	s.docsAssistantModel = "gpt-test"
	s.docsRateLimiter = newDocsRateLimiter(6, 16, time.Hour, time.Now)
	upstream.conversation = fake.conversation
	upstreamLimited := docsJSONRequest(t, newHTTPMux(s), s, http.MethodPost, "/api/docs/conversations/conv_test/messages", 1,
		`{"question":"钱包怎么算？","current_document_id":"public"}`)
	if upstreamLimited.Code != http.StatusTooManyRequests || upstreamLimited.Header().Get("Retry-After") != "2" || upstream.responseCalls != 1 {
		t.Fatalf("status=%d retry=%q body=%s calls=%d", upstreamLimited.Code, upstreamLimited.Header().Get("Retry-After"), upstreamLimited.Body.String(), upstream.responseCalls)
	}
}

func docsJSONRequest(t *testing.T, handler http.Handler, s *server, method, target string, uid int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := authorizedDocsRequest(t, s, method, target, uid)
	request.Body = io.NopCloser(bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func docsToolResponse(id, callID, name, arguments string) openaiapi.Response {
	return openaiapi.Response{ID: id, Object: "response", Status: "completed", Output: []openaiapi.Item{{
		ID: id + "_tool", Type: "function_call", CallID: callID, Name: name, Arguments: arguments, Status: "completed",
	}}}
}

func docsTextResponse(id, output string) openaiapi.Response {
	return openaiapi.Response{ID: id, Object: "response", Status: "completed", OutputText: output, Output: []openaiapi.Item{{
		ID: id + "_message", Type: "message", Role: "assistant", Status: "completed", Content: []openaiapi.Content{{Type: "output_text", Text: output}},
	}}}
}

func newDocsConversationServer(t *testing.T, client openaiapi.Client, privileged map[int64]struct{}) *server {
	t.Helper()
	s := newDocsTestServer(t, privileged)
	s.docsOpenAI = client
	return s
}

func cloneStringMap(value map[string]string) map[string]string {
	cloned := make(map[string]string, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
