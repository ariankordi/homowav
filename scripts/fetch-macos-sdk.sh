#!/usr/bin/env bash
# From rl-mii-renderer (scripts/fetch-macos-sdk.sh), by the same author.
# Downloads and extracts the headers-only macOS 12.0 SDK used by zig cc to
# cross-compile for macOS from a non-macOS/non-Xcode host. Idempotent: if the
# sysroot already exists at the destination, does nothing and just prints its
# path. Deliberately kept out of CMakeLists.txt -- this is a one-time fetch,
# not a build step.
#
# Source: https://github.com/hexops-graveyard/sdk-macos-12.0 (27MB zip, no
# git history). Pinned to a specific commit so the SDK contents never change
# out from under a build without this script being updated deliberately.
#
# Usage: fetch-macos-sdk.sh <dest-dir>
#   Prints the sysroot path (<dest-dir>/root, suitable for CMAKE_OSX_SYSROOT)
#   to stdout on success.

set -euo pipefail

readonly SDK_COMMIT="14613b4917c7059dad8f3789f55bb13a2548f83d"
readonly SDK_URL="https://github.com/hexops-graveyard/sdk-macos-12.0/archive/${SDK_COMMIT}.zip"
readonly SDK_ARCHIVE_DIR_NAME="sdk-macos-12.0-${SDK_COMMIT}"

if [[ $# -ne 1 ]]; then
    echo "Usage: $0 <dest-dir>" >&2
    exit 1
fi

readonly DEST_DIR="$1"
readonly SYSROOT="${DEST_DIR}/root"

if [[ -d "${SYSROOT}" ]]; then
    echo "${SYSROOT}"
    exit 0
fi

mkdir -p "${DEST_DIR}"
tmp_zip="$(mktemp "${DEST_DIR}/sdk-download.XXXXXX.zip")"
trap 'rm -f "${tmp_zip}"' EXIT

echo "Fetching macOS SDK (${SDK_COMMIT:0:7}) to ${DEST_DIR}..." >&2
curl -fsSL -o "${tmp_zip}" "${SDK_URL}"

echo "Extracting sysroot..." >&2
# Only the root/ subtree is the actual SDK content (headers + .tbd stubs);
# skip README/LICENSE/update.sh etc.
unzip -q "${tmp_zip}" "${SDK_ARCHIVE_DIR_NAME}/root/*" -d "${DEST_DIR}"
mv "${DEST_DIR}/${SDK_ARCHIVE_DIR_NAME}/root" "${SYSROOT}"
rmdir "${DEST_DIR}/${SDK_ARCHIVE_DIR_NAME}"

echo "${SYSROOT}"
