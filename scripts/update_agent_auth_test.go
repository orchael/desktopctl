package scripts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeCodexAuth(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, "auth.json")
	contents := `{"auth_mode":"chatgpt","tokens":{"access_token":"access","refresh_token":"refresh"}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runUpdateAgentAuth(t *testing.T, awsScript string, extraArgs ...string) ([]byte, string, error) {
	t.Helper()
	home := t.TempDir()
	bin := t.TempDir()
	calls := filepath.Join(home, "aws-calls")
	writeExecutable(t, filepath.Join(bin, "aws"), awsScript)
	auth := writeCodexAuth(t, home)
	args := []string{"--secret-id", "/test/agents", "--region", "us-east-2", "--codex-auth-json", auth, "--skip-claude", "--yes"}
	args = append(args, extraArgs...)
	cmd := exec.Command("./update-agent-auth.sh", args...)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "AWS_CALLS="+calls)
	out, err := cmd.CombinedOutput()
	callBytes, _ := os.ReadFile(calls)
	return out, string(callBytes), err
}

func TestUpdateAgentAuthFailsClosedWhenExistingSecretCannotBeRead(t *testing.T) {
	aws := `#!/bin/sh
printf '%s\n' "$*" >> "$AWS_CALLS"
case "$2" in
  get-secret-value) printf '%s\n' 'network unavailable' >&2; exit 255 ;;
  describe-secret) exit 0 ;;
  put-secret-value) exit 0 ;;
  *) exit 1 ;;
esac
`
	out, calls, err := runUpdateAgentAuth(t, aws)
	if err == nil {
		t.Fatalf("ambiguous read failure succeeded; output=%s calls=%s", out, calls)
	}
	if strings.Contains(calls, "put-secret-value") || strings.Contains(calls, "create-secret") {
		t.Fatalf("read failure attempted a write: %s", calls)
	}
	if !strings.Contains(string(out), "could not read existing secret") {
		t.Fatalf("missing safe read error: %s", out)
	}
}

func TestUpdateAgentAuthFailsClosedWhenSecretExistenceIsUnknown(t *testing.T) {
	aws := `#!/bin/sh
printf '%s\n' "$*" >> "$AWS_CALLS"
case "$2" in
  describe-secret) printf '%s\n' 'network unavailable' >&2; exit 255 ;;
  *) exit 1 ;;
esac
`
	out, calls, err := runUpdateAgentAuth(t, aws)
	if err == nil {
		t.Fatalf("ambiguous existence check succeeded; output=%s calls=%s", out, calls)
	}
	if strings.Contains(calls, "put-secret-value") || strings.Contains(calls, "create-secret") {
		t.Fatalf("existence failure attempted a write: %s", calls)
	}
	if !strings.Contains(string(out), "could not determine whether secret exists") {
		t.Fatalf("missing safe existence error: %s", out)
	}
}

func TestUpdateAgentAuthFailsClosedWhenExistingSecretHasNoStringValue(t *testing.T) {
	aws := `#!/bin/sh
printf '%s\n' "$*" >> "$AWS_CALLS"
case "$2" in
  describe-secret) exit 0 ;;
  get-secret-value) printf '%s' 'None' ;;
  *) exit 1 ;;
esac
`
	out, calls, err := runUpdateAgentAuth(t, aws)
	if err == nil {
		t.Fatalf("missing SecretString succeeded; output=%s calls=%s", out, calls)
	}
	if strings.Contains(calls, "put-secret-value") || strings.Contains(calls, "create-secret") {
		t.Fatalf("missing SecretString attempted a write: %s", calls)
	}
	if !strings.Contains(string(out), "does not contain a readable JSON SecretString") {
		t.Fatalf("missing safe SecretString error: %s", out)
	}
}

func TestUpdateAgentAuthFailsClosedWhenExistingSecretStringIsWhitespace(t *testing.T) {
	aws := `#!/bin/sh
printf '%s\n' "$*" >> "$AWS_CALLS"
case "$2" in
  describe-secret) exit 0 ;;
  get-secret-value) printf '   \n\t' ;;
  *) exit 0 ;;
esac
`
	out, calls, err := runUpdateAgentAuth(t, aws)
	if err == nil {
		t.Fatalf("whitespace SecretString succeeded; output=%s calls=%s", out, calls)
	}
	if strings.Contains(calls, "put-secret-value") || strings.Contains(calls, "create-secret") {
		t.Fatalf("whitespace SecretString attempted a write: %s", calls)
	}
}

func TestUpdateAgentAuthDoesNotTrustNotFoundTextOutsideAWSErrorCode(t *testing.T) {
	aws := `#!/bin/sh
