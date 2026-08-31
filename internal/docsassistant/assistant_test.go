package docsassistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
	openaiapi "github.com/hushine-tech/quant-handler/internal/openai"
)

type assistantStep struct {
	response openaiapi.Response
	err      error
}

type scriptedAssistantOpenAI struct {
	steps    []assistantStep
	requests []openaiapi.ResponseRequest
}

func (s *scriptedAssistantOpenAI) CreateConversation(context.Context, map[string]string) (openaiapi.Conversation, error) {
	return openaiapi.Conversation{}, errors.New("unexpected CreateConversation")
}
func (s *scriptedAssistantOpenAI) RetrieveConversation(context.Context, string) (openaiapi.Conversation, error) {
	return openaiapi.Conversation{}, errors.New("unexpected RetrieveConversation")
}
func (s *scriptedAssistantOpenAI) ListConversationItems(context.Context, string) ([]openaiapi.Item, error) {
	return nil, errors.New("unexpected ListConversationItems")
}
func (s *scriptedAssistantOpenAI) CreateResponse(_ context.Context, request openaiapi.ResponseRequest) (openaiapi.Response, error) {
	s.requests = append(s.requests, request)
	if len(s.steps) == 0 {
		return openaiapi.Response{}, errors.New("unexpected CreateResponse")
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step.response, step.err
}

func TestAssistantRunsDocsToolLoopAndBuildsCitationFromRetrievedHit(t *testing.T) {
	client := &scriptedAssistantOpenAI{steps: []assistantStep{
		{response: toolResponse("resp_1", "call_1", "search_docs", `{"query":"钱包","limit":5}`)},
		{response: textResponse("resp_2", `{"answer":"钱包按本地账本计算。","citations":[{"kind":"document","title":"Wallet Balance","document_id":"wallet","anchor":"wallet-balance"}]}`)},
	}}
	assistant := newTestAssistant(t, client)
	answer, err := assistant.Ask(context.Background(), 42, docsstore.ScopePublic, "conv_test", "钱包怎么算？", "wallet")
	if err != nil {
		t.Fatal(err)
	}
	if answer.Answer != "钱包按本地账本计算。" || len(answer.Citations) != 1 {
		t.Fatalf("answer = %+v", answer)
	}
	citation := answer.Citations[0]
	if citation.Kind != "document" || citation.DocumentID != "wallet" || citation.Title != "Wallet Balance" || citation.Anchor != "wallet-balance" {
		t.Fatalf("citation = %+v", citation)
	}
	if len(client.requests) != 2 || len(client.requests[1].ToolOutputs) != 1 || !strings.Contains(client.requests[1].ToolOutputs[0].Output, `"id":"doc:wallet"`) {
		t.Fatalf("requests = %+v", client.requests)
	}
	for _, request := range client.requests {
		if request.ConversationID != "conv_test" || len(request.ContextManagement) != 1 || request.ContextManagement[0].Type != "compaction" {
			t.Fatalf("conversation/context management = %+v", request)
		}
	}
	if len(client.requests[0].Tools) != 1 || client.requests[0].Tools[0].Name != "search_docs" {
		t.Fatalf("public tools = %+v", client.requests[0].Tools)
	}
}

func TestAssistantPrivilegedSourceCitationUsesExactIndexedCoordinates(t *testing.T) {
	client := &scriptedAssistantOpenAI{steps: []assistantStep{
		{response: toolResponse("resp_1", "call_1", "search_source", `{"query":"AvailableBalance","limit":3}`)},
		{response: textResponse("resp_2", `{"answer":"实现位于 core-service。","citations":[{"kind":"source","repository":"core-service","path":"internal/wallet/available.go","commit":"`+strings.Repeat("a", 40)+`","start_line":120,"end_line":150}]}`)},
	}}
	assistant := newTestAssistant(t, client)
	answer, err := assistant.Ask(context.Background(), 42, docsstore.ScopePrivileged, "conv_test", "源码在哪里？", "ops")
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.Citations) != 1 {
		t.Fatalf("answer = %+v", answer)
	}
	citation := answer.Citations[0]
	if citation.Kind != "source" || citation.Repository != "core-service" || citation.Commit != strings.Repeat("a", 40) || citation.StartLine != 120 || citation.EndLine != 150 {
		t.Fatalf("citation = %+v", citation)
	}
	if len(client.requests[0].Tools) != 2 || client.requests[0].Tools[1].Name != "search_source" {
		t.Fatalf("privileged tools = %+v", client.requests[0].Tools)
	}
}

