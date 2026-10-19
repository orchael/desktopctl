#!/usr/bin/env bash
# Build and manage public desktopctl AMIs. Deletion previews by default.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/public-amis.sh <build|list|delete|prune> \
  --profile PROFILE --regions REGION[,REGION...] [options]

Options:
  --config PATH   desktopctl config (required for build, delete, and prune)
  --cli PATH      desktopctl binary (default: repository bin/desktopctl; not needed for list)
  --account-id ID Expected AWS account ID (optional safety check)
  --enable-public-sharing  Allow build to disable AMI Block Public Access in approved regions
  --ami ID        AMI ID for delete (delete requires one region)
  --keep N        Number of newest images to retain per major and region (prune; default: 2)
  --apply         Perform delete or prune; without this, print the plan only

Only public, account-owned AMIs tagged ManagedBy=desktopctl-packer and named
desktopctl-v<major>-<UTC YYYYMMDD>-<build timestamp> are managed.
With --enable-public-sharing, build enables public AMI sharing when blocked in
ca-central-1, us-east-1, us-east-2, us-west-1, or us-west-2, then leaves it
enabled. This changes the regional account setting.
Deletion also asks for confirmation through desktopctl ami delete and refuses
the active_ami pinned in the selected config.
EOF
}

if [[ $# -eq 0 ]]; then usage; exit 2; fi
action=$1
shift
case "$action" in build|list|delete|prune) ;; *) usage >&2; exit 2 ;; esac

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cli="$repo_root/bin/desktopctl"
config_path=''
profile=''
regions_csv=''
account_id=''
ami_id=''
keep=2
apply=false
enable_public_sharing=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config|--profile|--regions|--account-id|--ami|--keep|--cli)
      if [[ $# -lt 2 ]]; then echo "Missing value for $1" >&2; exit 2; fi
      case "$1" in
        --config) config_path=$2 ;;
        --profile) profile=$2 ;;
        --regions) regions_csv=$2 ;;
        --account-id) account_id=$2 ;;
        --ami) ami_id=$2 ;;
        --keep) keep=$2 ;;
        --cli) cli=$2 ;;
      esac
      shift 2 ;;
    --apply) apply=true; shift ;;
    --enable-public-sharing) enable_public_sharing=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$profile" || -z "$regions_csv" ]]; then
  echo '--profile and --regions are required.' >&2
  exit 2
fi
if [[ -n "$account_id" && ! "$account_id" =~ ^[0-9]{12}$ ]]; then
  echo '--account-id must be a 12-digit AWS account ID.' >&2
  exit 2
fi
if [[ "$action" != list ]]; then
  if [[ ! -f "$config_path" || ! -x "$cli" ]]; then
    echo 'A readable --config and executable --cli are required for build, delete, and prune.' >&2
    exit 2
  fi
  config_path="$(cd "$(dirname "$config_path")" && pwd -P)/$(basename "$config_path")"
  cli="$(cd "$(dirname "$cli")" && pwd -P)/$(basename "$cli")"
fi
if [[ ! "$keep" =~ ^[1-9][0-9]*$ ]]; then echo '--keep must be at least 1.' >&2; exit 2; fi
if [[ "$action" == delete && -z "$ami_id" ]]; then echo 'delete requires --ami.' >&2; exit 2; fi
if [[ "$action" != delete && -n "$ami_id" ]]; then echo '--ami is only valid for delete.' >&2; exit 2; fi
if [[ "$action" != build && "$enable_public_sharing" == true ]]; then echo '--enable-public-sharing is only valid for build.' >&2; exit 2; fi

tools=(aws jq)
for tool in "${tools[@]}"; do
  command -v "$tool" >/dev/null || { echo "$tool is required." >&2; exit 2; }
done
actual_account=$(aws sts get-caller-identity --profile "$profile" --query Account --output text)
if [[ ! "$actual_account" =~ ^[0-9]{12}$ ]]; then
  echo "Could not determine a valid AWS account ID from profile $profile." >&2
  exit 1
fi
if [[ -n "$account_id" && "$actual_account" != "$account_id" ]]; then
  echo "AWS profile $profile resolves to account $actual_account, expected $account_id." >&2
  exit 1
fi
account_id=$actual_account

