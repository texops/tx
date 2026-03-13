#!/bin/sh
set -eu

VERSION="2.11.3"
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DIR="${ROOT_DIR}/bin"
BINARY="${BIN_DIR}/golangci-lint"

if [ -x "${BINARY}" ]; then
    echo "golangci-lint already installed"
    exit 0
fi

OS="$(uname -s)"
ARCH="$(uname -m)"

case "${OS}" in
    Linux)  OS="linux" ;;
    Darwin) OS="darwin" ;;
    *)
        echo "Unsupported OS: ${OS}" >&2
        exit 1
        ;;
esac

case "${ARCH}" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
    *)
        echo "Unsupported architecture: ${ARCH}" >&2
        exit 1
        ;;
esac

TARBALL="golangci-lint-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/golangci/golangci-lint/releases/download/v${VERSION}/${TARBALL}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

echo "Downloading golangci-lint v${VERSION} (${OS}/${ARCH})..."
if command -v curl >/dev/null 2>&1; then
    curl -fsSL "${URL}" -o "${TMPDIR}/${TARBALL}"
elif command -v wget >/dev/null 2>&1; then
    wget -q -O "${TMPDIR}/${TARBALL}" "${URL}"
else
    echo "Neither curl nor wget found" >&2
    exit 1
fi

tar -xzf "${TMPDIR}/${TARBALL}" -C "${TMPDIR}"

mkdir -p "${BIN_DIR}"
mv "${TMPDIR}/golangci-lint-${VERSION}-${OS}-${ARCH}/golangci-lint" "${BINARY}"
chmod +x "${BINARY}"

"${BINARY}" --version
