package docsstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeJSON(t *testing.T, file string, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

func writeRelease(t *testing.T, root, commit, publicText string) string {
	t.Helper()
	release := filepath.Join(root, "releases", commit)
	publicMarkdown := []byte("# Public\n\n" + publicText + "\n\n![Public](../../assets/public.svg)\n")
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
		file := filepath.Join(release, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	search := map[string]any{
		"schema_version": 1,
		"docs_commit":    commit,
		"documents": []map[string]any{
			{"id": "public", "slug": "public", "title": "Public", "section_id": "user-manual", "order": 1, "visibility": "public", "keywords": []string{"public"}, "text": publicText},
			{"id": "private", "slug": "private", "title": "Operations", "section_id": "operations", "order": 1, "visibility": "privileged", "keywords": []string{"internal"}, "text": "Internal operations"},
		},
	}
	searchData := writeJSON(t, filepath.Join(release, "search-index.json"), search)
	manifest := map[string]any{
		"schema_version":      1,
		"docs_commit":         commit,
		"generated_at":        "2024-01-01T00:00:00.000Z",
		"search_index_sha256": checksum(searchData),
		"deployment": map[string]any{
			"schema_version": 1,
			"repositories":   []map[string]string{{"name": "core-service", "commit": commitA}},
			"images":         []map[string]string{},
		},
		"sections": []map[string]any{
			{"id": "user-manual", "title": "User Manual", "order": 1},
			{"id": "operations", "title": "Operations", "order": 2},
		},
		"documents": []map[string]any{
			{"id": "public", "slug": "public", "title": "Public", "section_id": "user-manual", "order": 1, "visibility": "public", "path": "content/user-manual/public.md", "keywords": []string{"public"}, "sha256": checksum(publicMarkdown)},
			{"id": "private", "slug": "private", "title": "Operations", "section_id": "operations", "order": 1, "visibility": "privileged", "path": "content/operations/private.md", "keywords": []string{"internal"}, "sha256": checksum(privateMarkdown)},
		},
		"assets": []map[string]string{
			{"path": "assets/public.svg", "sha256": checksum(publicAsset)},
			{"path": "assets/private.svg", "sha256": checksum(privateAsset)},
		},
	}
	writeJSON(t, filepath.Join(release, "manifest.json"), manifest)
	return release
}

func switchCurrent(t *testing.T, root, target string) string {
	t.Helper()
	current := filepath.Join(root, "current")
	next := filepath.Join(root, "current.next")
	_ = os.Remove(next)
	if err := os.Symlink(target, next); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, current); err != nil {
		t.Fatal(err)
	}
	return current
}

func TestStoreFiltersEverySurfaceByAccessScope(t *testing.T) {
	root := t.TempDir()
	writeRelease(t, root, commitA, "Public wallet guidance.")
	store := New(switchCurrent(t, root, filepath.Join("releases", commitA)))

	publicManifest, err := store.Manifest(ScopePublic)
	if err != nil {
		t.Fatal(err)
	}
	if len(publicManifest.Documents) != 1 || publicManifest.Documents[0].ID != "public" {
		t.Fatalf("public documents = %+v", publicManifest.Documents)
	}
	if len(publicManifest.Sections) != 1 || publicManifest.Sections[0].ID != "user-manual" {
		t.Fatalf("public sections = %+v", publicManifest.Sections)
	}
	if len(publicManifest.Assets) != 1 || publicManifest.Assets[0].Path != "assets/public.svg" {
		t.Fatalf("public assets = %+v", publicManifest.Assets)
	}

	privilegedManifest, err := store.Manifest(ScopePrivileged)
	if err != nil {
		t.Fatal(err)
	}
	if len(privilegedManifest.Documents) != 2 || len(privilegedManifest.Assets) != 2 {
		t.Fatalf("privileged manifest = %+v", privilegedManifest)
	}

	index, err := store.SearchIndex(ScopePublic)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Documents) != 1 || index.Documents[0].ID != "public" {
		t.Fatalf("public search index = %+v", index.Documents)
	}

	document, err := store.Document(ScopePublic, "public")
	if err != nil {
		t.Fatal(err)
	}
	if document.Document.ID != "public" || document.Markdown == "" || document.ETag == "" {
		t.Fatalf("public document = %+v", document)
	}
	if _, err := store.Document(ScopePublic, "private"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden document error = %v, want ErrNotFound", err)
	}

	asset, err := store.Asset(ScopePublic, "assets/public.svg")
	if err != nil {
		t.Fatal(err)
	}
	if asset.ContentType != "image/svg+xml" || len(asset.Data) == 0 || asset.ETag == "" {
		t.Fatalf("public asset = %+v", asset)
	}
	if _, err := store.Asset(ScopePublic, "assets/private.svg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden asset error = %v, want ErrNotFound", err)
	}
	if _, err := store.Asset(ScopePrivileged, "../manifest.json"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("traversal error = %v, want ErrNotFound", err)
	}
}