func TestAssistantRejectsUnretrievedInvalidAndUnboundedAnswers(t *testing.T) {
	upstream := errors.New("upstream unavailable")
	tests := []struct {
		name  string
		steps []assistantStep
		want  error
	}{
		{name: "direct-answer", steps: []assistantStep{{response: textResponse("r", `{"answer":"guess","citations":[{"kind":"document","title":"Wallet Balance","document_id":"wallet","anchor":"wallet-balance"}]}`)}}, want: ErrAnswerUnverified},
		{name: "unknown-tool", steps: []assistantStep{{response: toolResponse("r", "c", "shell", `{}`)}}, want: ErrAssistantProtocol},
		{name: "public-source-tool", steps: []assistantStep{{response: toolResponse("r", "c", "search_source", `{"query":"wallet"}`)}}, want: ErrForbidden},
		{name: "citation-outside-results", steps: []assistantStep{{response: toolResponse("r1", "c1", "search_docs", `{"query":"钱包"}`)}, {response: textResponse("r2", `{"answer":"bad","citations":[{"kind":"document","title":"Other","document_id":"other","anchor":"x"}]}`)}}, want: ErrAnswerUnverified},
		{name: "missing-citation", steps: []assistantStep{{response: toolResponse("r1", "c1", "search_docs", `{"query":"钱包"}`)}, {response: textResponse("r2", `{"answer":"bad","citations":[]}`)}}, want: ErrAnswerUnverified},
		{name: "invalid-anchor", steps: []assistantStep{{response: toolResponse("r1", "c1", "search_docs", `{"query":"钱包"}`)}, {response: textResponse("r2", `{"answer":"bad","citations":[{"kind":"document","title":"Wallet Balance","document_id":"wallet","anchor":"invented"}]}`)}}, want: ErrAnswerUnverified},
		{name: "forged-fields", steps: []assistantStep{{response: toolResponse("r1", "c1", "search_docs", `{"query":"钱包"}`)}, {response: textResponse("r2", `{"answer":"bad","citations":[{"kind":"document","title":"Wallet Balance","document_id":"wallet","anchor":"wallet-balance","commit":"`+strings.Repeat("b", 40)+`"}]}`)}}, want: ErrAnswerUnverified},
		{name: "upstream", steps: []assistantStep{{err: upstream}}, want: upstream},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &scriptedAssistantOpenAI{steps: append([]assistantStep(nil), test.steps...)}
			assistant := newTestAssistant(t, client)
			_, err := assistant.Ask(context.Background(), 42, docsstore.ScopePublic, "conv_test", "question", "wallet")
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %T %v, want %v", err, err, test.want)
			}
		})
	}
}

func TestAssistantRejectsRepeatedCallsAndMoreThanFourToolRounds(t *testing.T) {
	t.Run("repeated-call-id", func(t *testing.T) {
		client := &scriptedAssistantOpenAI{steps: []assistantStep{
			{response: toolResponse("r1", "same", "search_docs", `{"query":"钱包"}`)},
			{response: toolResponse("r2", "same", "search_docs", `{"query":"钱包"}`)},
		}}
		_, err := newTestAssistant(t, client).Ask(context.Background(), 1, docsstore.ScopePublic, "conv_test", "q", "wallet")
		if !errors.Is(err, ErrAssistantProtocol) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("five-rounds", func(t *testing.T) {
		steps := make([]assistantStep, 5)
		for index := range steps {
			steps[index].response = toolResponse("r", "call_"+string(rune('a'+index)), "search_docs", `{"query":"钱包"}`)
		}
		client := &scriptedAssistantOpenAI{steps: steps}
		_, err := newTestAssistant(t, client).Ask(context.Background(), 1, docsstore.ScopePublic, "conv_test", "q", "wallet")
		if !errors.Is(err, ErrAssistantProtocol) || len(client.requests) != 5 {
			t.Fatalf("error = %v requests=%d", err, len(client.requests))
		}
	})
}

func newTestAssistant(t *testing.T, client openaiapi.Client) *Assistant {
	t.Helper()
	index := newRetrievalTestIndex(t)
	assistant, err := NewAssistant(AssistantOptions{OpenAI: client, Retriever: index, Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	return assistant
}

func toolResponse(id, callID, name, arguments string) openaiapi.Response {
	return openaiapi.Response{ID: id, Object: "response", Status: "completed", Output: []openaiapi.Item{{
		ID: id + "_tool", Type: "function_call", CallID: callID, Name: name, Arguments: arguments, Status: "completed",
	}}}
}

func textResponse(id, output string) openaiapi.Response {
	return openaiapi.Response{ID: id, Object: "response", Status: "completed", OutputText: output, Output: []openaiapi.Item{{
		ID: id + "_message", Type: "message", Role: "assistant", Status: "completed", Content: []openaiapi.Content{{Type: "output_text", Text: output}},
	}}}
}
