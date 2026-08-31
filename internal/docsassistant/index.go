package docsassistant

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
	"golang.org/x/text/unicode/norm"
)

var ErrForbidden = errors.New("docs retrieval is forbidden")

const (
	maxSearchHits      = 8
	maxSearchTextBytes = 24000
)

type SearchHit struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title,omitempty"`
	DocumentID string   `json:"document_id,omitempty"`
	Anchors    []string `json:"anchors,omitempty"`
	Repository string   `json:"repository,omitempty"`
	Path       string   `json:"path,omitempty"`
	Commit     string   `json:"commit,omitempty"`
	StartLine  int      `json:"start_line,omitempty"`
	EndLine    int      `json:"end_line,omitempty"`
	Text       string   `json:"text"`
}

type Retriever interface {
	SearchDocs(scope docsstore.AccessScope, query string, limit int) ([]SearchHit, error)
	SearchSource(scope docsstore.AccessScope, query string, limit int) ([]SearchHit, error)
}

type Index struct {
	docsCommit string
	documents  []docsstore.RetrievalDocument
	sources    []docsstore.SourceChunk
}

func NewIndex(corpus docsstore.RetrievalCorpus) (*Index, error) {
	if !commitPattern.MatchString(corpus.DocsCommit) {
		return nil, errors.New("retrieval corpus has invalid docs commit")
	}
	return &Index{
		docsCommit: corpus.DocsCommit,
		documents:  append([]docsstore.RetrievalDocument(nil), corpus.Documents...),
		sources:    append([]docsstore.SourceChunk(nil), corpus.Sources...),
	}, nil
}

type scoredHit struct {
	hit   SearchHit
	score int
}

func (i *Index) SearchDocs(scope docsstore.AccessScope, query string, limit int) ([]SearchHit, error) {
	if scope != docsstore.ScopePublic && scope != docsstore.ScopePrivileged {
		return nil, ErrForbidden
	}
	normalized, tokens := normalizeQuery(query)
	if normalized == "" {
		return []SearchHit{}, nil
	}
	scored := make([]scoredHit, 0)
	for _, document := range i.documents {
		if document.Visibility != string(docsstore.ScopePublic) && scope != docsstore.ScopePrivileged {
			continue
		}
		score := scoreDocument(document, normalized, tokens)
		if score == 0 {
			continue
		}
		scored = append(scored, scoredHit{score: score, hit: SearchHit{
			ID: "doc:" + document.ID, Kind: "document", Title: document.Title,
			DocumentID: document.ID, Anchors: append([]string(nil), document.Anchors...), Text: document.Text,
		}})
	}
	return rankAndBound(scored, limit), nil
}

func (i *Index) SearchSource(scope docsstore.AccessScope, query string, limit int) ([]SearchHit, error) {
	if scope != docsstore.ScopePrivileged {
		return nil, ErrForbidden
	}
	normalized, tokens := normalizeQuery(query)
	if normalized == "" {
		return []SearchHit{}, nil
	}
	scored := make([]scoredHit, 0)
	for _, source := range i.sources {
		score := scoreSource(source, normalized, tokens)
		if score == 0 {
			continue
		}
		scored = append(scored, scoredHit{score: score, hit: SearchHit{
			ID: source.ID, Kind: "source", Repository: source.Repository, Path: source.Path,
			Commit: source.Commit, StartLine: source.StartLine, EndLine: source.EndLine, Text: source.Text,
		}})
	}
	return rankAndBound(scored, limit), nil
}

func normalizeQuery(value string) (string, []string) {
	normalized := strings.ToLower(strings.TrimSpace(norm.NFKC.String(value)))
	fields := strings.FieldsFunc(normalized, func(character rune) bool {
		return !(unicode.IsLetter(character) || unicode.IsNumber(character) || character == '_' || character == '/' || character == '.' || character == '-')
	})
	seen := make(map[string]struct{}, len(fields))
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if utf8.RuneCountInString(field) < 2 {
			continue
		}
		if _, ok := seen[field]; !ok {
			seen[field] = struct{}{}
			tokens = append(tokens, field)
		}
	}
	return normalized, tokens
}

func scoreDocument(document docsstore.RetrievalDocument, query string, tokens []string) int {
	title := strings.ToLower(norm.NFKC.String(document.Title))
	identity := strings.ToLower(norm.NFKC.String(document.ID + " " + document.Slug))
	text := strings.ToLower(norm.NFKC.String(document.Text))
	keywords := make([]string, len(document.Keywords))
	for index, keyword := range document.Keywords {
		keywords[index] = strings.ToLower(norm.NFKC.String(keyword))
	}
	score := 0
	if strings.Contains(title, query) || strings.Contains(identity, query) {
		score += 30
	}
	for _, token := range tokens {
		if strings.Contains(title, token) || strings.Contains(identity, token) {
			score += 30
		}
		for _, keyword := range keywords {
			if strings.Contains(keyword, token) {
				score += 20
				break
			}
		}
		if strings.Contains(text, token) {
			score++
		}
	}
	if len(tokens) == 0 && strings.Contains(text, query) {
		score++
	}
	return score
}

func scoreSource(source docsstore.SourceChunk, query string, tokens []string) int {
	path := strings.ToLower(norm.NFKC.String(source.Path))
	text := strings.ToLower(norm.NFKC.String(source.Text))
	symbols := make([]string, len(source.Symbols))
	for index, symbol := range source.Symbols {
		symbols[index] = strings.ToLower(norm.NFKC.String(symbol))
	}
	score := 0
	if strings.Contains(path, query) {
		score += 30
	}
	for _, symbol := range symbols {
		if symbol == query {
			score += 100
		}
	}
	for _, token := range tokens {
		if strings.Contains(path, token) {
			score += 30
		}
		for _, symbol := range symbols {
			if symbol == token {
				score += 100
			} else if strings.Contains(symbol, token) {
				score += 20
			}
		}
		if strings.Contains(text, token) {
			score++
		}
	}
	if len(tokens) == 0 && strings.Contains(text, query) {
		score++
	}
	return score
}

func rankAndBound(scored []scoredHit, limit int) []SearchHit {
	if limit <= 0 || limit > maxSearchHits {
		limit = maxSearchHits
	}
	sort.SliceStable(scored, func(left, right int) bool {
		if scored[left].score != scored[right].score {
			return scored[left].score > scored[right].score
		}
		return scored[left].hit.ID < scored[right].hit.ID
	})
	results := make([]SearchHit, 0, min(limit, len(scored)))
	remaining := maxSearchTextBytes
	for _, item := range scored {
		if len(results) == limit || remaining == 0 {
			break
		}
		hit := item.hit
		hit.Text = truncateUTF8(hit.Text, remaining)
		remaining -= len(hit.Text)
		results = append(results, hit)
	}
	return results
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
