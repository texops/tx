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

find_install_dir() {
    case ":${PATH}:" in
        *":${HOME}/.local/bin:"*)
            echo "${HOME}/.local/bin"
            ;;
        *":${HOME}/bin:"*)
            echo "${HOME}/bin"
            ;;
        *)
            echo "${HOME}/.local/bin"
            ;;
    esac
}

print_path_instructions() {
    local install_dir="$1"
    local shell_name

    echo ""
    echo "${install_dir} is not in your PATH."
    echo "Add it by running:"
    echo ""

    shell_name="$(basename "${SHELL:-}")"
    case "${shell_name}" in
        bash)
            echo "  echo 'export PATH=\"${install_dir}:\$PATH\"' >> ~/.bashrc"
            echo "  source ~/.bashrc"
            ;;
        zsh)
            echo "  echo 'export PATH=\"${install_dir}:\$PATH\"' >> ~/.zshrc"
            echo "  source ~/.zshrc"
            ;;
        fish)
            echo "  set -U fish_user_paths ${install_dir} \$fish_user_paths"
            ;;
        *)
            echo "  export PATH=\"${install_dir}:\$PATH\""
            ;;
    esac
}

TMPDIR_CLEANUP=""
cleanup() {
    if [ -n "${TMPDIR_CLEANUP}" ]; then
        rm -rf "${TMPDIR_CLEANUP}"
    fi
}

main() {
    local os arch tag version ext binary url tmpdir install_dir in_path

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

    # 4. Extract binary from archive
    if [ "${ext}" = "zip" ]; then
        unzip -q -o "${tmpdir}/tx_archive" "${binary}" -d "${tmpdir}"
    else
        tar -xzf "${tmpdir}/tx_archive" -C "${tmpdir}" "${binary}"
    fi

    # 5. Install binary to ~/.local/bin (or ~/bin)
    install_dir="$(find_install_dir)"
    mkdir -p "${install_dir}"
    if [ -d "${install_dir}/${binary}" ]; then
        echo "Error: ${install_dir}/${binary} exists as a directory" >&2
        exit 1
    fi
    mv "${tmpdir}/${binary}" "${install_dir}/${binary}"
    chmod +x "${install_dir}/${binary}"

    echo "Installed ${binary} ${tag} to ${install_dir}/${binary}"

    # 6. Warn if install directory is not in PATH
    in_path=true
    case ":${PATH}:" in
        *":${install_dir}:"*) ;;
        *) in_path=false ;;
    esac

    if [ "${in_path}" = false ]; then
        print_path_instructions "${install_dir}"
    fi
}

main
