package docsassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hushine-tech/quant-handler/internal/codexcli"
	"github.com/hushine-tech/quant-handler/internal/docsstore"
)

var (
	ErrAnswerUnverified  = errors.New("docs answer could not be verified")
	ErrAssistantProtocol = errors.New("docs assistant violated the tool protocol")
)

const assistantInstructions = `You answer questions about the current Hushine deployment.
You must retrieve evidence with the available search tools before making factual claims.
Never invent a source, path, line number, document, or anchor. If retrieval returns no evidence, say so.
Return only strict JSON using this public citation shape:
{"answer":"...","citations":[{"kind":"document","title":"...","document_id":"...","anchor":"..."}]}
or {"kind":"source","repository":"...","path":"...","commit":"...","start_line":1,"end_line":2}.
Every citation must exactly copy a result returned by a search tool. Never include hit_id in the final answer.`

type AssistantOptions struct {
	CLI       codexcli.Client
	Retriever Retriever
	Model     string
}

type Assistant struct {
	cli       codexcli.Client
	retriever Retriever
	model     string
}

func NewAssistant(options AssistantOptions) (*Assistant, error) {
	if options.CLI == nil || options.Retriever == nil {
		return nil, errors.New("docs assistant requires CLI and retriever")
	}
	return &Assistant{cli: options.CLI, retriever: options.Retriever, model: strings.TrimSpace(options.Model)}, nil
}

func (a *Assistant) Ask(ctx context.Context, userID int64, scope docsstore.AccessScope, conversationID, question, currentDocumentID string) (Answer, error) {
	_ = userID
	release, err := a.cli.AcquireConversation(conversationID)
	if err != nil {
		return Answer{}, err
	}
	defer release()
	request := codexcli.ResponseRequest{
		Model:          a.model,
		ConversationID: conversationID,
		Instructions:   assistantInstructions + "\nCurrent document ID: " + currentDocumentID,
		Input: []codexcli.InputItem{{
			Type: "message", Role: "user", Content: []codexcli.Content{{
				Type: "input_text", Text: question,
			}},
		}},
		Tools:      assistantTools(scope),
		TextFormat: docsAnswerTextFormat(),
	}
	seenCalls := make(map[string]struct{})
	retrieved := make(map[string]SearchHit)
	totalCalls := 0
	toolRounds := 0
	for {
		response, err := a.cli.CreateResponse(ctx, request)
		if err != nil {
			return Answer{}, fmt.Errorf("create docs response: %w", err)
		}
		if response.Status != "completed" {
			return Answer{}, ErrAssistantProtocol
		}
		calls := functionCalls(response.Output)
		if len(calls) == 0 {
			if len(retrieved) == 0 {
				return Answer{}, ErrAnswerUnverified
			}
			answer, err := verifiedAnswer(response.OutputText, retrieved, scope)
			if err != nil {
				return Answer{}, err
			}
			if err := a.cli.SaveAnswer(ctx, conversationID, question, response.OutputText); err != nil {
				return Answer{}, err
			}
			return answer, nil
		}
		if toolRounds >= 4 || totalCalls+len(calls) > 8 {
			return Answer{}, ErrAssistantProtocol
		}
		outputs := make([]codexcli.ToolOutput, 0, len(calls))
		for _, call := range calls {
			if call.CallID == "" {
				return Answer{}, ErrAssistantProtocol
			}
			if _, duplicate := seenCalls[call.CallID]; duplicate {
				return Answer{}, ErrAssistantProtocol
			}
			seenCalls[call.CallID] = struct{}{}
			hits, err := a.executeTool(scope, call)
			if err != nil {
				return Answer{}, err
			}
			for _, hit := range hits {
				retrieved[hit.ID] = hit
			}
			encoded, err := json.Marshal(map[string]any{"hits": hits})
			if err != nil {
				return Answer{}, ErrAssistantProtocol
			}
			outputs = append(outputs, codexcli.ToolOutput{CallID: call.CallID, Output: string(encoded)})
		}
		totalCalls += len(calls)
		toolRounds++
		request.Input = nil
		request.ToolOutputs = outputs
	}
}