func TestStoreRejectsChecksumMismatchBeforeServingAnything(t *testing.T) {
	root := t.TempDir()
	release := writeRelease(t, root, commitA, "Original.")
	if err := os.WriteFile(filepath.Join(release, "content/user-manual/public.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := New(switchCurrent(t, root, filepath.Join("releases", commitA)))
	if _, err := store.Manifest(ScopePrivileged); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Manifest error = %v, want ErrUnavailable", err)
	}
}

func TestStoreRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	release := writeRelease(t, root, commitA, "Original.")
	outside := filepath.Join(t.TempDir(), "outside.svg")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(release, "assets/public.svg")
	if err := os.Remove(asset); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, asset); err != nil {
		t.Fatal(err)
	}
	store := New(switchCurrent(t, root, filepath.Join("releases", commitA)))
	if _, err := store.Manifest(ScopePrivileged); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Manifest error = %v, want ErrUnavailable", err)
	}
}

func TestStoreLoadsOnlyCompleteAtomicCurrentTargets(t *testing.T) {
	root := t.TempDir()
	writeRelease(t, root, commitA, "Version A.")
	writeRelease(t, root, commitB, "Version B.")
	current := switchCurrent(t, root, filepath.Join("releases", commitA))
	store := New(current)

	first, err := store.Document(ScopePublic, "public")
	if err != nil || first.Markdown != "# Public\n\nVersion A.\n\n![Public](../../assets/public.svg)\n" {
		t.Fatalf("first document = %+v, %v", first, err)
	}
	switchCurrent(t, root, filepath.Join("releases", commitB))
	second, err := store.Document(ScopePublic, "public")
	if err != nil || second.Markdown != "# Public\n\nVersion B.\n\n![Public](../../assets/public.svg)\n" {
		t.Fatalf("second document = %+v, %v", second, err)
	}
	switchCurrent(t, root, "releases/missing")
	if _, err := store.Manifest(ScopePublic); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("broken target error = %v, want ErrUnavailable", err)
	}
	switchCurrent(t, root, filepath.Join("releases", commitB))
	again, err := store.Document(ScopePublic, "public")
	if err != nil || again.Markdown != second.Markdown {
		t.Fatalf("cached complete target = %+v, %v", again, err)
	}
}

func TestStoreServesContentPinnedToTheManifestCommitAfterCurrentSwitch(t *testing.T) {
	root := t.TempDir()
	writeRelease(t, root, commitA, "Version A.")
	writeRelease(t, root, commitB, "Version B.")
	current := switchCurrent(t, root, filepath.Join("releases", commitA))
	store := New(current)

	manifest, err := store.Manifest(ScopePublic)
	if err != nil || manifest.DocsCommit != commitA {
		t.Fatalf("manifest = %+v, %v", manifest, err)
	}
	switchCurrent(t, root, filepath.Join("releases", commitB))

	pinned, err := store.DocumentAt(ScopePublic, commitA, "public")
	if err != nil || !strings.Contains(pinned.Markdown, "Version A.") {
		t.Fatalf("pinned document = %+v, %v", pinned, err)
	}
	currentDocument, err := store.Document(ScopePublic, "public")
	if err != nil || !strings.Contains(currentDocument.Markdown, "Version B.") {
		t.Fatalf("current document = %+v, %v", currentDocument, err)
	}
	if _, err := store.AssetAt(ScopePublic, commitA, "assets/public.svg"); err != nil {
		t.Fatalf("pinned asset: %v", err)
	}
	if _, err := store.DocumentAt(ScopePublic, "../../etc/passwd", "public"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid commit error = %v, want ErrNotFound", err)
	}
}

func TestStoreMissingPackageIsolatedAsUnavailable(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "missing"))
	if _, err := store.Manifest(ScopePublic); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Manifest error = %v, want ErrUnavailable", err)
	}
}
