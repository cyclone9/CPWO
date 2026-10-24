#!/usr/bin/env bash
# Purpose: show what cpwo is built from and whether any of it has a newer
#          release, and regenerate licenses.md.
# Usage:   scripts/deps.sh            current and latest version of every Go
#                                     module, of Go and of each GitHub Action
#          scripts/deps.sh --apply    the same, then update the outdated
#                                     modules, go mod tidy and go test ./...
#          scripts/deps.sh licenses   rewrite licenses.md from what
#                                     a release build links
# Deps:    bash, curl, awk, sed, sort -V. Go for --apply, licenses and the
#          bundled C library versions. GO=/path/to/go if it is not on PATH.
#          GITHUB_TOKEN, if set, lifts GitHub's limit of 60 anonymous calls
#          an hour.
set -euo pipefail
cd "$(dirname "$0")/.."
GO=${GO:-go}

need() { command -v "$1" >/dev/null 2>&1 || { echo "deps.sh: needs $1" >&2; exit 2; }; }

# fetch prints a URL's body, or nothing when offline or refused.
fetch() { curl -fsSL --max-time 20 "$@" 2>/dev/null || true; }

# newer A B succeeds when version B is higher than version A.
newer() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n1)" = "$2" ]; }

# requires prints "module version direct|indirect" for each go.mod requirement.
requires() {
  awk '
    /^require[ \t]*\(/        { block = 1; next }
    block && /^\)/            { block = 0; next }
    block && NF >= 2          { print $1, $2, (/\/\/ indirect/ ? "indirect" : "direct"); next }
    /^require[ \t]+[^( \t]/   { print $2, $3, (/\/\/ indirect/ ? "indirect" : "direct") }
  ' go.mod
}

# The module proxy spells an upper-case letter as ! and the lower-case one.
proxy_path() {
  printf '%s' "$1" | awk '{ s = ""; for (i = 1; i <= length($0); i++) { c = substr($0, i, 1); s = s (c ~ /[A-Z]/ ? "!" tolower(c) : c) } print s }'
}

latest_module() { fetch "https://proxy.golang.org/$(proxy_path "$1")/@latest" | sed -n 's/.*"Version":"\([^"]*\)".*/\1/p'; }

repo_of() {
  case $1 in
    github.com/*)   printf 'https://%s\n' "$(printf '%s' "$1" | cut -d/ -f1-3)" ;;
    golang.org/x/*) printf 'https://github.com/golang/%s\n' "$(printf '%s' "${1#golang.org/x/}" | cut -d/ -f1)" ;;
    *)              printf 'https://pkg.go.dev/%s\n' "$1" ;;
  esac
}

row() { printf '  %-30s %-9s %-10s %-10s %-17s %s\n' "$@"; }

check() {
  local apply=$1 mod cur kind lat status dir name v action ref major want newest have
  local outdated=()
  need curl

  printf 'Go modules (go.mod)\n'
  row module kind current latest status repository
  while read -r mod cur kind; do
    lat=$(latest_module "$mod")
    if [ -z "$lat" ]; then status="unknown, offline?"
    elif newer "$cur" "$lat"; then status="update available"; outdated+=("$mod")
    else status="up to date"; fi
    row "$mod" "$kind" "$cur" "${lat:--}" "$status" "$(repo_of "$mod")"
  done < <(requires)

  if command -v "$GO" >/dev/null 2>&1; then
    printf '\nC libraries compiled into the encoders (updated by updating the module)\n'
    for mod in github.com/gen2brain/webp github.com/gen2brain/avif; do
      dir=$("$GO" list -m -f '{{.Dir}}' "$mod" 2>/dev/null || true)
      [ -n "$dir" ] && [ -f "$dir/lib/Makefile" ] || continue
      sed -n 's/^LIB\([A-Z0-9_]*\)_VERSION *= *\([^ ]*\).*/\1 \2/p' "$dir/lib/Makefile" | while read -r name v; do
        case ${name#AVIF_} in
          WEBP) name=libwebp ;; AVIF) name=libavif ;; AOM) name=aom ;;
          YUV) name=libyuv ;; DAV1D) name=dav1d ;; *) name=$(printf '%s' "$name" | tr 'A-Z' 'a-z') ;;
        esac
        printf '  %-30s %-10s in %s\n' "$name" "$v" "$mod"
      done
    done
  fi

  printf '\nGo toolchain\n'
  want=$(sed -n 's/^go \([0-9.]*\).*/\1/p' go.mod)
  newest=$(fetch 'https://go.dev/dl/?mode=json' | sed -n 's/.*"version": *"go\([0-9.]*\)".*/\1/p' | head -n1)
  have=$("$GO" env GOVERSION 2>/dev/null | sed 's/^go//' || true)
  printf '  go.mod asks for %s or newer, the newest release is %s, installed here: %s\n' \
    "$want" "${newest:-unknown}" "${have:-none}"
  if [ -n "$have" ] && [ -n "$newest" ] && newer "$have" "$newest"; then
    printf '  a newer Go is out: https://go.dev/dl/\n'
  fi

  printf '\nGitHub Actions (.github/workflows)\n'
  row action "" pinned latest status ""
  local auth=()
  [ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
  grep -h -o 'uses: *[^ ]*@[^ ]*' .github/workflows/*.yml | sed 's/uses: *//' | sort -u |
    while IFS=@ read -r action ref; do
      lat=$(fetch ${auth[@]+"${auth[@]}"} "https://api.github.com/repos/$action/releases/latest" |
        sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
      major=${lat%%.*}
      if [ -z "$lat" ]; then status="unknown, offline?"
      elif newer "$ref" "$major"; then status="update available"
      else status="up to date"; fi
      row "$action" "" "$ref" "${lat:--}" "$status" "https://github.com/$action"
    done

  [ "$apply" = 1 ] || return 0
  if [ ${#outdated[@]} -eq 0 ]; then
    printf '\nNo module to update.\n'
    return 0
  fi
  need "$GO"
  printf '\nUpdating %s\n' "${outdated[*]}"
  for mod in "${outdated[@]}"; do "$GO" get "$mod@latest"; done
  "$GO" mod tidy
  "$GO" test ./...
  printf 'Updated and tested. Run scripts/deps.sh licenses too, so the notices match.\n'
}

# notice NAME VERSION URL DIR prints one section: every licence file in DIR,
# and the licences of the C libraries an encoder module bundles in DIR/lib.
notice() {
  local f
  printf '\n## %s%s\n\n%s\n' "$1" "${2:+ $2}" "$3"
  for f in "$4"/LICENSE* "$4"/NOTICE* "$4"/PATENTS* "$4"/lib/LICENSE.*; do
    [ -f "$f" ] || continue
    printf '\n%s:\n\n```text\n' "${f#"$4"/}"
    cat "$f"
    [ -z "$(tail -c1 "$f")" ] || printf '\n'
    printf '```\n'
  done
}

licenses() {
  local mod ver out=licenses.md
  need "$GO"
  tmp=$(mktemp -d) # global: the EXIT trap runs after this function returns
  trap 'rm -rf "$tmp"' EXIT
  # nodynamic, like a release: the notices cover exactly what ships
  CGO_ENABLED=0 "$GO" build -trimpath -tags nodynamic -o "$tmp/cpwo" .
  {
    printf '# License\n\n'
    printf 'CPWO is MIT licensed, see LICENSE. The `cpwo` binary also contains the code below, each part\n'
    printf 'under its own license, reproduced here in full. `cpwo -license` lists them in short.\n'
    notice "Go standard library and runtime" "" "https://go.dev" "$("$GO" env GOROOT)"
    "$GO" version -m "$tmp/cpwo" | awk '$1 == "dep" { print $2, $3 }' | while read -r mod ver; do
      notice "$mod" "$ver" "$(repo_of "$mod")" "$("$GO" list -m -f '{{.Dir}}' "$mod")"
    done
    # C libraries a module compiles in without shipping their license; the
    # texts are kept in scripts/licenses/, one <library>.txt each
    for f in scripts/licenses/*.txt; do
      [ -f "$f" ] || continue
      printf '\n## %s\n\nCompiled into github.com/gen2brain/avif, which does not ship its license.\n' "$(basename "$f" .txt)"
      printf '\nLICENSE:\n\n```text\n'
      cat "$f"
      [ -z "$(tail -c1 "$f")" ] || printf '\n'
      printf '```\n'
    done
  } >"$out"
  echo "wrote $out"
}

case ${1:-} in
  "")       check 0 ;;
  --apply)  check 1 ;;
  licenses) licenses ;;
  *)        sed -n 's/^# \{0,1\}//; 2,13p' "$0" >&2; exit 2 ;;
esac
