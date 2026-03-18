#!/bin/sh
# shellcheck disable=SC3043,SC3040
set -eu

detect_os() {
    local os
    os="$(uname -s)"
    case "${os}" in
        Darwin)          echo "darwin" ;;
        Linux)           echo "linux" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *)
            echo "Unsupported OS: ${os}" >&2
            exit 1
            ;;
    esac
}

detect_arch() {
    local arch
    arch="$(uname -m)"
    case "${arch}" in
        x86_64)
            if [ "$(uname -s)" = "Darwin" ]; then
                local translated
                translated="$(sysctl -n sysctl.proc_translated 2>/dev/null || echo "0")"
                if [ "${translated}" = "1" ]; then
                    echo "arm64"
                    return
                fi
            fi
            echo "amd64"
            ;;
        aarch64|arm64) echo "arm64" ;;
        *)
            echo "Unsupported architecture: ${arch}" >&2
            exit 1
            ;;
    esac
}

detect_latest_version() {
    local header tag
    header="$(curl -sSI https://github.com/texops/tx/releases/latest 2>/dev/null)" || true
    tag="$(echo "${header}" | grep -i '^location:' | sed 's|.*/tag/||' | tr -d '[:space:]')" || true
    if [ -z "${tag}" ]; then
        echo "Failed to detect latest version" >&2
        exit 1
    fi
    echo "${tag}"
}

TMPDIR_CLEANUP=""
cleanup() {
    if [ -n "${TMPDIR_CLEANUP}" ]; then
        rm -rf "${TMPDIR_CLEANUP}"
    fi
}

main() {
    local os arch tag version ext binary url tmpdir

    # 1. Detect OS, architecture, and latest release version
    os="$(detect_os)"
    arch="$(detect_arch)"
    tag="$(detect_latest_version)"
    version="${tag#v}"

    # 2. Build download URL
    if [ "${os}" = "windows" ]; then
        ext="zip"
        binary="tx.exe"
    else
        ext="tar.gz"
        binary="tx"
    fi
    url="https://github.com/texops/tx/releases/download/${tag}/tx_${version}_${os}_${arch}.${ext}"

    # 3. Download archive to temp directory
    tmpdir="$(mktemp -d)"
    TMPDIR_CLEANUP="${tmpdir}"
    trap cleanup EXIT

    echo "Downloading tx ${tag} (${os}/${arch})..."
    curl -fsSL "${url}" -o "${tmpdir}/tx_archive"

    # 4. Extract and place binary in current directory
    if [ "${ext}" = "zip" ]; then
        unzip -q -o "${tmpdir}/tx_archive" "${binary}" -d "${tmpdir}"
    else
        tar -xzf "${tmpdir}/tx_archive" -C "${tmpdir}" "${binary}"
    fi

    if [ -d "./${binary}" ]; then
        echo "Error: ./${binary} exists as a directory" >&2
        exit 1
    fi
    mv "${tmpdir}/${binary}" "./${binary}"
    chmod +x "./${binary}"

    echo "Downloaded ${binary} ${tag} to current directory"
}

main
