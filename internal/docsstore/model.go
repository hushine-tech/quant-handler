package docsstore

type AccessScope string

const (
	ScopePublic     AccessScope = "public"
	ScopePrivileged AccessScope = "privileged"
)

type RepositoryFact struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type ImageFact struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type Deployment struct {
	SchemaVersion int              `json:"schema_version"`
	Repositories  []RepositoryFact `json:"repositories"`
	Images        []ImageFact      `json:"images"`
}

type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Order int    `json:"order"`
}

type Document struct {
	ID         string   `json:"id"`
	Slug       string   `json:"slug"`
	Title      string   `json:"title"`
	SectionID  string   `json:"section_id"`
	Order      int      `json:"order"`
	Visibility string   `json:"visibility"`
	Path       string   `json:"path"`
	Keywords   []string `json:"keywords"`
	SHA256     string   `json:"sha256"`
}

type AssetDescriptor struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	SchemaVersion     int               `json:"schema_version"`
	DocsCommit        string            `json:"docs_commit"`
	GeneratedAt       string            `json:"generated_at"`
	Deployment        Deployment        `json:"deployment"`
	Sections          []Section         `json:"sections"`
	Documents         []Document        `json:"documents"`
	Assets            []AssetDescriptor `json:"assets"`
	SearchIndexSHA256 string            `json:"search_index_sha256"`
}

type SearchDocument struct {
	ID         string   `json:"id"`
	Slug       string   `json:"slug"`
	Title      string   `json:"title"`
	SectionID  string   `json:"section_id"`
	Order      int      `json:"order"`
	Visibility string   `json:"visibility"`
	Keywords   []string `json:"keywords"`
	Text       string   `json:"text"`
}

type SearchIndex struct {
	SchemaVersion int              `json:"schema_version"`
	DocsCommit    string           `json:"docs_commit"`
	Documents     []SearchDocument `json:"documents"`
}

type DocumentContent struct {
	Document Document `json:"document"`
	Markdown string   `json:"markdown"`
	ETag     string   `json:"-"`
}

type Asset struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"-"`
	SHA256      string `json:"sha256"`
	ETag        string `json:"-"`
}
