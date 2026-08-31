package app

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

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
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/docs/conversations/")
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
	if err := docsassistant.ValidateRestoredMessages(messages, scope, manifest); err != nil {
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
	writeErr(w, http.StatusBadGateway, "DOCS_CHAT_UNAVAILABLE")
}
