#!/usr/bin/env bash
# Install devrig: throwaway databases for integration tests.
#
#   curl -fsSL https://raw.githubusercontent.com/tarcisiomiranda/devrig/main/install.sh | bash
#
# Environment:
#   DEVRIG_VERSION         release tag (default: latest)
#   DEVRIG_BASE_URL        where release assets live (mirrors, air-gapped, testing)
#   DEVRIG_INSTALL_DIR     target dir (default: /usr/local/bin if writable, else ~/.local/bin)
#   DEVRIG_INSTALL_SKILLS  1|true|yes|on to also install the agent skill (default: off)
#   DEVRIG_COMPAT_TESTPG   0 to skip the `testpg` compatibility symlink (default: on)
#
# Linux and macOS only. Windows is not supported (use WSL2).
set -euo pipefail

REPO="${DEVRIG_REPO:-tarcisiomiranda/devrig}"
BASE_URL="${DEVRIG_BASE_URL:-https://github.com/${REPO}/releases/download}"
tmp=""
BINARY="devrig"
COMPAT_NAME="testpg"

info() { printf '  %s\n' "$*"; }
warn() { printf '  ! %s\n' "$*" >&2; }
die() {
    printf 'devrig install: %s\n' "$*" >&2
    exit 1
}

is_true() {
    case "${1:-}" in
    1 | true | TRUE | yes | YES | on | ON) return 0 ;;
    *) return 1 ;;
    esac
}

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

detect_platform() {
    local os arch
    os="$(uname -s)"
    arch="$(uname -m)"
    case "$os" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS: $os (Linux and macOS only; on Windows use WSL2)" ;;
    esac
    case "$arch" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "unsupported architecture: $arch" ;;
    esac
    printf '%s_%s' "$os" "$arch"
}

latest_version() {
    curl -fsSL -H 'Accept: application/vnd.github+json' \
        "https://api.github.com/repos/${REPO}/releases/latest" |
        sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1
}

install_dir() {
    if [ -n "${DEVRIG_INSTALL_DIR:-}" ]; then
        printf '%s' "$DEVRIG_INSTALL_DIR"
        return
    fi
    if [ -w /usr/local/bin ] 2>/dev/null; then
        printf '/usr/local/bin'
    else
        printf '%s/.local/bin' "$HOME"
    fi
}

verify_checksum() {
    local file="$1" sums="$2" name="$3" expected actual
    # awk, not grep: BSD grep (macOS) has no \| alternation in BRE, and with
    # `set -o pipefail` a non-matching grep kills the script with no message.
    # sha256sum/shasum print "<hash>  <name>" (or "*<name>" in binary mode).
    expected="$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1; exit }' "$sums" 2>/dev/null || true)"
    if [ -z "$expected" ]; then
        warn "no checksum published for ${name}; skipping verification"
        return 0
    fi
    if command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "$file" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "$file" | awk '{print $1}')"
    else
        warn "no sha256 tool found; skipping verification"
        return 0
    fi
    [ "$expected" = "$actual" ] || die "checksum mismatch for ${name} (expected ${expected}, got ${actual})"
    info "checksum ok"
}

main() {
    need curl
    need uname

    local platform version dir asset url
    platform="$(detect_platform)"
    version="${DEVRIG_VERSION:-$(latest_version)}"
    [ -n "$version" ] || die "could not resolve the latest release of ${REPO} (set DEVRIG_VERSION)"
    dir="$(install_dir)"
    asset="${BINARY}_${platform}"
    url="${BASE_URL}/${version}/${asset}"

    info "repo:     ${REPO}"
    info "version:  ${version}"
    info "platform: ${platform}"
    info "target:   ${dir}/${BINARY}"

    tmp="$(mktemp -d)"
    # Guarded: the EXIT trap may run after main()'s locals are out of scope,
    # and `set -u` would abort on a bare "$tmp".
    trap 'rm -rf "${tmp:-}"' EXIT

    curl -fsSL --retry 2 "$url" -o "${tmp}/${BINARY}" ||
        die "download failed: ${url}"
    if curl -fsSL --retry 2 "${BASE_URL}/${version}/checksums.txt" \
        -o "${tmp}/checksums.txt" 2>/dev/null; then
        verify_checksum "${tmp}/${BINARY}" "${tmp}/checksums.txt" "$asset"
    else
        warn "checksums.txt not available; skipping verification"
    fi

    chmod +x "${tmp}/${BINARY}"
    mkdir -p "$dir" 2>/dev/null ||
        die "cannot create ${dir} (set DEVRIG_INSTALL_DIR, or re-run with sudo)"
    if ! mv "${tmp}/${BINARY}" "${dir}/${BINARY}" 2>/dev/null; then
        die "cannot write to ${dir} (set DEVRIG_INSTALL_DIR, or re-run with sudo)"
    fi
    info "installed ${dir}/${BINARY}"

    # testpg was this tool's previous name; keep the old command working.
    if ! is_true "${DEVRIG_COMPAT_TESTPG:-1}"; then
        :
    elif [ -e "${dir}/${COMPAT_NAME}" ] && [ ! -L "${dir}/${COMPAT_NAME}" ]; then
        warn "${dir}/${COMPAT_NAME} exists and is not a symlink; leaving it alone"
    else
        ln -sf "${dir}/${BINARY}" "${dir}/${COMPAT_NAME}" &&
            info "linked ${dir}/${COMPAT_NAME} -> ${BINARY} (compatibility)"
    fi

    if is_true "${DEVRIG_INSTALL_SKILLS:-0}"; then
        install_skill
    fi

    case ":${PATH}:" in
    *":${dir}:"*) ;;
    *) warn "${dir} is not on your PATH — add it in your shell rc" ;;
    esac

    printf '\n'
    "${dir}/${BINARY}" version || true
    printf '\nDocker (or OrbStack/Colima) must be running. Try:\n'
    printf '  devrig up demo --db demo_test\n'
    # SC2016: the single quotes are the point — this prints a literal snippet
    # for the user to copy, it must not expand here.
    # shellcheck disable=SC2016
    printf '  export TEST_DATABASE_URL="$(devrig url demo)"\n'
    printf '  devrig down demo\n'
}

install_skill() {
    local skill_url="https://raw.githubusercontent.com/${REPO}/main/skills/devrig/SKILL.md"
    local installed=0 target
    for target in \
        "$HOME/.claude/skills/devrig" \
        "$HOME/.codex/skills/devrig" \
        "$HOME/.config/opencode/skills/devrig" \
        "$HOME/.cursor/skills/devrig" \
        "$HOME/.grok/skills/devrig" \
        "$HOME/.agents/skills/devrig"; do
        parent="$(dirname "$(dirname "$target")")"
        [ -d "$parent" ] || continue
        mkdir -p "$target"
        if curl -fsSL "$skill_url" -o "${target}/SKILL.md"; then
            info "skill installed: ${target}/SKILL.md"
            installed=$((installed + 1))
        fi
    done
    [ "$installed" -gt 0 ] || warn "no AI agent directories found; skill not installed"
}

main "$@"
