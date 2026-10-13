#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_DIR
readonly SOURCE_REMOTE="vercel"
readonly SOURCE_BRANCH="vercel/main-2.0.5"
readonly SOURCE_REF="refs/remotes/vercel/vercel/main-2.0.5"
readonly EXPECTED_VERSION="2.0.5"
readonly BUCKET="hive-ci-binaries"
readonly PREFIX="nomad"
readonly REGION="us-east-1"
readonly BUCKET_ACCOUNT_ID="000806556181"
readonly DEFAULT_AWS_PROFILE="vercel-hive"

aws_profile="${NOMAD_UPLOAD_PROFILE:-$DEFAULT_AWS_PROFILE}"
artifact_dir=""

log() {
  printf '==> %s\n' "$*" >&2
}

warn() {
  printf 'WARNING: %s\n' "$*" >&2
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Upload the latest build.sh artifacts without overwriting an existing object.

Usage:
  ./upload.sh [ARTIFACT_DIR] [--profile PROFILE]

With no ARTIFACT_DIR, the script uses the last successful build recorded by
build.sh. The default AWS profile is vercel-hive.
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

sha256_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

file_size() {
  local size

  if size="$(stat -f '%z' "$1" 2>/dev/null)"; then
    printf '%s\n' "$size"
    return
  fi
  stat -c '%s' "$1"
}

aws_cli() {
  aws --profile "$aws_profile" --region "$REGION" --no-cli-pager "$@"
}

refresh_source() {
  git -C "$REPO_DIR" fetch --quiet --no-tags "$SOURCE_REMOTE" \
    "+refs/heads/$SOURCE_BRANCH:$SOURCE_REF"
  git -C "$REPO_DIR" rev-parse --verify "$SOURCE_REF^{commit}"
}

preflight_bucket() {
  local account
  local bucket_region
  local versioning
  local encryption

  account="$(aws_cli sts get-caller-identity --query Account --output text)"
  [[ "$account" == "$BUCKET_ACCOUNT_ID" ]] ||
    die "profile $aws_profile resolves to account $account, expected $BUCKET_ACCOUNT_ID"

  bucket_region="$(aws_cli s3api get-bucket-location \
    --bucket "$BUCKET" \
    --expected-bucket-owner "$BUCKET_ACCOUNT_ID" \
    --query LocationConstraint --output text)"
  if [[ "$bucket_region" == "None" || "$bucket_region" == "null" ]]; then
    bucket_region="us-east-1"
  fi
  [[ "$bucket_region" == "$REGION" ]] || die "$BUCKET is in unexpected region $bucket_region"

  versioning="$(aws_cli s3api get-bucket-versioning \
    --bucket "$BUCKET" \
    --expected-bucket-owner "$BUCKET_ACCOUNT_ID" \
    --query Status --output text)"
  encryption="$(aws_cli s3api get-bucket-encryption \
    --bucket "$BUCKET" \
    --expected-bucket-owner "$BUCKET_ACCOUNT_ID" \
    --query 'ServerSideEncryptionConfiguration.Rules[].ApplyServerSideEncryptionByDefault.SSEAlgorithm' \
    --output text)"
  [[ "$encryption" == *"AES256"* ]] || die "$BUCKET does not report AES256 encryption"

  printf 'profile=%s account=%s bucket=%s region=%s prefix=%s versioning=%s encryption=%s\n' \
    "$aws_profile" "$account" "$BUCKET" "$bucket_region" "$PREFIX" "$versioning" "$encryption"
}

assert_absent() {
  local key="$1"
  local error_output

  if error_output="$(aws_cli s3api head-object \
    --bucket "$BUCKET" \
    --key "$key" \
    --expected-bucket-owner "$BUCKET_ACCOUNT_ID" 2>&1)"; then
    die "refusing to overwrite existing object s3://$BUCKET/$key"
  fi
  case "$error_output" in
    *"(404)"* | *"Not Found"* | *"NoSuchKey"*) ;;
    *) die "could not prove s3://$BUCKET/$key is absent: $error_output" ;;
  esac
}

