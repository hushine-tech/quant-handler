package docsassistant

import (
	"errors"
	"strings"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
)

func TestConversationIdentityBindsSecretUserCommitAndScope(t *testing.T) {
	const want = "6a5025b772d81374c58377a9fb19703041b59659b01978f8ef52737e4745b7b3"
	if got := UserHash([]byte("secret"), 42); got != want {
		t.Fatalf("UserHash = %q, want hand-checked HMAC %q", got, want)
	}
	if strings.Contains(UserHash([]byte("secret"), 42), "42") {
		t.Fatal("user hash contains the raw user ID")
	}
	if UserHash([]byte("secret"), 42) == UserHash([]byte("secret"), 43) {
		t.Fatal("two users share a user hash")
	}
	if UserHash([]byte("secret"), 42) == UserHash([]byte("different"), 42) {
		t.Fatal("two signing secrets share a user hash")
	}

	metadata := ConversationMetadata(
		[]byte("secret"), 42, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", docsstore.ScopePublic,
	)
	wantMetadata := map[string]string{
		"hushine_uid_hash": want,
		"docs_commit":      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"access_scope":     "public",
	}
	if len(metadata) != len(wantMetadata) {
		t.Fatalf("metadata = %#v", metadata)
	}
	for key, value := range wantMetadata {
		if metadata[key] != value {
			t.Fatalf("metadata[%q] = %q, want %q", key, metadata[key], value)
		}
	}
	if err := ValidateConversationMetadata(
		metadata, []byte("secret"), 42,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", docsstore.ScopePublic,
	); err != nil {
		t.Fatal(err)
	}
}

func TestConversationIdentityRejectsEveryStaleBinding(t *testing.T) {
	metadata := ConversationMetadata(
		[]byte("secret"), 42, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", docsstore.ScopePublic,
	)
	checks := []struct {
		name   string
		meta   map[string]string
		secret []byte
		uid    int64
		commit string
		scope  docsstore.AccessScope
	}{
		{name: "other-secret", meta: metadata, secret: []byte("other"), uid: 42, commit: metadata["docs_commit"], scope: docsstore.ScopePublic},
		{name: "other-user", meta: metadata, secret: []byte("secret"), uid: 43, commit: metadata["docs_commit"], scope: docsstore.ScopePublic},
		{name: "other-commit", meta: metadata, secret: []byte("secret"), uid: 42, commit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", scope: docsstore.ScopePublic},
		{name: "upgraded-scope", meta: metadata, secret: []byte("secret"), uid: 42, commit: metadata["docs_commit"], scope: docsstore.ScopePrivileged},
		{name: "missing-field", meta: map[string]string{"docs_commit": metadata["docs_commit"], "access_scope": "public"}, secret: []byte("secret"), uid: 42, commit: metadata["docs_commit"], scope: docsstore.ScopePublic},
		{name: "extra-field", meta: map[string]string{"hushine_uid_hash": metadata["hushine_uid_hash"], "docs_commit": metadata["docs_commit"], "access_scope": "public", "unexpected": "value"}, secret: []byte("secret"), uid: 42, commit: metadata["docs_commit"], scope: docsstore.ScopePublic},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := ValidateConversationMetadata(check.meta, check.secret, check.uid, check.commit, check.scope); !errors.Is(err, ErrConversationStale) {
				t.Fatalf("error = %T %v", err, err)
			}
		})
	}
}
