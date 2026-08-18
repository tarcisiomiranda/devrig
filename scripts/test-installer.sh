#!/usr/bin/env bash
# Drive install.sh against a fake release directory (no network).

set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPORARY_DIRECTORY="$(mktemp -d)"

cleanup() {
	rm -rf "$TEMPORARY_DIRECTORY"
}
trap cleanup EXIT

case "$(uname -s)" in
	Linux) target_os=linux ;;
	Darwin) target_os=darwin ;;
	*)
		printf 'Unsupported test operating system\n' >&2
		exit 1
		;;
esac

case "$(uname -m)" in
	x86_64 | amd64) target_arch=amd64 ;;
	aarch64 | arm64) target_arch=arm64 ;;
	*)
		printf 'Unsupported test architecture\n' >&2
		exit 1
		;;
esac

asset="devrig_${target_os}_${target_arch}"
release_directory="${TEMPORARY_DIRECTORY}/release"
mock_directory="${TEMPORARY_DIRECTORY}/mock-bin"
install_directory="${TEMPORARY_DIRECTORY}/install"
mkdir -p "$release_directory" "$mock_directory"

cat > "${release_directory}/${asset}" <<'FAKE_BINARY'
#!/bin/sh
if [ "${1:-}" = "version" ]; then
	printf 'devrig 0.0.0-test\n'
	exit 0
fi
printf 'fake devrig\n'
FAKE_BINARY
chmod 0755 "${release_directory}/${asset}"

if command -v sha256sum >/dev/null 2>&1; then
	(
		cd "$release_directory"
		sha256sum "$asset" > checksums.txt
	)
else
	checksum=$(shasum -a 256 "${release_directory}/${asset}")
	printf '%s  %s\n' "${checksum%% *}" "$asset" > "${release_directory}/checksums.txt"
fi

cat > "${mock_directory}/curl" <<'MOCK_CURL'
#!/bin/sh

url=
output=
while [ "$#" -gt 0 ]; do
	case "$1" in
		-o)
			output=$2
			shift 2
			;;
		https://* | http://*)
			url=$1
			shift
			;;
		*) shift ;;
	esac
done

[ -n "$url" ] && [ -n "$output" ]
case "$url" in
	*/skills/devrig/SKILL.md)
		cp "${DEVRIG_TEST_SKILL_FILE}" "$output"
		;;
	*)
		cp "${DEVRIG_TEST_RELEASE_DIRECTORY}/${url##*/}" "$output"
		;;
esac
MOCK_CURL
chmod 0755 "${mock_directory}/curl"

skill_file="${TEMPORARY_DIRECTORY}/SKILL.md"
cat > "$skill_file" <<'SKILL'
---
name: devrig
description: test skill
---
# test
SKILL

# Default: skills off
DEVRIG_TEST_RELEASE_DIRECTORY="$release_directory" \
	DEVRIG_TEST_SKILL_FILE="$skill_file" \
	DEVRIG_INSTALL_DIR="$install_directory" \
	DEVRIG_VERSION=v0.0.0 \
	DEVRIG_BASE_URL="https://example.test/releases" \
	DEVRIG_COMPAT_TESTPG=0 \
	PATH="${mock_directory}:${PATH}" \
	bash "${PROJECT_ROOT}/install.sh"

installed_binary="${install_directory}/devrig"
if [ ! -x "$installed_binary" ]; then
	printf 'Installer did not create an executable binary\n' >&2
	exit 1
fi
"$installed_binary" version

# With DEVRIG_INSTALL_SKILLS=1 and a fake agent home
fake_home="${TEMPORARY_DIRECTORY}/home"
mkdir -p "${fake_home}/.claude"
install_directory_skills="${TEMPORARY_DIRECTORY}/install-skills"
mkdir -p "$install_directory_skills"
DEVRIG_TEST_RELEASE_DIRECTORY="$release_directory" \
	DEVRIG_TEST_SKILL_FILE="$skill_file" \
	DEVRIG_INSTALL_DIR="$install_directory_skills" \
	DEVRIG_VERSION=v0.0.0 \
	DEVRIG_BASE_URL="https://example.test/releases" \
	DEVRIG_INSTALL_SKILLS=1 \
	DEVRIG_COMPAT_TESTPG=0 \
	HOME="$fake_home" \
	PATH="${mock_directory}:${PATH}" \
	bash "${PROJECT_ROOT}/install.sh"

skill_dest="${fake_home}/.claude/skills/devrig/SKILL.md"
if [ ! -f "$skill_dest" ]; then
	printf 'DEVRIG_INSTALL_SKILLS=1 did not install skill to %s\n' "$skill_dest" >&2
	exit 1
fi
if ! grep -q 'name: devrig' "$skill_dest"; then
	printf 'Installed skill content looks wrong\n' >&2
	exit 1
fi

# Explicitly off: no skill even if agent home exists
fake_home_off="${TEMPORARY_DIRECTORY}/home-off"
mkdir -p "${fake_home_off}/.claude"
install_directory_off="${TEMPORARY_DIRECTORY}/install-off"
mkdir -p "$install_directory_off"
DEVRIG_TEST_RELEASE_DIRECTORY="$release_directory" \
	DEVRIG_TEST_SKILL_FILE="$skill_file" \
	DEVRIG_INSTALL_DIR="$install_directory_off" \
	DEVRIG_VERSION=v0.0.0 \
	DEVRIG_BASE_URL="https://example.test/releases" \
	DEVRIG_INSTALL_SKILLS=0 \
	DEVRIG_COMPAT_TESTPG=0 \
	HOME="$fake_home_off" \
	PATH="${mock_directory}:${PATH}" \
	bash "${PROJECT_ROOT}/install.sh"

if [ -f "${fake_home_off}/.claude/skills/devrig/SKILL.md" ]; then
	printf 'DEVRIG_INSTALL_SKILLS=0 should not install skills\n' >&2
	exit 1
fi

printf 'Installer integration test passed\n'
