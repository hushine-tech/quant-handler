package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
	"github.com/hushine-tech/quant-handler/internal/logger"
)

const docsCacheControl = "private, no-cache"

func (s *server) docsScopeForUser(uid int64) docsstore.AccessScope {
	if _, ok := s.docsPrivilegedUIDs[uid]; ok {
		return docsstore.ScopePrivileged
	}
	return docsstore.ScopePublic
}

func (s *server) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}
	if s.docs == nil {
		writeDocsStoreError(w, r, docsstore.ErrUnavailable)
		return
	}

	scope := s.docsScopeForUser(uid)
	remainder := strings.TrimPrefix(r.URL.Path, "/api/docs/")
	switch {
	case remainder == "manifest":
		manifest, err := s.docs.Manifest(scope)
		if err != nil {
			writeDocsStoreError(w, r, err)
			return
		}
		writeDocsJSON(w, r, manifest.DocsCommit, manifest)
	case remainder == "search-index":
		index, err := s.docs.SearchIndex(scope)
		if err != nil {
			writeDocsStoreError(w, r, err)
			return
		}
		writeDocsJSON(w, r, index.DocsCommit, index)
	case strings.HasPrefix(remainder, "documents/"):
		documentID, ok := docsDocumentID(strings.TrimPrefix(remainder, "documents/"))
		if !ok {
			writeErr(w, http.StatusNotFound, "document not found")
			return
		}
		document, err := s.docs.Document(scope, documentID)
		if err != nil {
			writeDocsStoreError(w, r, err)
			return
		}
		if writeDocsNotModified(w, r, document.ETag) {
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(document.Markdown))
	case strings.HasPrefix(remainder, "assets/"):
		assetPath, ok := docsAssetPath(strings.TrimPrefix(remainder, "assets/"))
		if !ok {
			writeErr(w, http.StatusNotFound, "asset not found")
			return
		}
		asset, err := s.docs.Asset(scope, "assets/"+assetPath)
		if err != nil {
			writeDocsStoreError(w, r, err)
			return
		}
		if writeDocsNotModified(w, r, asset.ETag) {
			return
		}
		w.Header().Set("Content-Type", asset.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(asset.Data)
	default:
		writeErr(w, http.StatusNotFound, "document endpoint not found")
	}
}

func docsDocumentID(value string) (string, bool) {
	return value, value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\")
}

func docsAssetPath(value string) (string, bool) {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return "", false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	return value, true
}

func writeDocsJSON(w http.ResponseWriter, r *http.Request, commit string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "encode document response")
		return
	}
	data = append(data, '\n')
	sum := sha256.Sum256(data)
	etag := fmt.Sprintf("\"docs-%s-%s\"", commit, hex.EncodeToString(sum[:]))
	if writeDocsNotModified(w, r, etag) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writeDocsNotModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", docsCacheControl)
	if docsETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

func docsETagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

func writeDocsStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, docsstore.ErrNotFound):
		writeErr(w, http.StatusNotFound, "document not found")
	case errors.Is(err, docsstore.ErrUnavailable):
		logger.Info(r.Context(), "docs", fmt.Sprintf("document package unavailable: %v", err))
		writeErr(w, http.StatusServiceUnavailable, "DOCS_UNAVAILABLE")
	default:
		logger.Info(r.Context(), "docs", fmt.Sprintf("document request failed: %v", err))
		writeErr(w, http.StatusInternalServerError, "document request failed")
	}
}
