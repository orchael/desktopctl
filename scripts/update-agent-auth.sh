#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Update the ai-desktops agent secret with Codex and Claude Code auth.

This script updates an AWS Secrets Manager JSON secret such as:
  /ai-desktops/markcallen/agents

It preserves existing JSON keys and updates only the keys you provide:
  CODEX_AUTH                 literal contents of a Codex auth.json file
  CLAUDE_CODE_OAUTH_TOKEN    long-lived token printed by `claude setup-token`

Defaults:
  owner:      markcallen
  environment: dev
  secret id:  /ai-desktops/<owner>/agents
  Codex auth: ${CODEX_HOME:-$HOME/.codex}/auth.json

Claude Code token note:
  Claude Code docs say `claude setup-token` prints a one-year token and does
  not save it. This script can read CLAUDE_CODE_OAUTH_TOKEN from the current
  environment, from --claude-token-file, or from an interactive hidden prompt.

Examples:
  scripts/update-agent-auth.sh
  scripts/update-agent-auth.sh --owner acme --region us-east-1
  scripts/update-agent-auth.sh --profile my-aws-profile --yes
  scripts/update-agent-auth.sh --codex-auth-json ~/.codex/auth.json --skip-claude
  CLAUDE_CODE_OAUTH_TOKEN="$(pbpaste)" scripts/update-agent-auth.sh --yes

Options:
  --owner NAME              GitHub owner used in /ai-desktops/<owner>/agents
  --environment ENV         Environment tag for newly created secrets; default dev
  --secret-id ID            Override the full AWS Secrets Manager secret id
  --region REGION           AWS region; otherwise AWS CLI default resolution
  --profile PROFILE         AWS CLI profile
  --codex-auth-json PATH    Codex auth.json path
  --claude-token-file PATH  File containing only the Claude Code OAuth token
  --skip-codex              Do not update CODEX_AUTH
  --skip-claude             Do not update CLAUDE_CODE_OAUTH_TOKEN
  --yes                     Do not prompt before writing
  -h, --help                Show this help
EOF
}

owner="markcallen"
environment="dev"
secret_id=""
region=""
profile=""
codex_auth_json="${CODEX_HOME:-$HOME/.codex}/auth.json"
claude_token_file=""
skip_codex=false
skip_claude=false
assume_yes=false

while [ "$#" -gt 0 ]; do
  case "$1" in
    --owner)
      owner="${2:?--owner requires a value}"
      shift 2
      ;;
    --environment)
      environment="${2:?--environment requires a value}"
      shift 2
      ;;
    --secret-id)
      secret_id="${2:?--secret-id requires a value}"
      shift 2
      ;;
    --region)
      region="${2:?--region requires a value}"
      shift 2
      ;;
    --profile)
      profile="${2:?--profile requires a value}"
      shift 2
      ;;
    --codex-auth-json)
      codex_auth_json="${2:?--codex-auth-json requires a value}"
      shift 2
      ;;
    --claude-token-file)
      claude_token_file="${2:?--claude-token-file requires a value}"
      shift 2
      ;;
    --skip-codex)
      skip_codex=true
      shift
      ;;
    --skip-claude)
      skip_claude=true
      shift
      ;;
    --yes)
      assume_yes=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "ERROR: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$secret_id" ]; then
  secret_id="/ai-desktops/${owner}/agents"
fi

for tool in aws python3; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: required command not found: $tool" >&2
    exit 1
  fi
done

aws_args=()
if [ -n "$region" ]; then
  aws_args+=(--region "$region")
fi
if [ -n "$profile" ]; then
  aws_args+=(--profile "$profile")
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
existing_json="$tmp_dir/existing.json"
merged_json="$tmp_dir/merged.json"
codex_value_file="$tmp_dir/codex-auth.json"
claude_value_file="$tmp_dir/claude-token"

echo "Updating agent auth secret"
echo "  Secret: $secret_id"
echo "  Owner: $owner"
echo "  Environment: $environment"
if [ -n "$region" ]; then
  echo "  Region: $region"
else
  echo "  Region: AWS CLI default"
fi
if [ -n "$profile" ]; then
  echo "  Profile: $profile"
fi
echo

if [ "$skip_codex" = false ]; then
  if [ ! -f "$codex_auth_json" ]; then
    echo "ERROR: Codex auth file not found: $codex_auth_json" >&2
    echo "Run 'codex login' first, or pass --codex-auth-json PATH, or use --skip-codex." >&2
    exit 1
  fi
  python3 - "$codex_auth_json" "$codex_value_file" <<'PY'
import json
import pathlib
import sys

src = pathlib.Path(sys.argv[1])
dst = pathlib.Path(sys.argv[2])
text = src.read_text(encoding="utf-8")
json.loads(text)
dst.write_text(text, encoding="utf-8")
PY
  chmod 600 "$codex_value_file"
  echo "  CODEX_AUTH: will update from $codex_auth_json"
else
  : > "$codex_value_file"
  echo "  CODEX_AUTH: skipped"
fi

