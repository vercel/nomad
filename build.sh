#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_DIR
readonly SOURCE_REMOTE="vercel"
readonly SOURCE_BRANCH="vercel/main-2.0.5"
readonly SOURCE_REF="refs/remotes/vercel/vercel/main-2.0.5"
readonly BASE_TAG="v2.0.5"
readonly EXPECTED_VERSION="2.0.5"
readonly BUILD_IMAGE="amazonlinux:2023"
readonly OUTPUT_ROOT="$REPO_DIR/pkg/custom-nomad"

build_dir=""

log() {
  printf '==> %s\n' "$*" >&2
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Build linux/x86_64 and linux/arm64 Nomad binaries from the latest
vercel/main-2.0.5 commit, then write raw artifacts and SHA256SUMS under pkg/.

Usage:
  ./build.sh
EOF
}

cleanup() {
  if [[ -n "$build_dir" && -d "$build_dir" && "$(basename "$build_dir")" == nomad-release.* ]]; then
    rm -rf -- "$build_dir"
  fi
}
trap cleanup EXIT

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

refresh_source() {
  git -C "$REPO_DIR" fetch --quiet --no-tags "$SOURCE_REMOTE" \
    "+refs/heads/$SOURCE_BRANCH:$SOURCE_REF"
  git -C "$REPO_DIR" rev-parse --verify "$SOURCE_REF^{commit}"
}

validate_binary() {
  local binary="$1"
  local docker_arch="$2"
  local file_arch="$3"
  local version="$4"
  local source_commit="$5"
  local build_date="$6"
  local file_output
  local version_output

  file_output="$(file "$binary")"
  [[ "$file_output" == *"ELF 64-bit LSB"* && "$file_output" == *"$file_arch"* ]] ||
    die "unexpected binary architecture: $file_output"
  printf '%s\n' "$file_output"

  version_output="$(docker run --rm \
    --platform "linux/$docker_arch" \
    --volume "$binary:/usr/local/bin/nomad:ro" \
    "$BUILD_IMAGE" \
    nomad version)"
  printf '%s\n' "$version_output"
  grep -Fqx "Nomad v$version" <<<"$version_output" || die "$binary reports the wrong version"
  grep -Fqx "BuildDate $build_date" <<<"$version_output" || die "$binary reports the wrong build date"
  grep -Fqx "Revision $source_commit" <<<"$version_output" || die "$binary reports the wrong revision"
}

