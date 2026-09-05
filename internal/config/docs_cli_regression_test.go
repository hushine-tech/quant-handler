package config

import "testing"

func TestDocsChatEnablesUsingLocalCodexLoginWithoutAPIKey(t *testing.T) {
	t.Setenv("DOCS_CHAT_ENABLED", "true")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_DOCS_MODEL", "")
	if err := Default().ApplyEnvOverrides(); err != nil {
		t.Fatalf("local Codex login must not require API credentials: %v", err)
	}
}
