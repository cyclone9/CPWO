#!/usr/bin/env bash
# Purpose: cross-compile cpwo the way a release does, one folder per target:
#            bin/linux-amd64/cpwo  ...  bin/windows-arm64/cpwo.exe
#          plus bin/SHA256SUMS.
# Usage:   scripts/build.sh                    all six targets
#          scripts/build.sh linux/amd64 ...    only these
#          VERSION=1.2.3 scripts/build.sh      stamp this version (default: the
#                                              git tag, else main.go's version)
# Deps:    Go (GO=/path/to/go if it is not on PATH); `file`, when present, to
#          confirm the Linux binaries are static.
set -euo pipefail
cd "$(dirname "$0")/.."
GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || { echo "build.sh: Go not found, set GO=/path/to/go" >&2; exit 2; }

version=${VERSION:-$(git describe --tags --dirty 2>/dev/null | sed 's/^v//' || true)}
[ -n "$version" ] || version=$(sed -n 's/^var version = "\([^"]*\)".*/\1/p' main.go)

targets=("$@")
[ ${#targets[@]} -gt 0 ] || targets=(linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64)

for t in "${targets[@]}"; do
  os=${t%/*} arch=${t#*/}
  exe=cpwo
  [ "$os" = windows ] && exe=cpwo.exe
  out="bin/$os-$arch/$exe"
  mkdir -p "bin/$os-$arch"
  # Same flags as .goreleaser.yaml. nodynamic keeps the WebP/AVIF packages from
  # loading a system libwebp/libavif, and purego, which links libc, with them.
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 "$GO" build -trimpath -tags nodynamic \
    -ldflags "-s -w -X main.version=$version" -o "$out" .
  if [ "$os" = linux ] && command -v file >/dev/null 2>&1 && ! file "$out" | grep -q 'statically linked'; then
    echo "build.sh: $out is not statically linked" >&2
    exit 1
  fi
  printf '  %-14s %s\n' "$t" "$out"
done

# The binaries no longer carry the full license texts, so they travel beside
# them: in bin/ here and in the CI artifact, as in every release archive.
cp LICENSE licenses.md bin/

cd bin
if command -v sha256sum >/dev/null 2>&1; then sha256sum ./*/cpwo*; else shasum -a 256 ./*/cpwo*; fi >SHA256SUMS
echo "cpwo $version, checksums in bin/SHA256SUMS, licenses beside them"
