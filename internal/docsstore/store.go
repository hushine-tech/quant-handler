package docsstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

var (
	ErrUnavailable = errors.New("document package unavailable")
	ErrNotFound    = errors.New("document not found")
)

const (
	maxManifestBytes    = 8 << 20
	maxIndexBytes       = 64 << 20
	maxSourceIndexBytes = 256 << 20
	maxDocumentBytes    = 16 << 20
	maxAssetBytes       = 64 << 20
)

var (
	commitPattern  = regexp.MustCompile(`^[a-f0-9]{40}$`)
	sha256Pattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	markdownLinkRE = regexp.MustCompile(`!?\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
)

type storedDocument struct {
	metadata Document
	markdown string
}

type storedAsset struct {
	metadata AssetDescriptor
	data     []byte
}

type snapshot struct {
	root           string
	manifest       Manifest
	search         SearchIndex
	sourceOnce     sync.Once
	source         SourceIndex
	sourceErr      error
	documents      map[string]storedDocument
	assets         map[string]storedAsset
	assetDocuments map[string]map[string]struct{}
}

type Store struct {
	root string
	mu   sync.RWMutex
	byID map[string]*snapshot
}

func New(root string) *Store {
	return &Store{root: strings.TrimSpace(root), byID: make(map[string]*snapshot)}
}

func (s *Store) Manifest(scope AccessScope) (Manifest, error) {
	snapshot, err := s.current()
	if err != nil {
		return Manifest{}, err
	}
	if !validScope(scope) {
		return Manifest{}, ErrNotFound
	}
	visibleDocuments := make([]Document, 0, len(snapshot.manifest.Documents))
	visibleDocumentIDs := make(map[string]struct{})
	visibleSectionIDs := make(map[string]struct{})
	for _, document := range snapshot.manifest.Documents {
		if authorized(scope, document.Visibility) {
			visibleDocuments = append(visibleDocuments, cloneDocument(document))
			visibleDocumentIDs[document.ID] = struct{}{}
			visibleSectionIDs[document.SectionID] = struct{}{}
		}
	}
	visibleSections := make([]Section, 0, len(snapshot.manifest.Sections))
	for _, section := range snapshot.manifest.Sections {
		if _, ok := visibleSectionIDs[section.ID]; ok {
			visibleSections = append(visibleSections, section)
		}
	}
	visibleAssets := make([]AssetDescriptor, 0, len(snapshot.manifest.Assets))
	for _, asset := range snapshot.manifest.Assets {
		for documentID := range snapshot.assetDocuments[asset.Path] {
			if _, ok := visibleDocumentIDs[documentID]; ok {
				visibleAssets = append(visibleAssets, asset)
				break
			}
		}
	}
	manifest := snapshot.manifest
	manifest.Sections = visibleSections
	manifest.Documents = visibleDocuments
	manifest.Assets = visibleAssets
	manifest.Deployment.Repositories = append([]RepositoryFact(nil), snapshot.manifest.Deployment.Repositories...)
	manifest.Deployment.Images = append([]ImageFact(nil), snapshot.manifest.Deployment.Images...)
	return manifest, nil
}

func (s *Store) SearchIndex(scope AccessScope) (SearchIndex, error) {
	snapshot, err := s.current()
	if err != nil {
		return SearchIndex{}, err
	}
	if !validScope(scope) {
		return SearchIndex{}, ErrNotFound
	}
	documents := make([]SearchDocument, 0, len(snapshot.search.Documents))
	for _, document := range snapshot.search.Documents {
		if authorized(scope, document.Visibility) {
			copy := document
			copy.Keywords = append([]string(nil), document.Keywords...)
			documents = append(documents, copy)
		}
	}
	return SearchIndex{
		SchemaVersion: snapshot.search.SchemaVersion,
		DocsCommit:    snapshot.search.DocsCommit,
		Documents:     documents,
	}, nil
}

func (s *Store) RetrievalCorpus() (RetrievalCorpus, error) {
	snapshot, err := s.current()
	if err != nil {
		return RetrievalCorpus{}, err
	}
	documents := make([]RetrievalDocument, 0, len(snapshot.search.Documents))
	for _, document := range snapshot.search.Documents {
		stored, ok := snapshot.documents[document.ID]
		if !ok {
			return RetrievalCorpus{}, unavailable(fmt.Errorf("retrieval document is missing: %s", document.ID))
		}
		documents = append(documents, RetrievalDocument{
			ID: document.ID, Slug: document.Slug, Title: document.Title,
			Visibility: document.Visibility, Keywords: append([]string(nil), document.Keywords...),
			Text: document.Text, Anchors: markdownAnchors(stored.markdown),
		})
	}
	source, err := snapshot.retrievalSource()
	if err != nil {
		return RetrievalCorpus{}, unavailable(err)
	}
	sources := make([]SourceChunk, len(source.Chunks))
	for index, chunk := range source.Chunks {
		sources[index] = chunk
		sources[index].Symbols = append([]string(nil), chunk.Symbols...)
	}
	return RetrievalCorpus{DocsCommit: snapshot.manifest.DocsCommit, Documents: documents, Sources: sources}, nil
}

func (s *snapshot) retrievalSource() (SourceIndex, error) {
	s.sourceOnce.Do(func() {
		if s.manifest.SourceIndexSchemaVersion != 1 || !sha256Pattern.MatchString(s.manifest.SourceIndexSHA256) {
			s.sourceErr = fmt.Errorf("source index is not available")
			return
		}
		data, err := readSafeFile(s.root, "source-index.json", maxSourceIndexBytes)
		if err != nil {
			s.sourceErr = err
			return
		}
		if actual := sha256Hex(data); actual != s.manifest.SourceIndexSHA256 {
			s.sourceErr = fmt.Errorf("source index checksum mismatch")
			return
		}
		if err := decodeStrict(data, &s.source); err != nil {
			s.sourceErr = fmt.Errorf("decode source index: %w", err)
			return
		}
		s.sourceErr = validateSourceIndex(s.source, s.manifest)
	})
	return s.source, s.sourceErr
}

func (s *Store) Document(scope AccessScope, id string) (DocumentContent, error) {
	snapshot, err := s.current()
	if err != nil {
		return DocumentContent{}, err
	}
	return documentFromSnapshot(snapshot, scope, id)
}

func (s *Store) DocumentAt(scope AccessScope, commit, id string) (DocumentContent, error) {
	snapshot, err := s.atCommit(commit)
	if err != nil {
		return DocumentContent{}, err
	}
	return documentFromSnapshot(snapshot, scope, id)
}

func documentFromSnapshot(snapshot *snapshot, scope AccessScope, id string) (DocumentContent, error) {
	document, ok := snapshot.documents[id]
	if !ok || !authorized(scope, document.metadata.Visibility) {
		return DocumentContent{}, ErrNotFound
	}
	return DocumentContent{
		Document: cloneDocument(document.metadata),
		Markdown: document.markdown,
		ETag:     etag(snapshot.manifest.DocsCommit, document.metadata.SHA256),
	}, nil
}

func (s *Store) Asset(scope AccessScope, assetPath string) (Asset, error) {
	snapshot, err := s.current()
	if err != nil {
		return Asset{}, err
	}
	return assetFromSnapshot(snapshot, scope, assetPath)
}

func (s *Store) AssetAt(scope AccessScope, commit, assetPath string) (Asset, error) {
	snapshot, err := s.atCommit(commit)
	if err != nil {
		return Asset{}, err
	}
	return assetFromSnapshot(snapshot, scope, assetPath)
}

func assetFromSnapshot(snapshot *snapshot, scope AccessScope, assetPath string) (Asset, error) {
	normalized, ok := normalizeLookupPath(assetPath, "assets")
	if !ok {
		return Asset{}, ErrNotFound
	}
	asset, ok := snapshot.assets[normalized]
	if !ok {
		return Asset{}, ErrNotFound
	}
	authorizedReference := false
	for documentID := range snapshot.assetDocuments[normalized] {
		document, exists := snapshot.documents[documentID]
		if exists && authorized(scope, document.metadata.Visibility) {
			authorizedReference = true
			break
		}
	}
	if !authorizedReference {
		return Asset{}, ErrNotFound
	}
	contentType := mime.TypeByExtension(filepath.Ext(normalized))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return Asset{
		Path:        normalized,
		ContentType: contentType,
		Data:        append([]byte(nil), asset.data...),
		SHA256:      asset.metadata.SHA256,
		ETag:        etag(snapshot.manifest.DocsCommit, asset.metadata.SHA256),
	}, nil
}

func (s *Store) current() (*snapshot, error) {
	if s.root == "" {
		return nil, ErrUnavailable
	}
	release, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return nil, unavailable(err)
	}
	release, err = filepath.Abs(release)
	if err != nil {
		return nil, unavailable(err)
	}
	info, err := os.Stat(release)
	if err != nil || !info.IsDir() {
		return nil, unavailable(fmt.Errorf("release root is not a directory"))
	}
	return s.loadRelease(release)
}

func (s *Store) atCommit(commit string) (*snapshot, error) {
	if !commitPattern.MatchString(commit) {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	for _, cached := range s.byID {
		if cached.manifest.DocsCommit == commit {
			s.mu.RUnlock()
			return cached, nil
		}
	}
	s.mu.RUnlock()

	currentRelease, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return nil, unavailable(err)
	}
	currentRelease, err = filepath.Abs(currentRelease)
	if err != nil {
		return nil, unavailable(err)
	}
	release := filepath.Join(filepath.Dir(currentRelease), commit)
	return s.loadRelease(release)
}

func (s *Store) loadRelease(release string) (*snapshot, error) {
	s.mu.RLock()
	cached := s.byID[release]
	s.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	loaded, err := loadSnapshot(release)
	if err != nil {
		return nil, unavailable(err)
	}
	s.mu.Lock()
	if existing := s.byID[release]; existing != nil {
		loaded = existing
	} else {
		s.byID[release] = loaded
	}
	s.mu.Unlock()
	return loaded, nil
}

func loadSnapshot(root string) (*snapshot, error) {
	manifestData, err := readSafeFile(root, "manifest.json", maxManifestBytes)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := decodeStrict(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := validateManifest(root, manifest); err != nil {
		return nil, err
	}
	indexData, err := readSafeFile(root, "search-index.json", maxIndexBytes)
	if err != nil {
		return nil, err
	}
	if actual := sha256Hex(indexData); actual != manifest.SearchIndexSHA256 {
		return nil, fmt.Errorf("search index checksum mismatch")
	}
	var index SearchIndex
	if err := decodeStrict(indexData, &index); err != nil {
		return nil, fmt.Errorf("decode search index: %w", err)
	}
	if index.SchemaVersion != 1 || index.DocsCommit != manifest.DocsCommit {
		return nil, fmt.Errorf("search index identity mismatch")
	}
	documents := make(map[string]storedDocument, len(manifest.Documents))
	metadataByID := make(map[string]Document, len(manifest.Documents))
	for _, document := range manifest.Documents {
		data, err := readSafeFile(root, document.Path, maxDocumentBytes)
		if err != nil {
			return nil, err
		}
		if sha256Hex(data) != document.SHA256 {
			return nil, fmt.Errorf("document checksum mismatch: %s", document.ID)
		}
		documents[document.ID] = storedDocument{metadata: document, markdown: string(data)}
		metadataByID[document.ID] = document
	}
	if len(index.Documents) != len(manifest.Documents) {
		return nil, fmt.Errorf("search index document count mismatch")
	}
	seenSearch := make(map[string]struct{}, len(index.Documents))
	for _, searchDocument := range index.Documents {
		metadata, ok := metadataByID[searchDocument.ID]
		if !ok || searchDocument.Slug != metadata.Slug || searchDocument.Title != metadata.Title ||
			searchDocument.SectionID != metadata.SectionID || searchDocument.Order != metadata.Order ||
			searchDocument.Visibility != metadata.Visibility {
			return nil, fmt.Errorf("search index metadata mismatch: %s", searchDocument.ID)
		}
		if _, duplicate := seenSearch[searchDocument.ID]; duplicate {
			return nil, fmt.Errorf("duplicate search document: %s", searchDocument.ID)
		}
		seenSearch[searchDocument.ID] = struct{}{}
	}

	assets := make(map[string]storedAsset, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		data, err := readSafeFile(root, asset.Path, maxAssetBytes)
		if err != nil {
			return nil, err
		}
		if sha256Hex(data) != asset.SHA256 {
			return nil, fmt.Errorf("asset checksum mismatch: %s", asset.Path)
		}
		assets[asset.Path] = storedAsset{metadata: asset, data: data}
	}
	assetDocuments := make(map[string]map[string]struct{}, len(assets))
	for documentID, document := range documents {
		for _, assetPath := range referencedAssets(document.metadata.Path, document.markdown) {
			if _, ok := assets[assetPath]; !ok {
				return nil, fmt.Errorf("document references unregistered asset: %s", assetPath)
			}
			if assetDocuments[assetPath] == nil {
				assetDocuments[assetPath] = make(map[string]struct{})
			}
			assetDocuments[assetPath][documentID] = struct{}{}
		}
	}
	return &snapshot{
		root:           root,
		manifest:       manifest,
		search:         index,
		documents:      documents,
		assets:         assets,
		assetDocuments: assetDocuments,
	}, nil
}

func validateManifest(root string, manifest Manifest) error {
	if manifest.SchemaVersion != 1 || !commitPattern.MatchString(manifest.DocsCommit) {
		return fmt.Errorf("invalid manifest identity")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.GeneratedAt); err != nil {
		return fmt.Errorf("invalid manifest generated_at")
	}
	if manifest.Deployment.SchemaVersion != 1 || !sha256Pattern.MatchString(manifest.SearchIndexSHA256) {
		return fmt.Errorf("invalid manifest deployment or search checksum")
	}
	if (manifest.SourceIndexSchemaVersion == 0) != (manifest.SourceIndexSHA256 == "") ||
		(manifest.SourceIndexSchemaVersion != 0 && (manifest.SourceIndexSchemaVersion != 1 || !sha256Pattern.MatchString(manifest.SourceIndexSHA256))) {
		return fmt.Errorf("invalid manifest source index metadata")
	}
	if filepath.Base(root) != manifest.DocsCommit {
		return fmt.Errorf("release directory does not match docs commit")
	}
	sectionIDs := make(map[string]struct{}, len(manifest.Sections))
	for _, section := range manifest.Sections {
		if section.ID == "" || section.Title == "" || section.Order < 0 {
			return fmt.Errorf("invalid section")
		}
		if _, duplicate := sectionIDs[section.ID]; duplicate {
			return fmt.Errorf("duplicate section: %s", section.ID)
		}
		sectionIDs[section.ID] = struct{}{}
	}
	documentIDs := make(map[string]struct{}, len(manifest.Documents))
	slugs := make(map[string]struct{}, len(manifest.Documents))
	paths := make(map[string]struct{}, len(manifest.Documents))
	for _, document := range manifest.Documents {
		if document.ID == "" || document.Slug == "" || document.Title == "" || document.Order < 0 ||
			!sha256Pattern.MatchString(document.SHA256) || !validVisibility(document.Visibility) {
			return fmt.Errorf("invalid document: %s", document.ID)
		}
		if _, ok := sectionIDs[document.SectionID]; !ok {
			return fmt.Errorf("document references unknown section: %s", document.ID)
		}
		if _, ok := normalizeLookupPath(document.Path, "content"); !ok {
			return fmt.Errorf("invalid document path: %s", document.Path)
		}
		if _, duplicate := documentIDs[document.ID]; duplicate {
			return fmt.Errorf("duplicate document id: %s", document.ID)
		}
		documentIDs[document.ID] = struct{}{}
		if _, duplicate := slugs[document.Slug]; duplicate {
			return fmt.Errorf("duplicate document slug: %s", document.Slug)
		}
		slugs[document.Slug] = struct{}{}
		if _, duplicate := paths[document.Path]; duplicate {
			return fmt.Errorf("duplicate document path: %s", document.Path)
		}
		paths[document.Path] = struct{}{}
	}
	assetPaths := make(map[string]struct{}, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		if _, ok := normalizeLookupPath(asset.Path, "assets"); !ok || !sha256Pattern.MatchString(asset.SHA256) {
			return fmt.Errorf("invalid asset: %s", asset.Path)
		}
		if _, duplicate := assetPaths[asset.Path]; duplicate {
			return fmt.Errorf("duplicate asset: %s", asset.Path)
		}
		assetPaths[asset.Path] = struct{}{}
	}
	return nil
}

func validateSourceIndex(index SourceIndex, manifest Manifest) error {
	if index.SchemaVersion != manifest.SourceIndexSchemaVersion || !sha256Pattern.MatchString(index.DeploymentDigest) {
		return fmt.Errorf("source index identity mismatch")
	}
	repositories := make(map[string]string, len(manifest.Deployment.Repositories))
	for _, repository := range manifest.Deployment.Repositories {
		repositories[repository.Name] = repository.Commit
	}
	seen := make(map[string]struct{}, len(index.Chunks))
	previous := ""
	for _, chunk := range index.Chunks {
		orderKey := fmt.Sprintf("%s\x00%s\x00%010d", chunk.Repository, chunk.Path, chunk.StartLine)
		if previous != "" && orderKey < previous {
			return fmt.Errorf("source index chunks are not sorted")
		}
		previous = orderKey
		if !sha256Pattern.MatchString(chunk.ID) || !sha256Pattern.MatchString(chunk.SHA256) ||
			repositories[chunk.Repository] != chunk.Commit || !validSourcePath(chunk.Path) ||
			chunk.StartLine < 1 || chunk.EndLine < chunk.StartLine || chunk.Text == "" ||
			strings.Count(chunk.Text, "\n")+1 != chunk.EndLine-chunk.StartLine+1 ||
			sha256Hex([]byte(chunk.Text)) != chunk.SHA256 || !validSourceLanguage(chunk.Language) {
			return fmt.Errorf("invalid source chunk: %s", chunk.ID)
		}
		if _, duplicate := seen[chunk.ID]; duplicate {
			return fmt.Errorf("duplicate source chunk: %s", chunk.ID)
		}
		seen[chunk.ID] = struct{}{}
		for _, symbol := range chunk.Symbols {
			if strings.TrimSpace(symbol) == "" {
				return fmt.Errorf("invalid source symbol: %s", chunk.ID)
			}
		}
	}
	return nil
}

func validSourcePath(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !path.IsAbs(value) && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func validSourceLanguage(value string) bool {
	switch value {
	case "go", "proto", "python", "typescript", "sql", "yaml", "markdown":
		return true
	default:
		return false
	}
}

func markdownAnchors(markdown string) []string {
	seen := make(map[string]int)
	anchors := make([]string, 0)
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
			continue
		}
		title := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(trimmed[level+1:]), "#"))
		var builder strings.Builder
		for _, character := range strings.ToLower(title) {
			switch {
			case unicode.IsLetter(character), unicode.IsNumber(character), unicode.IsMark(character), character == '_', character == '-':
				builder.WriteRune(character)
			case unicode.IsSpace(character):
				builder.WriteRune('-')
			}
		}
		base := builder.String()
		anchor := base
		if count := seen[base]; count > 0 {
			anchor = fmt.Sprintf("%s-%d", base, count)
		}
		seen[base]++
		anchors = append(anchors, anchor)
	}
	return anchors
}

func readSafeFile(root, relative string, limit int64) ([]byte, error) {
	normalized := path.Clean(strings.ReplaceAll(relative, "\\", "/"))
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || path.IsAbs(normalized) {
		return nil, fmt.Errorf("invalid package path")
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(rootResolved, filepath.FromSlash(normalized))
	targetResolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, err
	}
	relativeToRoot, err := filepath.Rel(rootResolved, targetResolved)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("package path follows symlink outside release")
	}
	info, err := os.Stat(targetResolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("package entry is not a regular file")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("package entry exceeds size limit")
	}
	return os.ReadFile(targetResolved)
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func referencedAssets(documentPath, markdown string) []string {
	seen := make(map[string]struct{})
	for _, match := range markdownLinkRE.FindAllStringSubmatch(markdown, -1) {
		target := match[1]
		if strings.HasPrefix(target, "/docs/") || strings.HasPrefix(target, "#") ||
			strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
			strings.HasPrefix(target, "mailto:") {
			continue
		}
		target = strings.SplitN(strings.SplitN(target, "#", 2)[0], "?", 2)[0]
		decoded, err := url.PathUnescape(target)
		if err != nil {
			continue
		}
		normalized := path.Clean(path.Join(path.Dir(documentPath), decoded))
		if strings.HasPrefix(normalized, "assets/") {
			seen[normalized] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for assetPath := range seen {
		result = append(result, assetPath)
	}
	return result
}

func cloneDocument(document Document) Document {
	document.Keywords = append([]string(nil), document.Keywords...)
	return document
}

func normalizeLookupPath(value, prefix string) (string, bool) {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) {
		return "", false
	}
	normalized := path.Clean(value)
	if normalized != value || !strings.HasPrefix(normalized, prefix+"/") {
		return "", false
	}
	return normalized, true
}

func validVisibility(value string) bool {
	return value == string(ScopePublic) || value == string(ScopePrivileged)
}

func validScope(scope AccessScope) bool {
	return scope == ScopePublic || scope == ScopePrivileged
}

func authorized(scope AccessScope, visibility string) bool {
	return validScope(scope) && (visibility == string(ScopePublic) || scope == ScopePrivileged)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func etag(commit, checksum string) string {
	return fmt.Sprintf("\"docs-%s-%s\"", commit, checksum)
}

func unavailable(err error) error {
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}