main() {
  local source_commit
  local latest_commit
  local source_dir
  local staging_dir
  local go_version
  local node_version
  local pnpm_version
  local version
  local build_date
  local arm64_binary
  local amd64_binary
  local arm64_sha
  local amd64_sha
  local arm64_name
  local amd64_name
  local arm64_size
  local amd64_size
  local artifact_dir
  local pointer_tmp

  if [[ "$#" -gt 0 ]]; then
    case "$1" in
      -h | --help)
        usage
        return
        ;;
      *) die "unknown option: $1" ;;
    esac
  fi

  require_command docker
  require_command file
  require_command git
  require_command shasum
  require_command stat
  docker info >/dev/null

  log "Fetching the latest $SOURCE_REMOTE/$SOURCE_BRANCH commit"
  source_commit="$(refresh_source)"
  git -C "$REPO_DIR" fetch --quiet --no-tags origin \
    "refs/tags/$BASE_TAG:refs/tags/$BASE_TAG"
  git -C "$REPO_DIR" merge-base --is-ancestor "$BASE_TAG" "$source_commit" ||
    die "$source_commit does not descend from $BASE_TAG"
  log "Building source commit $source_commit"

  build_dir="$(mktemp -d "${TMPDIR:-/tmp}/nomad-release.XXXXXX")"
  source_dir="$build_dir/nomad"
  staging_dir="$build_dir/staging"
  mkdir -p "$staging_dir"
  git clone --quiet --local --no-hardlinks --no-checkout "$REPO_DIR" "$source_dir"
  git -C "$source_dir" checkout --quiet --detach "$source_commit"

  go_version="$(tr -d '[:space:]' <"$source_dir/.go-version")"
  node_version="$(sed -n 's/.*"node": "\([^"]*\)".*/\1/p' "$source_dir/package.json")"
  pnpm_version="$(sed -n 's/.*"packageManager": "pnpm@\([0-9][0-9.]*\).*/\1/p' \
    "$source_dir/package.json")"
  version="$("$source_dir/scripts/version.sh" \
    "$source_dir/version/version.go" "$source_dir/version/version.go")"
  build_date="$(TZ=UTC0 git -C "$source_dir" show -s \
    --format=%cd --date=format-local:'%Y-%m-%dT%H:%M:%SZ' HEAD)"
  [[ "$version" == "$EXPECTED_VERSION" ]] ||
    die "expected Nomad $EXPECTED_VERSION, got $version at $source_commit"

  log "Generating release assets and building linux/arm64"
  docker run --rm \
    --platform linux/arm64 \
    --volume "$source_dir:/tmp/nomad" \
    --workdir /tmp/nomad \
    --env "GO_VERSION=$go_version" \
    --env "NODE_VERSION=$node_version" \
    --env "PNPM_VERSION=$pnpm_version" \
    "$BUILD_IMAGE" \
    bash -euxo pipefail -c '
      dnf install -y git gcc gcc-c++ make tar gzip xz zip libatomic tree
      curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-arm64.tar.gz" | tar -C /usr/local -xz
      curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-arm64.tar.xz" | tar -C /usr/local --strip-components=1 -xJ
      export PATH="/usr/local/go/bin:/root/go/bin:$PATH"
      corepack enable
      corepack prepare "pnpm@${PNPM_VERSION}" --activate
      make deps
      make prerelease GO_TAGS="ui codegen_generated release hashicorpmetrics"
      TARGETS=linux_arm64 make release GIT_DIRTY= \
        GO_TAGS="ui codegen_generated release hashicorpmetrics"
    '
  cp "$source_dir/pkg/linux_arm64/nomad" "$staging_dir/nomad-arm64"

  log "Building linux/amd64"
  docker run --rm \
    --platform linux/amd64 \
    --volume "$source_dir:/tmp/nomad" \
    --workdir /tmp/nomad \
    --env "GO_VERSION=$go_version" \
    "$BUILD_IMAGE" \
    bash -euxo pipefail -c '
      dnf install -y git gcc gcc-c++ make tar gzip zip libatomic tree
      curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
      export PATH="/usr/local/go/bin:$PATH"
      TARGETS=linux_amd64 make release GIT_DIRTY= \
        GO_TAGS="ui codegen_generated release hashicorpmetrics"
    '
  cp "$source_dir/pkg/linux_amd64/nomad" "$staging_dir/nomad-amd64"
  chmod 0755 "$staging_dir/nomad-arm64" "$staging_dir/nomad-amd64"

  arm64_binary="$staging_dir/nomad-arm64"
  amd64_binary="$staging_dir/nomad-amd64"
  log "Validating raw binaries"
  validate_binary "$arm64_binary" arm64 "ARM aarch64" "$version" "$source_commit" "$build_date"
  validate_binary "$amd64_binary" amd64 "x86-64" "$version" "$source_commit" "$build_date"

  arm64_sha="$(sha256_file "$arm64_binary")"
  amd64_sha="$(sha256_file "$amd64_binary")"
  arm64_size="$(file_size "$arm64_binary")"
  amd64_size="$(file_size "$amd64_binary")"
  arm64_name="nomad-$version-arm64@$arm64_sha"
  amd64_name="nomad-$version-x86_64@$amd64_sha"

  log "Confirming the source branch did not advance during the build"
  latest_commit="$(refresh_source)"
  [[ "$latest_commit" == "$source_commit" ]] ||
    die "$SOURCE_REMOTE/$SOURCE_BRANCH advanced to $latest_commit; rebuild required"

  mkdir -p "$OUTPUT_ROOT"
  artifact_dir="$(mktemp -d "$OUTPUT_ROOT/${source_commit:0:12}.XXXXXX")"
  install -m 0755 "$amd64_binary" "$artifact_dir/$amd64_name"
  install -m 0755 "$arm64_binary" "$artifact_dir/$arm64_name"

  {
    printf '%s  %s\n' "$amd64_sha" "$amd64_name"
    printf '%s  %s\n' "$arm64_sha" "$arm64_name"
  } >"$artifact_dir/SHA256SUMS"
  {
    printf 'format_version\t1\n'
    printf 'remote\t%s\n' "$SOURCE_REMOTE"
    printf 'branch\t%s\n' "$SOURCE_BRANCH"
    printf 'source_commit\t%s\n' "$source_commit"
    printf 'version\t%s\n' "$version"
    printf 'build_date\t%s\n' "$build_date"
    printf 'build_image\t%s\n' "$BUILD_IMAGE"
    printf 'artifact\tx86_64\t%s\t%s\t%s\n' "$amd64_name" "$amd64_sha" "$amd64_size"
    printf 'artifact\tarm64\t%s\t%s\t%s\n' "$arm64_name" "$arm64_sha" "$arm64_size"
  } >"$artifact_dir/manifest.tsv"
  {
    printf "NOMAD_X86_64_S3_PATH='%s'\n" "s3://hive-ci-binaries/nomad/$amd64_name"
    printf "NOMAD_X86_64_EXPECTED_HASH='%s'\n" "$amd64_sha"
    printf "NOMAD_ARM64_S3_PATH='%s'\n" "s3://hive-ci-binaries/nomad/$arm64_name"
    printf "NOMAD_ARM64_EXPECTED_HASH='%s'\n" "$arm64_sha"
  } >"$artifact_dir/HIVE_ARTIFACTS.env"

  pointer_tmp="$OUTPUT_ROOT/LAST_BUILD.tmp.$$"
  printf '%s\n' "$artifact_dir" >"$pointer_tmp"
  mv "$pointer_tmp" "$OUTPUT_ROOT/LAST_BUILD"

  log "Build complete: $artifact_dir"
  printf 'x86_64  sha256:%s  %s bytes  %s\n' "$amd64_sha" "$amd64_size" "$amd64_name"
  printf 'arm64   sha256:%s  %s bytes  %s\n' "$arm64_sha" "$arm64_size" "$arm64_name"
  printf '\nUpload these exact artifacts with:\n  %s/upload.sh\n' "$REPO_DIR"
}

main "$@"
