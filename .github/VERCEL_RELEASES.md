# Vercel Nomad releases

Pushes to `vercel/main-2.0.5` build Linux amd64 and arm64 binaries through
[`vercel-release.yml`](workflows/vercel-release.yml). Public release assets are
`nomad-VERSION-amd64.tar` and `nomad-VERSION-arm64.tar`, replacing the previous
ZIPs and unsigned release checksum file. ZIPs remain internal artifacts between
the build and release jobs.

Each tar contains:

- `nomad`: the executable, with mode 755.
- `LICENSE.txt`: the license distributed by the existing release build.
- `release.json`: the version (without `v`), OS, architecture, and full source commit.
- `manifest.sha256`: SHA-256 checksums of those three files.
- `manifest.sha256.sigstore.json`: the Sigstore bundle signing the manifest.

The release job uses GitHub Actions OIDC for keyless signing with Cosign. No
long-lived signing key is stored. It verifies its signature and checksums before
packaging. Signing uses the same manifest-and-bundle approach as Hive's kernel
cache; the outer tar is transport packaging, not the signed subject.

## Verify a release

Supply the intended version, architecture, and reviewed source commit. Do not
derive the expected commit from the downloaded archive itself. Extract only
these named members to fixed files in a temporary directory; do not execute a
downloaded binary before verification.

```bash
version='2.0.5+vercelN' # Replace with the intended release version.
arch='amd64' # Or arm64.
commit='<expected full source commit>'
archive="$PWD/nomad-${version}-${arch}.tar"
verified=$(mktemp -d) || exit 1
for member in nomad LICENSE.txt release.json manifest.sha256 manifest.sha256.sigstore.json; do
  tar -xOf "$archive" "$member" > "$verified/$member" || exit 1
done
cd "$verified" || exit 1

cosign verify-blob --bundle manifest.sha256.sigstore.json \
  --certificate-identity 'https://github.com/vercel/nomad/.github/workflows/vercel-release.yml@refs/heads/vercel/main-2.0.5' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-repository vercel/nomad \
  --certificate-github-workflow-trigger push \
  --certificate-github-workflow-sha "$commit" manifest.sha256 || exit 1

# Require exactly the expected checksum entries before interpreting their paths.
test "$(wc -l < manifest.sha256 | tr -d ' ')" = 3 || exit 1
for member in nomad LICENSE.txt release.json; do
  test "$(grep -Ec "^[0-9a-f]{64}  ${member//./\\.}$" manifest.sha256)" = 1 || exit 1
done
sha256sum --check manifest.sha256 || exit 1
jq -e --arg version "$version" --arg arch "$arch" --arg commit "$commit" \
  '. == {version: $version, os: "linux", arch: $arch, commit: $commit}' release.json || exit 1
chmod 755 nomad
```

Consumers can mirror the tar unchanged to S3 and repeat verification after
downloading it. Hive's importer must switch from ZIPs and `SHA256SUMS` to this
format before importing these releases. Keep Hive's separately reviewed raw
binary SHA-256 pins when selecting AMI inputs.

Verification requires the exact release workflow, protected source branch,
GitHub issuer, push event, and expected source commit. The source branch must
require approved PRs and restrict direct pushes and bypasses. Those repository
settings are external to this workflow. Signing does not protect against a
compromised authorized build or signing job.
