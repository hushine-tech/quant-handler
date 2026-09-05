package docsassistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/hushine-tech/quant-handler/internal/codexcli"
	"github.com/hushine-tech/quant-handler/internal/docsstore"
)

var (
	ErrConversationStale = errors.New("docs conversation is stale")
	ErrMalformedHistory  = errors.New("docs conversation history is malformed")
)

var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type Citation struct {
	Kind       string `json:"kind"`
	Title      string `json:"title,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	Anchor     string `json:"anchor,omitempty"`
	Repository string `json:"repository,omitempty"`
	Path       string `json:"path,omitempty"`
	Commit     string `json:"commit,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
}

type Answer struct {
	Answer    string     `json:"answer"`
	Citations []Citation `json:"citations"`
}

type Message struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Citations []Citation `json:"citations,omitempty"`
}

type ConversationHistory struct {
	ConversationID string                `json:"conversation_id"`
	Messages       []Message             `json:"messages"`
	DocsCommit     string                `json:"docs_commit"`
	AccessScope    docsstore.AccessScope `json:"access_scope"`
}

func RestoreMessages(items []codexcli.Item) ([]Message, error) {
	messages := make([]Message, 0, len(items))
	for _, item := range items {
		if item.Type != "message" {
			continue
		}
		if item.ID == "" || (item.Status != "" && item.Status != "completed") {
			return nil, ErrMalformedHistory
		}
		switch item.Role {
		case "user":
			content, err := messageText(item.Content, "input_text")
			if err != nil {
				return nil, err
			}
			messages = append(messages, Message{ID: item.ID, Role: item.Role, Content: content})
		case "assistant":
			content, err := messageText(item.Content, "output_text")
			if err != nil {
				return nil, err
			}
			answer, err := ParseAnswer(content)
			if err != nil {
				return nil, err
			}
			messages = append(messages, Message{
				ID: item.ID, Role: item.Role, Content: answer.Answer, Citations: answer.Citations,
			})
		default:
			return nil, ErrMalformedHistory
		}
	}
	return messages, nil
}

func ValidateRestoredMessages(messages []Message, scope docsstore.AccessScope, manifest docsstore.Manifest, corpus docsstore.RetrievalCorpus) error {
	if corpus.DocsCommit != manifest.DocsCommit {
		return ErrMalformedHistory
	}
	documents := make(map[string]string, len(manifest.Documents))
	for _, document := range manifest.Documents {
		documents[document.ID] = document.Title
	}
	repositories := make(map[string]string, len(manifest.Deployment.Repositories))
	for _, repository := range manifest.Deployment.Repositories {
		repositories[repository.Name] = repository.Commit
	}
	documentAnchors := make(map[string]map[string]struct{}, len(corpus.Documents))
	for _, document := range corpus.Documents {
		anchors := make(map[string]struct{}, len(document.Anchors))
		for _, anchor := range document.Anchors {
			anchors[anchor] = struct{}{}
		}
		documentAnchors[document.ID] = anchors
	}
	sources := make(map[string]struct{}, len(corpus.Sources))
	for _, source := range corpus.Sources {
		sources[sourceCitationKey(source.Repository, source.Path, source.Commit, source.StartLine, source.EndLine)] = struct{}{}
	}
	for _, message := range messages {
		for _, citation := range message.Citations {
			switch citation.Kind {
			case "document":
				if documents[citation.DocumentID] != citation.Title {
					return ErrMalformedHistory
				}
				if _, ok := documentAnchors[citation.DocumentID][citation.Anchor]; !ok {
					return ErrMalformedHistory
				}
			case "source":
				if scope != docsstore.ScopePrivileged || repositories[citation.Repository] != citation.Commit {
					return ErrMalformedHistory
				}
				if _, ok := sources[sourceCitationKey(citation.Repository, citation.Path, citation.Commit, citation.StartLine, citation.EndLine)]; !ok {
					return ErrMalformedHistory
				}
			default:
				return ErrMalformedHistory
			}
		}
	}
	return nil
}

func sourceCitationKey(repository, path, commit string, startLine, endLine int) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", repository, path, commit, startLine, endLine)
}

func messageText(content []codexcli.Content, expectedType string) (string, error) {
	parts := make([]string, 0, len(content))
	for _, part := range content {
		if part.Type == expectedType && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	if len(parts) == 0 {
		return "", ErrMalformedHistory
	}
	return strings.Join(parts, "\n"), nil
}

func ParseAnswer(value string) (Answer, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	var answer Answer
	if err := decoder.Decode(&answer); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return Answer{}, ErrMalformedHistory
	}
	if strings.TrimSpace(answer.Answer) == "" || answer.Citations == nil {
		return Answer{}, ErrMalformedHistory
	}
	for index, citation := range answer.Citations {
		if err := validateCitation(citation); err != nil {
			return Answer{}, fmt.Errorf("%w: citation %d", ErrMalformedHistory, index)
		}
	}
	return answer, nil
}

func validateCitation(citation Citation) error {
	switch citation.Kind {
	case "document":
		if strings.TrimSpace(citation.Title) == "" || strings.TrimSpace(citation.DocumentID) == "" || strings.TrimSpace(citation.Anchor) == "" ||
			citation.Repository != "" || citation.Path != "" || citation.Commit != "" || citation.StartLine != 0 || citation.EndLine != 0 {
			return ErrMalformedHistory
		}
	case "source":
		if strings.TrimSpace(citation.Repository) == "" || strings.TrimSpace(citation.Path) == "" || !commitPattern.MatchString(citation.Commit) ||
			citation.StartLine < 1 || citation.EndLine < citation.StartLine || citation.Title != "" || citation.DocumentID != "" || citation.Anchor != "" {
			return ErrMalformedHistory
		}
	default:
		return ErrMalformedHistory
	}
	return nil
}
