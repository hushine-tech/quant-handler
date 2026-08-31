package app

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hushine-tech/quant-handler/internal/docsassistant"
	"github.com/hushine-tech/quant-handler/internal/docsstore"
	openaiapi "github.com/hushine-tech/quant-handler/internal/openai"
)

var docsConversationIDPattern = regexp.MustCompile(`^conv_[A-Za-z0-9_-]+$`)

func (s *server) handleDocsConversations(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/docs/conversations" {
		writeErr(w, http.StatusNotFound, "document endpoint not found")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, scope, manifest, ok := s.docsConversationRequest(w, r)
	if !ok {
		return
	}
	metadata := docsassistant.ConversationMetadata(s.jwtSecret, uid, manifest.DocsCommit, scope)
	conversation, err := s.docsOpenAI.CreateConversation(r.Context(), metadata)
	if err != nil {
		writeDocsConversationUpstreamError(w, err, false)
		return
	}
	if !docsConversationIDPattern.MatchString(conversation.ID) ||
		docsassistant.ValidateConversationMetadata(conversation.Metadata, s.jwtSecret, uid, manifest.DocsCommit, scope) != nil {
		writeErr(w, http.StatusBadGateway, "DOCS_CHAT_UNAVAILABLE")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"conversation_id": conversation.ID,
		"docs_commit":     manifest.DocsCommit,
		"access_scope":    scope,
	})
}

func (s *server) handleDocsConversation(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, "/api/docs/conversations/")
	if strings.HasSuffix(remainder, "/messages") {
		id := strings.TrimSuffix(remainder, "/messages")
		if !docsConversationIDPattern.MatchString(id) || strings.Contains(id, "/") {
			writeErr(w, http.StatusNotFound, "conversation not found")
			return
		}
		s.handleDocsConversationMessage(w, r, id)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := remainder
	if !docsConversationIDPattern.MatchString(id) {
		writeErr(w, http.StatusNotFound, "conversation not found")
		return
	}
	uid, scope, manifest, ok := s.docsConversationRequest(w, r)
	if !ok {
		return
	}
	conversation, err := s.docsOpenAI.RetrieveConversation(r.Context(), id)
	if err != nil {
		writeDocsConversationUpstreamError(w, err, true)
		return
	}
	if conversation.ID != id || docsassistant.ValidateConversationMetadata(
		conversation.Metadata, s.jwtSecret, uid, manifest.DocsCommit, scope,
	) != nil {
		writeErr(w, http.StatusConflict, "DOCS_CONVERSATION_STALE")
		return
	}
	items, err := s.docsOpenAI.ListConversationItems(r.Context(), id)
	if err != nil {
		writeDocsConversationUpstreamError(w, err, true)
		return
	}
	messages, err := docsassistant.RestoreMessages(items)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "DOCS_CHAT_UNAVAILABLE")
		return
	}
	corpus, err := s.docs.RetrievalCorpusAt(manifest.DocsCommit)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "DOCS_CHAT_UNAVAILABLE")
		return
	}
	if err := docsassistant.ValidateRestoredMessages(messages, scope, manifest, corpus); err != nil {
		writeErr(w, http.StatusBadGateway, "DOCS_CHAT_UNAVAILABLE")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, docsassistant.ConversationHistory{
		ConversationID: id,
		Messages:       messages,
		DocsCommit:     manifest.DocsCommit,
		AccessScope:    scope,
	})
}