IFS=',' read -r -a regions <<< "$regions_csv"
if [[ ${#regions[@]} -eq 0 ]]; then echo 'At least one region is required.' >&2; exit 2; fi
seen_regions=()
for region in "${regions[@]}"; do
  if [[ ! "$region" =~ ^[a-z0-9-]+$ ]]; then echo "Invalid region: $region" >&2; exit 2; fi
  for seen in "${seen_regions[@]}"; do
    if [[ "$seen" == "$region" ]]; then echo "Duplicate region: $region" >&2; exit 2; fi
  done
  seen_regions+=("$region")
done
if [[ "$action" == delete && ${#regions[@]} -ne 1 ]]; then
  echo 'delete requires exactly one region.' >&2; exit 2
fi

# Packer paths in desktopctl are relative to the repository checkout.
cd "$repo_root"

image_json() {
  local region=$1
  aws ec2 describe-images --profile "$profile" --region "$region" --owners self \
    --filters 'Name=name,Values=desktopctl-v*' \
      'Name=tag:ManagedBy,Values=desktopctl-packer' \
      'Name=is-public,Values=true' 'Name=state,Values=available' --output json
}

ensure_public_sharing() {
  local region=$1 state_json state managed_by allowed=false response
  state_json=$(aws ec2 get-image-block-public-access-state --profile "$profile" --region "$region" --output json)
  state=$(jq -er '.ImageBlockPublicAccessState' <<< "$state_json")
  managed_by=$(jq -r '.ManagedBy // "unknown"' <<< "$state_json")
  if [[ "$state" == unblocked ]]; then return 0; fi
  if [[ "$state" != block-new-sharing ]]; then
    echo "Unexpected AMI Block Public Access state '$state' in $region." >&2
    return 1
  fi
  if [[ "$managed_by" != account ]]; then
    echo "Cannot enable public AMI sharing in $region: setting is managed by $managed_by." >&2
    return 1
  fi
  if [[ "$enable_public_sharing" != true ]]; then
    echo "AMI public sharing is blocked in $region; pass --enable-public-sharing to change the regional account setting." >&2
    return 1
  fi
  case "$region" in
    ca-central-1|us-east-1|us-east-2|us-west-1|us-west-2) allowed=true ;;
  esac
  if [[ "$allowed" != true ]]; then
    echo "AMI public sharing is blocked in $region; automatic account-setting changes are limited to ca-central-1, us-east-1, us-east-2, us-west-1, and us-west-2." >&2
    return 1
  fi
  echo "Enabling public AMI sharing in account $account_id, region $region; it will remain enabled."
  response=$(aws ec2 disable-image-block-public-access --profile "$profile" --region "$region" --output json)
  if [[ $(jq -r '.ImageBlockPublicAccessState // ""' <<< "$response") != unblocked ]]; then
    echo "AWS did not confirm public AMI sharing is enabled in $region." >&2
    return 1
  fi
}

# Recheck image names locally; EC2 wildcards and tags alone are too broad for deletion.
# jq receives $account as a jq variable.
# shellcheck disable=SC2016
managed_filter='[.Images[] | select(.Public == true and .OwnerId == $account and any(.Tags[]?; .Key == "ManagedBy" and .Value == "desktopctl-packer") and (.Name | test("^desktopctl-v(0|[1-9][0-9]*)-[0-9]{8}-[0-9]+$")))]'

for region in "${regions[@]}"; do
  echo "Account $account_id, region $region, action $action"
  if [[ "$action" == build ]]; then
    ensure_public_sharing "$region"
    "$cli" --config "$config_path" --profile "$profile" ami build --regions "$region" --public
    continue
  fi

  images=$(image_json "$region")
  if [[ "$action" == list ]]; then
    jq -r --arg account "$account_id" "$managed_filter | sort_by(.CreationDate) | reverse | .[] | [.ImageId, .Name, .CreationDate] | @tsv" <<< "$images"
    continue
  fi

  if [[ "$action" == delete ]]; then
    candidates=$(jq -r --arg account "$account_id" --arg id "$ami_id" "$managed_filter | .[] | select(.ImageId == \$id) | [.ImageId, .Name, .CreationDate] | @tsv" <<< "$images")
    if [[ -z "$candidates" ]]; then
      echo "AMI $ami_id is not an available public desktopctl image owned by $account_id with the required name and tag." >&2
      exit 1
    fi
  else
    candidates=$(jq -r --arg account "$account_id" --argjson keep "$keep" "$managed_filter | group_by(.Name | capture(\"^desktopctl-v(?<major>[0-9]+)-\").major) | .[] | sort_by(.CreationDate) | reverse | .[\$keep:][] | [.ImageId, .Name, .CreationDate] | @tsv" <<< "$images")
  fi

  if [[ -z "$candidates" ]]; then echo 'No deletion candidates.'; continue; fi
  while IFS=$'\t' read -r candidate_id candidate_name candidate_date <&3; do
    [[ -n "$candidate_id" ]] || continue
    echo "Candidate: $candidate_id  $candidate_name  $candidate_date"
    if [[ "$apply" == true ]]; then
      in_use=$(aws ec2 describe-instances --profile "$profile" --region "$region" \
        --filters "Name=image-id,Values=$candidate_id" \
          'Name=instance-state-name,Values=pending,running,stopping,stopped' \
        --query 'Reservations[].Instances[].InstanceId' --output text)
      if [[ -n "$in_use" ]]; then
        echo "Refusing to delete $candidate_id; instances still use it: $in_use" >&2
        exit 1
      fi
      "$cli" --config "$config_path" --profile "$profile" --region "$region" ami delete "$candidate_id"
    fi
  done 3<<< "$candidates"
  if [[ "$apply" == false ]]; then echo 'Preview only. Pass --apply to request deletion.'; fi
done