put_artifact() {
  local path="$1"
  local key="$2"
  local expected_sha="$3"
  local expected_size="$4"
  local checksum_base64
  local put_output
  local head_output
  local remote_checksum
  local remote_size
  local remote_encryption

  checksum_base64="$(openssl dgst -sha256 -binary "$path" | openssl base64 -A)"
  log "Creating s3://$BUCKET/$key"
  if put_output="$(aws_cli s3api put-object \
    --bucket "$BUCKET" \
    --key "$key" \
    --body "$path" \
    --content-type binary/octet-stream \
    --server-side-encryption AES256 \
    --checksum-algorithm SHA256 \
    --checksum-sha256 "$checksum_base64" \
    --if-none-match '*' \
    --expected-bucket-owner "$BUCKET_ACCOUNT_ID" \
    --query ChecksumSHA256 --output text 2>&1)"; then
    if [[ "$put_output" != "$checksum_base64" ]]; then
      warn "PutObject did not echo the checksum; verifying it with HeadObject"
    fi
  else
    case "$put_output" in
      *"PreconditionFailed"* | *"(412)"* | *"ConditionalRequestConflict"* | *"(409)"*)
        die "a concurrent writer created s3://$BUCKET/$key; nothing was overwritten"
        ;;
      *) die "upload failed for s3://$BUCKET/$key: $put_output" ;;
    esac
  fi

  head_output="$(aws_cli s3api head-object \
    --bucket "$BUCKET" \
    --key "$key" \
    --checksum-mode ENABLED \
    --expected-bucket-owner "$BUCKET_ACCOUNT_ID" \
    --query '[ChecksumSHA256,ContentLength,ServerSideEncryption]' --output text)"
  IFS=$'\t' read -r remote_checksum remote_size remote_encryption <<<"$head_output"
  [[ "$remote_checksum" == "$checksum_base64" ]] || die "remote checksum mismatch for $key"
  [[ "$remote_size" == "$expected_size" ]] || die "remote size mismatch for $key"
  [[ "$remote_encryption" == "AES256" ]] || die "remote encryption mismatch for $key"

  printf 'uploaded  sha256:%s  s3://%s/%s\n' "$expected_sha" "$BUCKET" "$key"
}

parse_args() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --profile)
        [[ "$#" -ge 2 ]] || die "--profile requires a value"
        aws_profile="$2"
        shift 2
        ;;
      -h | --help)
        usage
        exit 0
        ;;
      -*) die "unknown option: $1" ;;
      *)
        [[ -z "$artifact_dir" ]] || die "only one artifact directory may be supplied"
        artifact_dir="$1"
        shift
        ;;
    esac
  done
}

