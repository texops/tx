#!/usr/bin/env bash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
ROOT_DIR="$SCRIPT_DIR/.."

cd "$ROOT_DIR"

make man-text

if ! git diff --stat --exit-code man/tx.1.txt; then
  echo ""
  echo "man/tx.1.txt is out of date."
  echo "Please run 'make man-text' locally and commit the changes."
  exit 1
fi
