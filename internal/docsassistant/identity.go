package docsassistant

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"

	"github.com/hushine-tech/quant-handler/internal/docsstore"
)

const conversationIdentityPrefix = "docs-conversation:v1:"

func UserHash(secret []byte, userID int64) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(conversationIdentityPrefix + strconv.FormatInt(userID, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func ConversationMetadata(secret []byte, userID int64, docsCommit string, scope docsstore.AccessScope) map[string]string {
	return map[string]string{
		"hushine_uid_hash": UserHash(secret, userID),
		"docs_commit":      docsCommit,
		"access_scope":     string(scope),
	}
}

func ValidateConversationMetadata(metadata map[string]string, secret []byte, userID int64, docsCommit string, scope docsstore.AccessScope) error {
	if len(metadata) != 3 || metadata["docs_commit"] != docsCommit || metadata["access_scope"] != string(scope) {
		return ErrConversationStale
	}
	want := UserHash(secret, userID)
	got := metadata["hushine_uid_hash"]
	if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return ErrConversationStale
	}
	return nil
}
