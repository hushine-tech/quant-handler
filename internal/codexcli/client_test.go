package codexcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLIUsesChatGPTLoginPersistsThreadAndResumesExactConversation(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	binary := fakeExecutable(t, `if [ "$1" = login ]; then echo 'Logged in using ChatGPT' >&2; exit 0; fi
printf '%s\n' "$@" >> '`+log+`'
test -z "$OPENAI_API_KEY" || exit 9
test -z "$AUTH_JWT_SECRET" || exit 10
cat >/dev/null
echo '{"type":"thread.started","thread_id":"019f0000-0000-7000-8000-000000000001"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"{\"output_text\":\"answer\",\"calls\":[]}"}}'
echo '{"type":"turn.completed"}'
`)
	t.Setenv("OPENAI_API_KEY", "must-not-inherit")
	t.Setenv("AUTH_JWT_SECRET", "must-not-inherit")
	options := Options{Binary: binary, StateDir: filepath.Join(dir, "state"), Timeout: time.Second}
	client, err := NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := client.CreateConversation(context.Background(), map[string]string{"owner": "alice"})
	if err != nil {
		t.Fatal(err)
	}
	request := ResponseRequest{ConversationID: conversation.ID, Instructions: "read only", Input: []InputItem{{Type: "message", Role: "user", Content: []Content{{Type: "input_text", Text: "hello $(touch never)"}}}}}
	if _, err := client.CreateResponse(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := client.SaveAnswer(context.Background(), conversation.ID, "hello", `{"answer":"answer","citations":[]}`); err != nil {
		t.Fatal(err)
	}
	// Recreate the client, as after a quant-handler restart; no --last lookup is allowed.
	restarted, err := NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.RetrieveConversation(context.Background(), conversation.ID)
	if err != nil || got.Metadata["owner"] != "alice" {
		t.Fatalf("conversation=%+v error=%v", got, err)
	}
	items, err := restarted.ListConversationItems(context.Background(), conversation.ID)
	if err != nil || len(items) != 2 || items[0].Content[0].Text != "hello" {
		t.Fatalf("history=%+v error=%v", items, err)
	}
	if _, err := restarted.CreateResponse(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(log)
	for _, want := range []string{"resume\n019f0000-0000-7000-8000-000000000001", "sandbox_mode=\"read-only\"", "approval_policy=\"never\"", "forced_login_method=\"chatgpt\"", "features.shell_tool=false", "features.plugins=false", "web_search=\"disabled\"", "--ignore-user-config"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("missing safe invocation %q: %s", want, args)
		}
	}
	if strings.Contains(string(args), "hello") || strings.Contains(string(args), "--last") {
		t.Fatalf("prompt in argv or ambiguous resume: %s", args)
	}
	if _, err := restarted.RetrieveConversation(context.Background(), "../config"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("path traversal: %v", err)
	}
}

func TestCLIFailsClosedForMissingLoginMalformedEventsAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		loginFails bool
	}{
		{"not-logged-in", "echo 'Not logged in' >&2; exit 1\n", true},
		{"api-login", "echo 'Logged in using an API key' >&2\n", true},
		{"no-completion", "echo '{\"type\":\"thread.started\",\"thread_id\":\"019f0000-0000-7000-8000-000000000001\"}'\n", false},
		{"malformed", "echo not-json\n", false},
		{"timeout", "exec sleep 10\n", false},
		{"failed-turn", "echo '{\"type\":\"turn.failed\",\"error\":{\"message\":\"secret upstream detail\"}}'\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if !tc.loginFails {
				body = "if [ \"$1\" = login ]; then echo 'Logged in using ChatGPT'; exit 0; fi\n" + body
			}
			client, err := NewClient(Options{Binary: fakeExecutable(t, body), StateDir: t.TempDir(), Timeout: 100 * time.Millisecond})
			if tc.loginFails {
				if err == nil {
					t.Fatal("accepted missing ChatGPT login")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := client.CreateConversation(context.Background(), map[string]string{"owner": "alice"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.CreateResponse(context.Background(), ResponseRequest{ConversationID: conversation.ID})
			if err == nil || strings.Contains(err.Error(), "secret upstream") {
				t.Fatalf("unsafe error=%v", err)
			}
			items, err := client.ListConversationItems(context.Background(), conversation.ID)
			if err != nil || len(items) != 0 {
				t.Fatalf("failed output persisted as visible history: %+v %v", items, err)
			}
		})
	}
}

func TestConversationLockRejectsOverlappingTurnsAndReleases(t *testing.T) {
	client, err := NewClient(Options{Binary: fakeExecutable(t, "echo 'Logged in using ChatGPT'\n"), StateDir: t.TempDir(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	release, err := client.AcquireConversation("conv_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AcquireConversation("conv_a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlapping turn allowed: %v", err)
	}
	release()
	if release, err = client.AcquireConversation("conv_a"); err != nil {
		t.Fatal(err)
	}
	release()
}

func TestCLITimeoutClosesInheritedChildOutput(t *testing.T) {
	// npm's codex executable launches a native child retaining stdout. Killing
	// only the wrapper must not leave that pipe blocking the HTTP request.
	binary := fakeExecutable(t, "if [ \"$1\" = login ]; then echo 'Logged in using ChatGPT'; exit 0; fi\nsleep 1\n")
	client, err := NewClient(Options{Binary: binary, StateDir: t.TempDir(), Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := client.CreateConversation(context.Background(), map[string]string{"owner": "alice"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = client.CreateResponse(context.Background(), ResponseRequest{ConversationID: conversation.ID})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("child pipe outlived timeout: %v", elapsed)
	}
}

func TestCLIRejectsUnexpectedBuiltinToolEvents(t *testing.T) {
	for _, kind := range []string{"command_execution", "file_change", "mcp_tool_call", "web_search"} {
		stream := `{"type":"thread.started","thread_id":"019f0000-0000-7000-8000-000000000001"}` + "\n" +
			`{"type":"item.started","item":{"type":"` + kind + `"}}` + "\n" +
			`{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}` + "\n" +
			`{"type":"turn.completed"}` + "\n"
		if _, _, err := readEvents(strings.NewReader(stream)); err == nil {
			t.Fatalf("accepted forbidden built-in tool %s", kind)
		}
	}
}

func TestCapacityReservedForWholeConversationTurn(t *testing.T) {
	client, err := NewClient(Options{Binary: fakeExecutable(t, "echo 'Logged in using ChatGPT'\n"), StateDir: t.TempDir(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	a, err := client.AcquireConversation("conv_a")
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := client.AcquireConversation("conv_b")
	if err != nil {
		t.Fatal(err)
	}
	if release, err := client.AcquireConversation("conv_c"); !errors.Is(err, ErrBusy) {
		if release != nil {
			release()
		}
		t.Fatalf("third user stole admitted turn capacity: %v", err)
	}
	b()
	c, err := client.AcquireConversation("conv_c")
	if err != nil {
		t.Fatal(err)
	}
	c()
}

func TestLoginTimeoutTerminatesLauncherChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := checkLogin(ctx, fakeExecutable(t, "sleep 1\n"), t.TempDir()); err == nil {
		t.Fatal("accepted timed out login")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("login child outlived timeout: %v", elapsed)
	}
}
