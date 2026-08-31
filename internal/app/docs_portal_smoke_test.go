package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
)

func TestDocsPortalSmokeFromRelease(t *testing.T) {
	release := os.Getenv("DOCS_SMOKE_RELEASE")
	if release == "" {
		t.Skip("DOCS_SMOKE_RELEASE is required for the cross-repository smoke")
	}
	s := &server{
		docs:               docsstore.New(release),
		docsPrivilegedUIDs: map[int64]struct{}{9: {}},
		jwtSecret:          []byte("docs-portal-smoke-secret"),
		corsOrigins:        []string{"*"},
	}
	mux := newHTTPMux(s)

	health := httptest.NewRecorder()
	mux.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d; body=%s", health.Code, health.Body.String())
	}

	publicManifestResponse := docsRequest(t, mux, s, http.MethodGet, "/api/docs/manifest", 1, "")
	var publicManifest docsstore.Manifest
	decodeDocsBody(t, publicManifestResponse, &publicManifest)
	privilegedManifestResponse := docsRequest(t, mux, s, http.MethodGet, "/api/docs/manifest", 9, "")
	var privilegedManifest docsstore.Manifest
	decodeDocsBody(t, privilegedManifestResponse, &privilegedManifest)
	if len(publicManifest.Documents) == 0 || len(privilegedManifest.Documents) <= len(publicManifest.Documents) {
		t.Fatalf("document counts public=%d privileged=%d", len(publicManifest.Documents), len(privilegedManifest.Documents))
	}
	for _, document := range publicManifest.Documents {
		if document.Visibility != string(docsstore.ScopePublic) {
			t.Fatalf("public manifest leaked %s document %s", document.Visibility, document.ID)
		}
	}

	publicSearchResponse := docsRequest(t, mux, s, http.MethodGet, "/api/docs/search-index", 1, "")
	var publicSearch docsstore.SearchIndex
	decodeDocsBody(t, publicSearchResponse, &publicSearch)
	privilegedSearchResponse := docsRequest(t, mux, s, http.MethodGet, "/api/docs/search-index", 9, "")
	var privilegedSearch docsstore.SearchIndex
	decodeDocsBody(t, privilegedSearchResponse, &privilegedSearch)
	if len(publicSearch.Documents) != len(publicManifest.Documents) || len(privilegedSearch.Documents) != len(privilegedManifest.Documents) {
		t.Fatalf("search/manifest counts public=%d/%d privileged=%d/%d",
			len(publicSearch.Documents), len(publicManifest.Documents), len(privilegedSearch.Documents), len(privilegedManifest.Documents))
	}

	publicDocument := publicManifest.Documents[0]
	publicContent := docsRequest(t, mux, s, http.MethodGet, "/api/docs/documents/"+publicDocument.ID+"?docs_commit="+publicManifest.DocsCommit, 1, "")
	if publicContent.Code != http.StatusOK || publicContent.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || publicContent.Body.Len() == 0 {
		t.Fatalf("public content = status %d type %q bytes %d", publicContent.Code, publicContent.Header().Get("Content-Type"), publicContent.Body.Len())
	}
	publicNotModified := docsRequest(t, mux, s, http.MethodGet, "/api/docs/documents/"+publicDocument.ID+"?docs_commit="+publicManifest.DocsCommit, 1, publicContent.Header().Get("ETag"))
	if publicNotModified.Code != http.StatusNotModified || publicNotModified.Body.Len() != 0 {
		t.Fatalf("conditional content = status %d bytes %d", publicNotModified.Code, publicNotModified.Body.Len())
	}

	var privilegedOnly docsstore.Document
	for _, document := range privilegedManifest.Documents {
		if document.Visibility == string(docsstore.ScopePrivileged) {
			privilegedOnly = document
			break
		}
	}
	if privilegedOnly.ID == "" {
		t.Fatal("release contains no privileged document")
	}
	hidden := docsRequest(t, mux, s, http.MethodGet, "/api/docs/documents/"+privilegedOnly.ID+"?docs_commit="+publicManifest.DocsCommit, 1, "")
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("public hidden-document status = %d, want 404", hidden.Code)
	}
	privilegedContent := docsRequest(t, mux, s, http.MethodGet, "/api/docs/documents/"+privilegedOnly.ID+"?docs_commit="+privilegedManifest.DocsCommit, 9, "")
	if privilegedContent.Code != http.StatusOK || privilegedContent.Body.Len() == 0 {
		t.Fatalf("privileged content = status %d bytes %d", privilegedContent.Code, privilegedContent.Body.Len())
	}

	t.Log("docs portal release smoke passed")
}
