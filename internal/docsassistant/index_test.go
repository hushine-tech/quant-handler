package docsassistant

import (
	"errors"
	"strings"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
)

func TestIndexFiltersDocsAndSourceByScope(t *testing.T) {
	index := newRetrievalTestIndex(t)
	public, err := index.SearchDocs(docsstore.ScopePublic, "钱包", 8)
	if err != nil || len(public) != 1 || public[0].DocumentID != "wallet" {
		t.Fatalf("public docs = %+v, %v", public, err)
	}
	privileged, err := index.SearchDocs(docsstore.ScopePrivileged, "deployment", 8)
	if err != nil || len(privileged) != 1 || privileged[0].DocumentID != "ops" {
		t.Fatalf("privileged docs = %+v, %v", privileged, err)
	}
	if _, err := index.SearchSource(docsstore.ScopePublic, "AvailableBalance", 8); !errors.Is(err, ErrForbidden) {
		t.Fatalf("public source error = %v", err)
	}
	source, err := index.SearchSource(docsstore.ScopePrivileged, "AvailableBalance", 8)
	if err != nil || len(source) == 0 || source[0].Path != "internal/wallet/available.go" {
		t.Fatalf("privileged source = %+v, %v", source, err)
	}
}

func TestIndexRanksExactSymbolThenPathAndPreservesSourceLines(t *testing.T) {
	index := newRetrievalTestIndex(t)
	hits, err := index.SearchSource(docsstore.ScopePrivileged, "AvailableBalance", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].Path != "internal/wallet/available.go" {
		t.Fatalf("ranked hits = %+v", hits)
	}
	if hits[0].Commit != strings.Repeat("a", 40) || hits[0].StartLine != 120 || hits[0].EndLine != 150 {
		t.Fatalf("exact source coordinates = %+v", hits[0])
	}
	pathHits, err := index.SearchSource(docsstore.ScopePrivileged, "legacy/AvailableBalance", 8)
	if err != nil || len(pathHits) == 0 || pathHits[0].Path != "legacy/AvailableBalance.ts" {
		t.Fatalf("path hits = %+v, %v", pathHits, err)
	}
}

func TestIndexNormalizesUnicodeBoundsResultsAndReturnsZeroForNoMatch(t *testing.T) {
	index := newRetrievalTestIndex(t)
	hits, err := index.SearchDocs(docsstore.ScopePublic, "ＡＶＡＩＬＡＢＬＥ　ＢＡＬＡＮＣＥ", 100)
	if err != nil || len(hits) == 0 || hits[0].DocumentID != "wallet" {
		t.Fatalf("normalized hits = %+v, %v", hits, err)
	}
	if len(hits) > 8 {
		t.Fatalf("hit count = %d, want <= 8", len(hits))
	}
	total := 0
	for _, hit := range hits {
		total += len(hit.Text)
	}
	if total > 24000 {
		t.Fatalf("result text bytes = %d, want <= 24000", total)
	}
	missing, err := index.SearchDocs(docsstore.ScopePublic, "definitely-not-present", 8)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing hits = %+v, %v", missing, err)
	}
}

func newRetrievalTestIndex(t *testing.T) *Index {
	t.Helper()
	large := strings.Repeat("available balance wallet ", 2000)
	index, err := NewIndex(docsstore.RetrievalCorpus{
		DocsCommit: strings.Repeat("f", 40),
		Documents: []docsstore.RetrievalDocument{
			{ID: "wallet", Slug: "wallet", Title: "Wallet Balance", Visibility: "public", Keywords: []string{"available balance", "钱包"}, Text: "钱包 available balance 计算说明 " + large, Anchors: []string{"wallet-balance"}},
			{ID: "ops", Slug: "deployment", Title: "Deployment", Visibility: "privileged", Keywords: []string{"deployment"}, Text: "internal deployment", Anchors: []string{"deployment"}},
		},
		Sources: []docsstore.SourceChunk{
			{ID: strings.Repeat("1", 64), Repository: "core-service", Commit: strings.Repeat("a", 40), Path: "internal/wallet/available.go", StartLine: 120, EndLine: 150, Language: "go", Symbols: []string{"AvailableBalance"}, Text: "func AvailableBalance() {}"},
			{ID: strings.Repeat("2", 64), Repository: "quant-frontend", Commit: strings.Repeat("b", 40), Path: "legacy/AvailableBalance.ts", StartLine: 1, EndLine: 4, Language: "typescript", Symbols: []string{"LegacyValue"}, Text: "export const AvailableBalance = 0"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return index
}