main() {
  local manifest
  local record
  local field1
  local field2
  local field3
  local field4
  local extra
  local format_version=""
  local manifest_remote=""
  local manifest_branch=""
  local source_commit=""
  local version=""
  local build_date=""
  local build_image=""
  local artifact_count=0
  local amd64_count=0
  local arm64_count=0
  local current_commit
  local index
  local arch
  local name
  local expected_sha
  local expected_size
  local path
  local actual_sha
  local actual_size
  local file_output
  local arches=()
  local names=()
  local hashes=()
  local sizes=()
  local paths=()
  local keys=()

  parse_args "$@"
  require_command aws
  require_command file
  require_command git
  require_command openssl
  require_command shasum
  require_command stat
  [[ -n "$aws_profile" ]] || die "AWS profile may not be empty"

  if [[ -z "$artifact_dir" ]]; then
    [[ -f "$REPO_DIR/pkg/custom-nomad/LAST_BUILD" ]] ||
      die "no successful build recorded; run ./build.sh first"
    artifact_dir="$(<"$REPO_DIR/pkg/custom-nomad/LAST_BUILD")"
  fi
  if [[ "$artifact_dir" != /* ]]; then
    artifact_dir="$PWD/$artifact_dir"
  fi
  [[ -d "$artifact_dir" ]] || die "artifact directory not found: $artifact_dir"
  artifact_dir="$(cd "$artifact_dir" && pwd -P)"
  manifest="$artifact_dir/manifest.tsv"
  [[ -f "$manifest" ]] || die "manifest not found: $manifest"

  while IFS=$'\t' read -r record field1 field2 field3 field4 extra; do
    [[ -n "$record" ]] || continue
    case "$record" in
      format_version) format_version="$field1" ;;
      remote) manifest_remote="$field1" ;;
      branch) manifest_branch="$field1" ;;
      source_commit) source_commit="$field1" ;;
      version) version="$field1" ;;
      build_date) build_date="$field1" ;;
      build_image) build_image="$field1" ;;
      artifact)
        [[ -z "$extra" ]] || die "malformed artifact record"
        arches[artifact_count]="$field1"
        names[artifact_count]="$field2"
        hashes[artifact_count]="$field3"
        sizes[artifact_count]="$field4"
        artifact_count=$((artifact_count + 1))
        ;;
      *) die "unknown manifest record: $record" ;;
    esac
  done <"$manifest"

  [[ "$format_version" == "1" ]] || die "unsupported manifest format"
  [[ "$manifest_remote" == "$SOURCE_REMOTE" ]] || die "unexpected source remote"
  [[ "$manifest_branch" == "$SOURCE_BRANCH" ]] || die "unexpected source branch"
  [[ "$version" == "$EXPECTED_VERSION" ]] || die "unexpected Nomad version"
  [[ "$source_commit" =~ ^[0-9a-f]{40}$ ]] || die "invalid source commit"
  [[ -n "$build_date" && -n "$build_image" ]] || die "incomplete build metadata"
  [[ "$artifact_count" -eq 2 ]] || die "manifest must contain exactly two artifacts"

  for ((index = 0; index < artifact_count; index++)); do
    arch="${arches[$index]}"
    name="${names[$index]}"
    expected_sha="${hashes[$index]}"
    expected_size="${sizes[$index]}"
    [[ "$expected_sha" =~ ^[0-9a-f]{64}$ ]] || die "invalid SHA-256: $expected_sha"
    [[ "$expected_size" =~ ^[0-9]+$ ]] || die "invalid file size: $expected_size"
    [[ "$name" != */* ]] || die "artifact filename contains a slash"
    [[ "$name" == "nomad-$version-$arch@$expected_sha" ]] || die "artifact filename is inconsistent"

    case "$arch" in
      x86_64) amd64_count=$((amd64_count + 1)) ;;
      arm64) arm64_count=$((arm64_count + 1)) ;;
      *) die "unsupported architecture: $arch" ;;
    esac
    path="$artifact_dir/$name"
    [[ -f "$path" ]] || die "artifact missing: $path"
    file_output="$(file "$path")"
    case "$arch" in
      x86_64)
        [[ "$file_output" == *"ELF 64-bit LSB"* && "$file_output" == *"x86-64"* ]] ||
          die "$path is not a Linux x86-64 ELF"
        ;;
      arm64)
        [[ "$file_output" == *"ELF 64-bit LSB"* && "$file_output" == *"ARM aarch64"* ]] ||
          die "$path is not a Linux ARM64 ELF"
        ;;
    esac
    actual_sha="$(sha256_file "$path")"
    actual_size="$(file_size "$path")"
    [[ "$actual_sha" == "$expected_sha" ]] || die "local checksum mismatch for $name"
    [[ "$actual_size" == "$expected_size" ]] || die "local size mismatch for $name"
    paths[index]="$path"
    keys[index]="$PREFIX/$name"
  done
  [[ "$amd64_count" -eq 1 && "$arm64_count" -eq 1 ]] ||
    die "manifest must contain one x86_64 and one arm64 artifact"

  log "Confirming these were built from the latest $SOURCE_REMOTE/$SOURCE_BRANCH commit"
  current_commit="$(refresh_source)"
  [[ "$current_commit" == "$source_commit" ]] ||
    die "$SOURCE_BRANCH is now $current_commit; rebuild required"
  preflight_bucket

  for ((index = 0; index < artifact_count; index++)); do
    assert_absent "${keys[$index]}"
  done
  current_commit="$(refresh_source)"
  [[ "$current_commit" == "$source_commit" ]] || die "$SOURCE_BRANCH advanced during preflight"

  log "Both keys are absent; starting atomic create-only uploads"
  for ((index = 0; index < artifact_count; index++)); do
    put_artifact "${paths[$index]}" "${keys[$index]}" "${hashes[$index]}" "${sizes[$index]}"
  done
}

main "$@"