if [ "$skip_claude" = false ]; then
  claude_token="${CLAUDE_CODE_OAUTH_TOKEN:-}"
  if [ -z "$claude_token" ] && [ -n "$claude_token_file" ]; then
    if [ ! -f "$claude_token_file" ]; then
      echo "ERROR: Claude token file not found: $claude_token_file" >&2
      exit 1
    fi
    claude_token="$(python3 - "$claude_token_file" <<'PY'
import pathlib
import sys
print(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8").strip())
PY
)"
  fi
  if [ -z "$claude_token" ]; then
    cat <<'EOF'

Claude Code OAuth token not found in CLAUDE_CODE_OAUTH_TOKEN.
To create one, run this in another terminal:
  claude setup-token

Paste the printed token here. Input is hidden. Press Enter on an empty line to
leave CLAUDE_CODE_OAUTH_TOKEN unchanged.
EOF
    if [ -t 0 ]; then
      printf "CLAUDE_CODE_OAUTH_TOKEN: "
      IFS= read -rs claude_token || true
      printf "\n"
    fi
  fi
  if [ -n "$claude_token" ]; then
    printf '%s' "$claude_token" > "$claude_value_file"
    chmod 600 "$claude_value_file"
    echo "  CLAUDE_CODE_OAUTH_TOKEN: will update"
  else
    : > "$claude_value_file"
    echo "  CLAUDE_CODE_OAUTH_TOKEN: unchanged"
  fi
else
  : > "$claude_value_file"
  echo "  CLAUDE_CODE_OAUTH_TOKEN: skipped"
fi

echo
if [ "$assume_yes" = false ]; then
  printf "Write these updates to %s? [y/N]: " "$secret_id"
  IFS= read -r answer
  case "$answer" in
    y|Y|yes|YES)
      ;;
    *)
      echo "No changes written."
      exit 0
      ;;
  esac
fi

if aws secretsmanager get-secret-value "${aws_args[@]}" \
  --secret-id "$secret_id" \
  --query SecretString \
  --output text > "$existing_json" 2>/dev/null; then
  if [ ! -s "$existing_json" ] || [ "$(cat "$existing_json")" = "None" ]; then
    printf '{}' > "$existing_json"
  fi
else
  printf '{}' > "$existing_json"
fi

python3 - "$existing_json" "$merged_json" "$codex_value_file" "$claude_value_file" "$skip_codex" "$skip_claude" <<'PY'
import json
import pathlib
import sys

existing_path, merged_path, codex_path, claude_path, skip_codex, skip_claude = sys.argv[1:7]
raw = pathlib.Path(existing_path).read_text(encoding="utf-8").strip() or "{}"
try:
    data = json.loads(raw)
except json.JSONDecodeError as exc:
    raise SystemExit(f"existing secret is not valid JSON: {exc}") from exc
if not isinstance(data, dict):
    raise SystemExit("existing secret JSON must be an object")

if skip_codex != "true":
    codex_auth = pathlib.Path(codex_path).read_text(encoding="utf-8")
    if codex_auth:
        data["CODEX_AUTH"] = codex_auth

if skip_claude != "true":
    claude_token = pathlib.Path(claude_path).read_text(encoding="utf-8")
    if claude_token:
        data["CLAUDE_CODE_OAUTH_TOKEN"] = claude_token.strip()

pathlib.Path(merged_path).write_text(
    json.dumps(data, sort_keys=True, separators=(",", ":")),
    encoding="utf-8",
)
PY
chmod 600 "$merged_json"

if aws secretsmanager describe-secret "${aws_args[@]}" --secret-id "$secret_id" >/dev/null 2>&1; then
  aws secretsmanager put-secret-value "${aws_args[@]}" \
    --secret-id "$secret_id" \
    --secret-string "file://$merged_json" >/dev/null
  action="updated"
else
  aws secretsmanager create-secret "${aws_args[@]}" \
    --name "$secret_id" \
    --description "AI provider API keys and auth for ai-desktops owner: $owner" \
    --tags \
      Key=ai-desktops,Value=true \
      Key=github-owner,Value="$owner" \
      Key=environment,Value="$environment" \
    --secret-string "file://$merged_json" >/dev/null
  action="created"
fi

echo "Secret $action: $secret_id"
echo "Requested updates:"
if [ "$skip_codex" = false ] && [ -s "$codex_value_file" ]; then
  echo "  CODEX_AUTH"
fi
if [ "$skip_claude" = false ] && [ -s "$claude_value_file" ]; then
  echo "  CLAUDE_CODE_OAUTH_TOKEN"
fi
echo "Current secret key names:"
python3 - "$merged_json" <<'PY'
import json
import pathlib
import sys

data = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for key in sorted(data):
    print(f"  {key}")
PY

cat <<'EOF'

Existing desktops do not automatically reload this secret. Run:
  ai-desktops secrets reload <desktop-id>
Older desktops must first register this secret with `ai-desktops secrets add`.
Reload interrupts active sessions and replaces the desktop's auth snapshot.
EOF
