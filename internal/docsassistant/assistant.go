package docsassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
	openaiapi "github.com/hushine-tech/quant-handler/internal/openai"
)

var (
	ErrAnswerUnverified  = errors.New("docs answer could not be verified")
	ErrAssistantProtocol = errors.New("docs assistant violated the tool protocol")
)

const assistantInstructions = `You answer questions about the current Hushine deployment.
You must retrieve evidence with the available search tools before making factual claims.
Never invent a source, path, line number, document, or anchor. If retrieval returns no evidence, say so.
Return only strict JSON: {"answer":"...","citations":[{"hit_id":"...","anchor":"..."}]}.
Every factual answer must cite one or more hit_id values returned by tools. For source hits anchor must be empty.`

type AssistantOptions struct {
	OpenAI    openaiapi.Client
	Retriever Retriever
	Model     string
}

type Assistant struct {
	openAI    openaiapi.Client
	retriever Retriever
	model     string
}

func NewAssistant(options AssistantOptions) (*Assistant, error) {
	if options.OpenAI == nil || options.Retriever == nil || strings.TrimSpace(options.Model) == "" {
		return nil, errors.New("docs assistant requires OpenAI, retriever, and model")
	}
	return &Assistant{openAI: options.OpenAI, retriever: options.Retriever, model: strings.TrimSpace(options.Model)}, nil
}

func (a *Assistant) Ask(ctx context.Context, userID int64, scope docsstore.AccessScope, conversationID, question, currentDocumentID string) (Answer, error) {
	_ = userID
	request := openaiapi.ResponseRequest{
		Model:          a.model,
		ConversationID: conversationID,
		Instructions:   assistantInstructions,
		Input: []openaiapi.InputItem{{
			Type: "message", Role: "user", Content: []openaiapi.Content{{
				Type: "input_text", Text: question + "\n\nCurrent document ID: " + currentDocumentID,
			}},
		}},
		Tools:             assistantTools(scope),
		ContextManagement: []openaiapi.ContextManagement{{Type: "compaction", CompactThreshold: 100000}},
	}
	seenCalls := make(map[string]struct{})
	retrieved := make(map[string]SearchHit)
	totalCalls := 0
	toolRounds := 0
	for {
		response, err := a.openAI.CreateResponse(ctx, request)
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
			return verifiedAnswer(response.OutputText, retrieved, scope)
		}
		if toolRounds >= 4 || totalCalls+len(calls) > 8 {
			return Answer{}, ErrAssistantProtocol
		}
		outputs := make([]openaiapi.ToolOutput, 0, len(calls))
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
			outputs = append(outputs, openaiapi.ToolOutput{CallID: call.CallID, Output: string(encoded)})
		}
		totalCalls += len(calls)
		toolRounds++
		request.Input = nil
		request.ToolOutputs = outputs
	}
}

func assistantTools(scope docsstore.AccessScope) []openaiapi.Tool {
	querySchema := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 8},
		},
		"required": []string{"query"},
	}
	tools := []openaiapi.Tool{{
		Type: "function", Name: "search_docs", Description: "Search authorized Hushine documentation.",
		Parameters: querySchema, Strict: true,
	}}
	if scope == docsstore.ScopePrivileged {
		tools = append(tools, openaiapi.Tool{
			Type: "function", Name: "search_source", Description: "Search exact deployed source code with commit and line coordinates.",
			Parameters: querySchema, Strict: true,
		})
	}
	return tools
}

func functionCalls(output []openaiapi.Item) []openaiapi.Item {
	calls := make([]openaiapi.Item, 0)
	for _, item := range output {
		if item.Type == "function_call" {
			calls = append(calls, item)
		}
	}
	return calls
}

func (a *Assistant) executeTool(scope docsstore.AccessScope, call openaiapi.Item) ([]SearchHit, error) {
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

type rawAnswer struct {
	Answer    string        `json:"answer"`
	Citations []rawCitation `json:"citations"`
}

type rawCitation struct {
	HitID  string `json:"hit_id"`
	Anchor string `json:"anchor"`
}

func verifiedAnswer(value string, retrieved map[string]SearchHit, scope docsstore.AccessScope) (Answer, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	var raw rawAnswer
	if err := decoder.Decode(&raw); err != nil || decoder.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(raw.Answer) == "" || len(raw.Citations) == 0 {
		return Answer{}, ErrAnswerUnverified
	}
	citations := make([]Citation, 0, len(raw.Citations))
	seen := make(map[string]struct{}, len(raw.Citations))
	for _, requested := range raw.Citations {
		if _, duplicate := seen[requested.HitID]; duplicate {
			return Answer{}, ErrAnswerUnverified
		}
		seen[requested.HitID] = struct{}{}
		hit, ok := retrieved[requested.HitID]
		if !ok {
			return Answer{}, ErrAnswerUnverified
		}
		switch hit.Kind {
		case "document":
			if !containsString(hit.Anchors, requested.Anchor) {
				return Answer{}, ErrAnswerUnverified
			}
			citations = append(citations, Citation{
				Kind: "document", Title: hit.Title, DocumentID: hit.DocumentID, Anchor: requested.Anchor,
			})
		case "source":
			if scope != docsstore.ScopePrivileged || requested.Anchor != "" || !commitPattern.MatchString(hit.Commit) || hit.StartLine < 1 || hit.EndLine < hit.StartLine {
				return Answer{}, ErrAnswerUnverified
			}
			citations = append(citations, Citation{
				Kind: "source", Repository: hit.Repository, Path: hit.Path, Commit: hit.Commit,
				StartLine: hit.StartLine, EndLine: hit.EndLine,
			})
		default:
			return Answer{}, ErrAnswerUnverified
		}
	}
	return Answer{Answer: strings.TrimSpace(raw.Answer), Citations: citations}, nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
