package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/docsassistant"
	"github.com/hushine-tech/quant-handler/internal/docsstore"
	openaiapi "github.com/hushine-tech/quant-handler/internal/openai"
)

type fakeDocsOpenAI struct {
	createdMetadata map[string]string
	conversation    openaiapi.Conversation
	items           []openaiapi.Item
	retrieveErr     error
	listErr         error
	createCalls     int
	retrieveCalls   int
	listCalls       int
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

func (f *fakeDocsOpenAI) CreateResponse(context.Context, openaiapi.ResponseRequest) (openaiapi.Response, error) {
	return openaiapi.Response{}, errors.New("unexpected CreateResponse")
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
