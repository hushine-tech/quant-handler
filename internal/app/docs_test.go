package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
)

const docsTestCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestDocsRequireBearerAuthenticationAndGET(t *testing.T) {
	s := newDocsTestServer(t, map[int64]struct{}{9: {}})
	handler := s.cors(s.auth(http.HandlerFunc(s.handleDocs)))

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/docs/manifest", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want 401", unauthorized.Code)
	}

	request := authorizedDocsRequest(t, s, http.MethodPost, "/api/docs/manifest", 1)
	methodNotAllowed := httptest.NewRecorder()
	handler.ServeHTTP(methodNotAllowed, request)
	if methodNotAllowed.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", methodNotAllowed.Code)
	}
}

func TestDocsFilterManifestSearchDocumentsAndAssetsByUser(t *testing.T) {
	s := newDocsTestServer(t, map[int64]struct{}{9: {}})
	handler := s.auth(http.HandlerFunc(s.handleDocs))

	publicManifest := docsRequest(t, handler, s, http.MethodGet, "/api/docs/manifest", 1, "")
	if publicManifest.Code != http.StatusOK {
		t.Fatalf("public manifest status = %d; body=%s", publicManifest.Code, publicManifest.Body.String())
	}
	var publicManifestBody docsstore.Manifest
	decodeDocsBody(t, publicManifest, &publicManifestBody)
	if len(publicManifestBody.Documents) != 1 || publicManifestBody.Documents[0].ID != "public" {
		t.Fatalf("public documents = %+v", publicManifestBody.Documents)
	}

	privilegedManifest := docsRequest(t, handler, s, http.MethodGet, "/api/docs/manifest", 9, "")
	var privilegedManifestBody docsstore.Manifest
	decodeDocsBody(t, privilegedManifest, &privilegedManifestBody)
	if len(privilegedManifestBody.Documents) != 2 {
		t.Fatalf("privileged documents = %+v", privilegedManifestBody.Documents)
	}

	publicSearch := docsRequest(t, handler, s, http.MethodGet, "/api/docs/search-index", 1, "")
	var publicSearchBody docsstore.SearchIndex
	decodeDocsBody(t, publicSearch, &publicSearchBody)
	if len(publicSearchBody.Documents) != 1 || publicSearchBody.Documents[0].ID != "public" {
		t.Fatalf("public search documents = %+v", publicSearchBody.Documents)
	}

	unpinned := docsRequest(t, handler, s, http.MethodGet, "/api/docs/documents/public", 1, "")
	if unpinned.Code != http.StatusBadRequest {
		t.Fatalf("unpinned document status = %d, want 400; body=%s", unpinned.Code, unpinned.Body.String())
	}

	hidden := docsRequest(t, handler, s, http.MethodGet, pinnedDocsTarget("/api/docs/documents/private"), 1, "")
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("hidden document status = %d, want 404; body=%s", hidden.Code, hidden.Body.String())
	}

	document := docsRequest(t, handler, s, http.MethodGet, pinnedDocsTarget("/api/docs/documents/public"), 1, "")
	if document.Code != http.StatusOK {
		t.Fatalf("document status = %d; body=%s", document.Code, document.Body.String())
	}
	if got := document.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Fatalf("document content type = %q", got)
	}
	if got := document.Body.String(); !strings.Contains(got, "Public wallet guidance") {
		t.Fatalf("document body = %q", got)
	}
	if document.Header().Get("ETag") == "" {
		t.Fatal("document ETag is empty")
	}
	if got := document.Header().Get("X-Docs-Commit"); got != docsTestCommit {
		t.Fatalf("document commit = %q, want %q", got, docsTestCommit)
	}

	notModified := docsRequest(t, handler, s, http.MethodGet, pinnedDocsTarget("/api/docs/documents/public"), 1, document.Header().Get("ETag"))
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional document = status %d body %q", notModified.Code, notModified.Body.String())
	}

	asset := docsRequest(t, handler, s, http.MethodGet, pinnedDocsTarget("/api/docs/assets/public.svg"), 1, "")
	if asset.Code != http.StatusOK {
		t.Fatalf("asset status = %d; body=%s", asset.Code, asset.Body.String())
	}
	if got := asset.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("asset content type = %q", got)
	}
	if asset.Header().Get("ETag") == "" || asset.Header().Get("Cache-Control") == "" {
		t.Fatalf("asset cache headers = ETag %q, Cache-Control %q", asset.Header().Get("ETag"), asset.Header().Get("Cache-Control"))
	}
	privateAsset := docsRequest(t, handler, s, http.MethodGet, pinnedDocsTarget("/api/docs/assets/private.svg"), 1, "")
	if privateAsset.Code != http.StatusNotFound {
		t.Fatalf("hidden asset status = %d, want 404", privateAsset.Code)
	}
}

