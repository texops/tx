#!/usr/bin/env bash

set -e

BIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." &>/dev/null && pwd)/bin"

for path in "$@"; do
  if [ -d "$path" ]; then
    # gofumpt can format entire directories, but it skips auto-generated files.
    find "$path" -type f -name '*.go' -print0 | xargs -0 "$BIN_DIR/gofumpt" -l -w
  else
    "$BIN_DIR/gofumpt" -l -w "$path"
  fi
done

"$BIN_DIR/gci" write "$@" -s standard -s 'prefix(github.com/texops/tx)' -s default -s blank -s dot
