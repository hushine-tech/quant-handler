package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultMaxResponseBodyBytes int64 = 4 << 20

var (
	ErrNotFound          = errors.New("openai resource not found")
	ErrMalformedResponse = errors.New("openai returned a malformed response")
	ErrResponseTooLarge  = errors.New("openai response exceeded the size limit")
)

type Client interface {
	CreateConversation(ctx context.Context, metadata map[string]string) (Conversation, error)
	RetrieveConversation(ctx context.Context, id string) (Conversation, error)
	ListConversationItems(ctx context.Context, id string) ([]Item, error)
	CreateResponse(ctx context.Context, request ResponseRequest) (Response, error)
}

type Options struct {
	BaseURL              string
	APIKey               string
	HTTPClient           *http.Client
	MaxResponseBodyBytes int64
}

type Conversation struct {
	ID        string            `json:"id"`
	Object    string            `json:"object"`
	CreatedAt int64             `json:"created_at"`
	Metadata  map[string]string `json:"metadata"`
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
	Role    string    `json:"role,omitempty"`
	Content []Content `json:"content,omitempty"`
}

type Tool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

type ToolOutput struct {
	CallID string
	Output string
}

type ContextManagement struct {
	Type             string `json:"type"`
	CompactThreshold int    `json:"compact_threshold,omitempty"`
}

type ResponseRequest struct {
	Model             string
	ConversationID    string
	Instructions      string
	Input             []InputItem
	Tools             []Tool
	ToolOutputs       []ToolOutput
	ContextManagement []ContextManagement
}

type Response struct {
	ID           string `json:"id"`
	Object       string `json:"object"`
	Status       string `json:"status"`
	OutputText   string `json:"output_text"`
	Conversation struct {
		ID string `json:"id"`
	} `json:"conversation"`
	Output []Item `json:"output"`
}

type RateLimitError struct {
	RetryAfter time.Duration
	RequestID  string
}

func (e *RateLimitError) Error() string {
	return "openai rate limit exceeded"
}

type UpstreamError struct {
	Status    int
	RequestID string
}

func (e *UpstreamError) Error() string {
	if e.RequestID == "" {
		return fmt.Sprintf("openai request failed with status %d", e.Status)
	}
	return fmt.Sprintf("openai request failed with status %d (request_id=%s)", e.Status, e.RequestID)
}

type httpClient struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	maxBodyBytes int64
}

func NewClient(options Options) (Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("openai base URL must be an absolute HTTP(S) URL")
	}
	if strings.TrimSpace(options.APIKey) == "" {
		return nil, fmt.Errorf("openai API key is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	maxBodyBytes := options.MaxResponseBodyBytes
	if maxBodyBytes == 0 {
		maxBodyBytes = defaultMaxResponseBodyBytes
	}
	if maxBodyBytes < 1 {
		return nil, fmt.Errorf("openai response body limit must be positive")
	}
	return &httpClient{
		baseURL: baseURL, apiKey: options.APIKey, httpClient: client, maxBodyBytes: maxBodyBytes,
	}, nil
}

func (c *httpClient) CreateConversation(ctx context.Context, metadata map[string]string) (Conversation, error) {
	var conversation Conversation
	err := c.doJSON(ctx, http.MethodPost, "/conversations", map[string]any{"metadata": metadata}, &conversation)
	if err != nil {
		return Conversation{}, err
	}
	if !validConversation(conversation) {
		return Conversation{}, ErrMalformedResponse
	}
	return conversation, nil
}

func (c *httpClient) RetrieveConversation(ctx context.Context, id string) (Conversation, error) {
	var conversation Conversation
	err := c.doJSON(ctx, http.MethodGet, "/conversations/"+url.PathEscape(id), nil, &conversation)
	if err != nil {
		return Conversation{}, err
	}
	if !validConversation(conversation) {
		return Conversation{}, ErrMalformedResponse
	}
	return conversation, nil
}

func validConversation(conversation Conversation) bool {
	return conversation.ID != "" && conversation.Object == "conversation" && conversation.Metadata != nil
}

func (c *httpClient) ListConversationItems(ctx context.Context, id string) ([]Item, error) {
	items := make([]Item, 0)
	after := ""
	for pageNumber := 0; pageNumber < 1000; pageNumber++ {
		query := url.Values{"order": {"asc"}, "limit": {"100"}}
		if after != "" {
			query.Set("after", after)
		}
		var page struct {
			Object  string `json:"object"`
			Data    []Item `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		path := "/conversations/" + url.PathEscape(id) + "/items?" + query.Encode()
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		if page.Object != "list" || page.Data == nil {
			return nil, ErrMalformedResponse
		}
		items = append(items, page.Data...)
		if !page.HasMore {
			return items, nil
		}
		if page.LastID == "" || page.LastID == after {
			return nil, ErrMalformedResponse
		}
		after = page.LastID
	}
	return nil, ErrMalformedResponse
}

func (c *httpClient) CreateResponse(ctx context.Context, request ResponseRequest) (Response, error) {
	input := make([]any, 0, len(request.Input)+len(request.ToolOutputs))
	for _, item := range request.Input {
		input = append(input, item)
	}
	for _, output := range request.ToolOutputs {
		input = append(input, struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		}{Type: "function_call_output", CallID: output.CallID, Output: output.Output})
	}
	payload := struct {
		Model             string              `json:"model"`
		Conversation      string              `json:"conversation"`
		Instructions      string              `json:"instructions"`
		Input             []any               `json:"input"`
		Tools             []Tool              `json:"tools,omitempty"`
		ParallelToolCalls bool                `json:"parallel_tool_calls"`
		ContextManagement []ContextManagement `json:"context_management,omitempty"`
	}{
		Model: request.Model, Conversation: request.ConversationID,
		Instructions: request.Instructions, Input: input, Tools: request.Tools,
		ParallelToolCalls: false, ContextManagement: request.ContextManagement,
	}
	var response Response
	if err := c.doJSON(ctx, http.MethodPost, "/responses", payload, &response); err != nil {
		return Response{}, err
	}
	if response.ID == "" || response.Object != "response" || response.Status == "" || response.Output == nil {
		return Response{}, ErrMalformedResponse
	}
	return response, nil
}

func (c *httpClient) doJSON(ctx context.Context, method, path string, requestBody, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode openai request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build openai request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("openai request failed: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, c.maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read openai response: %w", err)
	}
	if int64(len(data)) > c.maxBodyBytes {
		return ErrResponseTooLarge
	}
	requestID := safeRequestID(response.Header.Get("x-request-id"))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		switch response.StatusCode {
		case http.StatusNotFound:
			return ErrNotFound
		case http.StatusTooManyRequests:
			return &RateLimitError{RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()), RequestID: requestID}
		default:
			return &UpstreamError{Status: response.StatusCode, RequestID: requestID}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(responseBody); err != nil {
		return ErrMalformedResponse
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ErrMalformedResponse
	}
	return nil
}

func safeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if !(character == '-' || character == '_' || character == '.' || character >= '0' && character <= '9' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z') {
			return ""
		}
	}
	return value
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if timestamp, err := http.ParseTime(value); err == nil && timestamp.After(now) {
		return timestamp.Sub(now)
	}
	return 0
}
