#!/usr/bin/env bash

set -e

CODE_DIRS="cmd internal"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
ROOT_DIR="$SCRIPT_DIR/.."

cd "$ROOT_DIR"

export GOBIN="$ROOT_DIR/bin"

go install github.com/daixiang0/gci@v0.14.0
go install mvdan.cc/gofumpt@v0.9.2

# shellcheck disable=SC2086 # we do want word splitting here to pass CODE_DIRS as multiple args
"$SCRIPT_DIR"/format-go.sh $CODE_DIRS

# Sometimes gofumpt needs to be called twice to ensure stable formatting
# shellcheck disable=SC2086
"$SCRIPT_DIR"/format-go.sh $CODE_DIRS
