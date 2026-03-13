#!/usr/bin/env bash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
ROOT_DIR="$SCRIPT_DIR/.."

cd "$ROOT_DIR"

make fmt

if ! git diff --stat --exit-code; then
  echo ""
  echo "The files listed above are not formatted correctly."
  echo "Please run 'make fmt' locally and commit the changes."
  exit 1
fi