printf '%s\n' "$*" >> "$AWS_CALLS"
case "$2" in
  describe-secret) printf '%s\n' 'An error occurred (AccessDeniedException) when calling the DescribeSecret operation: denied for /test/ResourceNotFoundException' >&2; exit 254 ;;
  *) exit 0 ;;
esac
`
	out, calls, err := runUpdateAgentAuth(t, aws, "--secret-id", "/test/ResourceNotFoundException")
	if err == nil {
		t.Fatalf("misleading AccessDenied error succeeded; output=%s calls=%s", out, calls)
	}
	if strings.Contains(calls, "create-secret") || strings.Contains(calls, "put-secret-value") {
		t.Fatalf("misleading AccessDenied error attempted a write: %s", calls)
	}
}

func TestUpdateAgentAuthPreservesUnmodifiedKeys(t *testing.T) {
	home := t.TempDir()
	captured := filepath.Join(home, "captured.json")
	t.Setenv("AWS_CAPTURE", captured)
	aws := `#!/bin/sh
printf '%s\n' "$*" >> "$AWS_CALLS"
case "$2" in
  describe-secret) exit 0 ;;
  get-secret-value) printf '%s' '{"CLAUDE_CODE_OAUTH_TOKEN":"claude","GEMINI_API_KEY":"gemini","OPENAI_API_KEY":"openai"}' ;;
  put-secret-value)
    for arg in "$@"; do
      case "$arg" in file://*) cp "${arg#file://}" "$AWS_CAPTURE" ;; esac
    done
    ;;
  *) exit 1 ;;
esac
`
	out, calls, err := runUpdateAgentAuth(t, aws)
	if err != nil {
		t.Fatalf("update failed: %v\noutput=%s\ncalls=%s", err, out, calls)
	}
	b, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "claude",
		"GEMINI_API_KEY":          "gemini",
		"OPENAI_API_KEY":          "openai",
	} {
		if got[key] != want {
			t.Fatalf("%s was not preserved: %#v", key, got[key])
		}
	}
	if _, ok := got["CODEX_AUTH"]; !ok {
		t.Fatal("CODEX_AUTH was not updated")
	}
}

func TestUpdateAgentAuthCanCreateAndReloadDesktop(t *testing.T) {
	home := t.TempDir()
	aiCalls := filepath.Join(home, "ai-calls")
	ai := filepath.Join(home, "ai-desktops")
	writeExecutable(t, ai, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AI_CALLS\"\n")
	aws := `#!/bin/sh
printf '%s\n' "$*" >> "$AWS_CALLS"
case "$2" in
  get-secret-value|describe-secret) printf '%s\n' 'An error occurred (ResourceNotFoundException) when calling the DescribeSecret operation: secret not found' >&2; exit 254 ;;
  create-secret) exit 0 ;;
  *) exit 1 ;;
esac
`
	t.Setenv("AI_CALLS", aiCalls)
	out, calls, err := runUpdateAgentAuth(t, aws, "--reload-desktop", "d-test", "--ai-desktops-bin", ai)
	if err != nil {
		t.Fatalf("create and reload failed: %v\noutput=%s\ncalls=%s", err, out, calls)
	}
	if !strings.Contains(calls, "create-secret") {
		t.Fatalf("missing create call: %s", calls)
	}
	aiCallBytes, _ := os.ReadFile(aiCalls)
	if got := strings.TrimSpace(string(aiCallBytes)); got != "secrets reload d-test" {
		t.Fatalf("reload call = %q", got)
	}
}

func TestUpdateAgentAuthPropagatesReloadFailure(t *testing.T) {
	home := t.TempDir()
	ai := filepath.Join(home, "ai-desktops")
	writeExecutable(t, ai, "#!/bin/sh\nexit 42\n")
	aws := `#!/bin/sh
printf '%s\n' "$*" >> "$AWS_CALLS"
case "$2" in
  get-secret-value|describe-secret) printf '%s\n' 'An error occurred (ResourceNotFoundException) when calling the DescribeSecret operation: secret not found' >&2; exit 254 ;;
  create-secret) exit 0 ;;
  *) exit 1 ;;
esac
`
	out, _, err := runUpdateAgentAuth(t, aws, "--reload-desktop", "d-test", "--ai-desktops-bin", ai)
	if err == nil {
		t.Fatalf("reload failure was ignored: %s", out)
	}
}
