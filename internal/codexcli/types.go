// Package codexcli adapts authenticated, non-interactive Codex CLI sessions to
// the documentation assistant's read-only retrieval protocol. It has no HTTP
// model client or API-key authentication path.
package codexcli

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("docs conversation not found")
	ErrBusy              = errors.New("docs conversation is busy")
	ErrExecution         = errors.New("Codex CLI did not complete the request")
	ErrMalformedResponse = errors.New("Codex CLI returned an invalid response")
)

type Client interface {
	CreateConversation(context.Context, map[string]string) (Conversation, error)
	RetrieveConversation(context.Context, string) (Conversation, error)
	ListConversationItems(context.Context, string) ([]Item, error)
	CreateResponse(context.Context, ResponseRequest) (Response, error)
	AcquireConversation(string) (func(), error)
	SaveAnswer(context.Context, string, string, string) error
}

type Options struct {
	Binary, StateDir string
	Timeout          time.Duration
}
type Conversation struct {
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata"`
}
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
type Item struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Role      string    `json:"role,omitempty"`
	Status    string    `json:"status,omitempty"`
	Content   []Content `json:"content,omitempty"`
	CallID    string    `json:"call_id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Arguments string    `json:"arguments,omitempty"`
}
type InputItem struct {
	Type    string    `json:"type"`
	Role    string    `json:"role"`
	Content []Content `json:"content"`
}
type Tool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}
type ToolOutput struct {
	CallID string `json:"call_id"`
	Output string `json:"output"`
}
type TextFormat struct {
	Type, Name string
	Strict     bool
	Schema     map[string]any
}
type ResponseRequest struct {
	Model, ConversationID, Instructions string
	Input                               []InputItem
	Tools                               []Tool
	ToolOutputs                         []ToolOutput
	TextFormat                          *TextFormat
}
type Response struct {
	ID, Status, OutputText string
	Output                 []Item
}