func (s *server) handleDocsConversationMessage(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.docsAssistantModel == "" || s.docsRateLimiter == nil {
		writeErr(w, http.StatusServiceUnavailable, "DOCS_CHAT_UNAVAILABLE")
		return
	}
	var body struct {
		Question          string `json:"question"`
		CurrentDocumentID string `json:"current_document_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeErr(w, http.StatusBadRequest, "DOCS_QUESTION_INVALID")
		return
	}
	body.Question = strings.TrimSpace(body.Question)
	_, validDocumentID := docsDocumentID(body.CurrentDocumentID)
	if body.Question == "" || utf8.RuneCountInString(body.Question) > 8000 || !validDocumentID {
		writeErr(w, http.StatusBadRequest, "DOCS_QUESTION_INVALID")
		return
	}

	uid, scope, manifest, ok := s.docsConversationRequest(w, r)
	if !ok {
		return
	}
	conversation, err := s.docsOpenAI.RetrieveConversation(r.Context(), id)
	if err != nil {
		writeDocsConversationUpstreamError(w, err, true)
		return
	}
	if conversation.ID != id || docsassistant.ValidateConversationMetadata(
		conversation.Metadata, s.jwtSecret, uid, manifest.DocsCommit, scope,
	) != nil {
		writeErr(w, http.StatusConflict, "DOCS_CONVERSATION_STALE")
		return
	}
	if _, err := s.docs.DocumentAt(scope, manifest.DocsCommit, body.CurrentDocumentID); err != nil {
		writeDocsStoreError(w, r, err)
		return
	}
	corpus, err := s.docs.RetrievalCorpusAt(manifest.DocsCommit)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "DOCS_CHAT_UNAVAILABLE")
		return
	}
	retriever, err := docsassistant.NewIndex(corpus)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "DOCS_CHAT_UNAVAILABLE")
		return
	}
	assistant, err := docsassistant.NewAssistant(docsassistant.AssistantOptions{
		OpenAI: s.docsOpenAI, Retriever: retriever, Model: s.docsAssistantModel,
	})
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "DOCS_CHAT_UNAVAILABLE")
		return
	}
	if allowed, retryAfter := s.docsRateLimiter.Allow(uid); !allowed {
		writeDocsRateLimited(w, retryAfter)
		return
	}
	answer, err := assistant.Ask(r.Context(), uid, scope, id, body.Question, body.CurrentDocumentID)
	if err != nil {
		switch {
		case errors.Is(err, docsassistant.ErrAnswerUnverified):
			writeErr(w, http.StatusBadGateway, "DOCS_ANSWER_UNVERIFIED")
		case errors.Is(err, docsassistant.ErrForbidden), errors.Is(err, docsassistant.ErrAssistantProtocol):
			writeErr(w, http.StatusBadGateway, "DOCS_CHAT_UNAVAILABLE")
		default:
			writeDocsConversationUpstreamError(w, err, false)
		}
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, struct {
		docsassistant.Answer
		DocsCommit string `json:"docs_commit"`
	}{Answer: answer, DocsCommit: manifest.DocsCommit})
}

func (s *server) docsConversationRequest(w http.ResponseWriter, r *http.Request) (int64, docsstore.AccessScope, docsstore.Manifest, bool) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing authenticated user")
		return 0, "", docsstore.Manifest{}, false
	}
	if s.docsOpenAI == nil {
		writeErr(w, http.StatusServiceUnavailable, "DOCS_CHAT_UNAVAILABLE")
		return 0, "", docsstore.Manifest{}, false
	}
	if s.docs == nil {
		writeDocsStoreError(w, r, docsstore.ErrUnavailable)
		return 0, "", docsstore.Manifest{}, false
	}
	scope := s.docsScopeForUser(uid)
	manifest, err := s.docs.Manifest(scope)
	if err != nil {
		writeDocsStoreError(w, r, err)
		return 0, "", docsstore.Manifest{}, false
	}
	return uid, scope, manifest, true
}

func writeDocsConversationUpstreamError(w http.ResponseWriter, err error, notFoundIsStale bool) {
	if notFoundIsStale && errors.Is(err, openaiapi.ErrNotFound) {
		writeErr(w, http.StatusConflict, "DOCS_CONVERSATION_STALE")
		return
	}
	var rateLimit *openaiapi.RateLimitError
	if errors.As(err, &rateLimit) {
		writeDocsRateLimited(w, rateLimit.RetryAfter)
		return
	}
	writeErr(w, http.StatusBadGateway, "DOCS_CHAT_UNAVAILABLE")
}

func writeDocsRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeErr(w, http.StatusTooManyRequests, "DOCS_RATE_LIMITED")
}