func docsAnswerTextFormat() *codexcli.TextFormat {
	documentCitation := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"kind":        map[string]any{"type": "string", "enum": []string{"document"}},
			"title":       map[string]any{"type": "string", "minLength": 1},
			"document_id": map[string]any{"type": "string", "minLength": 1},
			"anchor":      map[string]any{"type": "string", "minLength": 1},
		},
		"required": []string{"kind", "title", "document_id", "anchor"},
	}
	sourceCitation := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"kind":       map[string]any{"type": "string", "enum": []string{"source"}},
			"repository": map[string]any{"type": "string", "minLength": 1},
			"path":       map[string]any{"type": "string", "minLength": 1},
			"commit":     map[string]any{"type": "string", "pattern": "^[a-f0-9]{40}$"},
			"start_line": map[string]any{"type": "integer", "minimum": 1},
			"end_line":   map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"kind", "repository", "path", "commit", "start_line", "end_line"},
	}
	return &codexcli.TextFormat{
		Type: "json_schema", Name: "hushine_docs_answer", Strict: true,
		Schema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"answer": map[string]any{"type": "string", "minLength": 1},
				"citations": map[string]any{
					"type": "array", "minItems": 1,
					"items": map[string]any{"anyOf": []any{documentCitation, sourceCitation}},
				},
			},
			"required": []string{"answer", "citations"},
		},
	}
}

func assistantTools(scope docsstore.AccessScope) []codexcli.Tool {
	querySchema := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 8},
		},
		"required": []string{"query"},
	}
	tools := []codexcli.Tool{{
		Type: "function", Name: "search_docs", Description: "Search authorized Hushine documentation.",
		Parameters: querySchema, Strict: true,
	}}
	if scope == docsstore.ScopePrivileged {
		tools = append(tools, codexcli.Tool{
			Type: "function", Name: "search_source", Description: "Search exact deployed source code with commit and line coordinates.",
			Parameters: querySchema, Strict: true,
		})
	}
	return tools
}

func functionCalls(output []codexcli.Item) []codexcli.Item {
	calls := make([]codexcli.Item, 0)
	for _, item := range output {
		if item.Type == "function_call" {
			calls = append(calls, item)
		}
	}
	return calls
}

func (a *Assistant) executeTool(scope docsstore.AccessScope, call codexcli.Item) ([]SearchHit, error) {
	var arguments struct {
		Query string `json:"query"`
		Limit int    `json:"limit,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewBufferString(call.Arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil || decoder.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(arguments.Query) == "" {
		return nil, ErrAssistantProtocol
	}
	if arguments.Limit == 0 {
		arguments.Limit = 5
	}
	if arguments.Limit < 1 || arguments.Limit > 8 {
		return nil, ErrAssistantProtocol
	}
	switch call.Name {
	case "search_docs":
		return a.retriever.SearchDocs(scope, arguments.Query, arguments.Limit)
	case "search_source":
		if scope != docsstore.ScopePrivileged {
			return nil, ErrForbidden
		}
		return a.retriever.SearchSource(scope, arguments.Query, arguments.Limit)
	default:
		return nil, ErrAssistantProtocol
	}
}

func verifiedAnswer(value string, retrieved map[string]SearchHit, scope docsstore.AccessScope) (Answer, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	var answer Answer
	if err := decoder.Decode(&answer); err != nil || decoder.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(answer.Answer) == "" || len(answer.Citations) == 0 {
		return Answer{}, ErrAnswerUnverified
	}
	seen := make(map[string]struct{}, len(answer.Citations))
	for _, citation := range answer.Citations {
		if validateCitation(citation) != nil {
			return Answer{}, ErrAnswerUnverified
		}
		key := citationKey(citation)
		if _, duplicate := seen[key]; duplicate {
			return Answer{}, ErrAnswerUnverified
		}
		seen[key] = struct{}{}
		if !citationMatchesRetrieved(citation, retrieved, scope) {
			return Answer{}, ErrAnswerUnverified
		}
	}
	answer.Answer = strings.TrimSpace(answer.Answer)
	return answer, nil
}

func citationMatchesRetrieved(citation Citation, retrieved map[string]SearchHit, scope docsstore.AccessScope) bool {
	for _, hit := range retrieved {
		switch citation.Kind {
		case "document":
			if hit.Kind == "document" && hit.Title == citation.Title && hit.DocumentID == citation.DocumentID && containsString(hit.Anchors, citation.Anchor) {
				return true
			}
		case "source":
			if scope == docsstore.ScopePrivileged && hit.Kind == "source" && hit.Repository == citation.Repository && hit.Path == citation.Path &&
				hit.Commit == citation.Commit && hit.StartLine == citation.StartLine && hit.EndLine == citation.EndLine {
				return true
			}
		}
	}
	return false
}

func citationKey(citation Citation) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d", citation.Kind, citation.Title, citation.DocumentID,
		citation.Anchor, citation.Repository+"/"+citation.Path+"@"+citation.Commit, citation.StartLine, citation.EndLine)
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