func TestDocsUnavailableDoesNotAffectHealth(t *testing.T) {
	s := &server{
		docs:               docsstore.New(""),
		docsPrivilegedUIDs: map[int64]struct{}{},
		jwtSecret:          []byte("docs-test-secret"),
		corsOrigins:        []string{"*"},
	}
	mux := newHTTPMux(s)

	health := httptest.NewRecorder()
	mux.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || health.Body.String() != `{"ok":true}` {
		t.Fatalf("health = status %d body %q", health.Code, health.Body.String())
	}

	unavailable := httptest.NewRecorder()
	mux.ServeHTTP(unavailable, authorizedDocsRequest(t, s, http.MethodGet, "/api/docs/manifest", 1))
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("docs unavailable status = %d, want 503; body=%s", unavailable.Code, unavailable.Body.String())
	}
	if !strings.Contains(unavailable.Body.String(), "DOCS_UNAVAILABLE") {
		t.Fatalf("docs unavailable body = %s", unavailable.Body.String())
	}
}

func newDocsTestServer(t *testing.T, privileged map[int64]struct{}) *server {
	t.Helper()
	release := writeDocsTestRelease(t)
	return &server{
		docs:               docsstore.New(release),
		docsPrivilegedUIDs: privileged,
		jwtSecret:          []byte("docs-test-secret"),
		corsOrigins:        []string{"*"},
	}
}

func authorizedDocsRequest(t *testing.T, s *server, method, target string, uid int64) *http.Request {
	t.Helper()
	token, err := s.issueToken(uid)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func docsRequest(t *testing.T, handler http.Handler, s *server, method, target string, uid int64, etag string) *httptest.ResponseRecorder {
	t.Helper()
	request := authorizedDocsRequest(t, s, method, target, uid)
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func pinnedDocsTarget(target string) string {
	return target + "?docs_commit=" + docsTestCommit
}

func decodeDocsBody(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

func writeDocsTestRelease(t *testing.T) string {
	t.Helper()
	release := filepath.Join(t.TempDir(), docsTestCommit)
	publicMarkdown := []byte("# Public\n\nPublic wallet guidance.\n\n![Public](../../assets/public.svg)\n")
	privateMarkdown := []byte("# Operations\n\nInternal operations.\n\n![Private](../../assets/private.svg)\n")
	publicAsset := []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	privateAsset := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><title>private</title></svg>`)
	files := map[string][]byte{
		"content/user-manual/public.md": publicMarkdown,
		"content/operations/private.md": privateMarkdown,
		"assets/public.svg":             publicAsset,
		"assets/private.svg":            privateAsset,
	}
	for relative, data := range files {
		filename := filepath.Join(release, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	search := map[string]any{
		"schema_version": 1,
		"docs_commit":    docsTestCommit,
		"documents": []map[string]any{
			{"id": "public", "slug": "public", "title": "Public", "section_id": "user-manual", "order": 1, "visibility": "public", "keywords": []string{"public"}, "text": "Public wallet guidance"},
			{"id": "private", "slug": "private", "title": "Operations", "section_id": "operations", "order": 1, "visibility": "privileged", "keywords": []string{"internal"}, "text": "Internal operations"},
		},
	}
	searchData := docsTestWriteJSON(t, filepath.Join(release, "search-index.json"), search)
	sourceText := "package wallet\n\nfunc AvailableBalance() string { return \"100\" }"
	source := map[string]any{
		"schema_version":    1,
		"deployment_digest": strings.Repeat("d", 64),
		"chunks": []map[string]any{{
			"id": strings.Repeat("1", 64), "repository": "core-service", "commit": docsTestCommit,
			"path": "internal/wallet/balance.go", "start_line": 1, "end_line": 3,
			"language": "go", "symbols": []string{"AvailableBalance"}, "text": sourceText,
			"sha256": docsTestChecksum([]byte(sourceText)),
		}},
	}
	sourceData := docsTestWriteJSON(t, filepath.Join(release, "source-index.json"), source)
	manifest := map[string]any{
		"schema_version":              1,
		"docs_commit":                 docsTestCommit,
		"generated_at":                "2024-01-01T00:00:00.000Z",
		"search_index_sha256":         docsTestChecksum(searchData),
		"source_index_schema_version": 1,
		"source_index_sha256":         docsTestChecksum(sourceData),
		"deployment": map[string]any{
			"schema_version": 1,
			"repositories":   []map[string]string{{"name": "core-service", "commit": docsTestCommit}},
			"images":         []map[string]string{},
		},
		"sections": []map[string]any{
			{"id": "user-manual", "title": "User Manual", "order": 1},
			{"id": "operations", "title": "Operations", "order": 2},
		},
		"documents": []map[string]any{
			{"id": "public", "slug": "public", "title": "Public", "section_id": "user-manual", "order": 1, "visibility": "public", "path": "content/user-manual/public.md", "keywords": []string{"public"}, "sha256": docsTestChecksum(publicMarkdown)},
			{"id": "private", "slug": "private", "title": "Operations", "section_id": "operations", "order": 1, "visibility": "privileged", "path": "content/operations/private.md", "keywords": []string{"internal"}, "sha256": docsTestChecksum(privateMarkdown)},
		},
		"assets": []map[string]string{
			{"path": "assets/public.svg", "sha256": docsTestChecksum(publicAsset)},
			{"path": "assets/private.svg", "sha256": docsTestChecksum(privateAsset)},
		},
	}
	docsTestWriteJSON(t, filepath.Join(release, "manifest.json"), manifest)
	return release
}

func docsTestWriteJSON(t *testing.T, filename string, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

func docsTestChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
