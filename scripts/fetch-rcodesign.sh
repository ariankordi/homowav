#!/usr/bin/env bash
# Downloads rcodesign (indygreg/apple-platform-rs, the apple-codesign crate)
# for this Linux host and verifies its SHA-256. Idempotent: if it is already
# extracted at the destination, does nothing and just prints its path.
#
# The release build uses it to ad-hoc sign the macOS .app on hosts without
# Apple's codesign. Ad-hoc means no certificate: the signature only records
# hashes of the bundle's files, which macOS insists on for Apple Silicon code
# and for the Info.plist to count as part of the app.
#
# The hashes are the .sha256 files upstream publishes next to each archive.
#
# Usage: fetch-rcodesign.sh <dest-dir>
#   Prints the path of the rcodesign executable to stdout on success.

set -euo pipefail

readonly RCODESIGN_VERSION="0.29.0"
readonly RELEASE_URL="https://github.com/indygreg/apple-platform-rs/releases/download/apple-codesign%2F${RCODESIGN_VERSION}"

if [[ $# -ne 1 ]]; then
    echo "Usage: $0 <dest-dir>" >&2
    exit 1
fi
readonly DEST_DIR="$1"

case "$(uname -s)-$(uname -m)" in
    Linux-x86_64)  platform="x86_64-unknown-linux-musl";  sha256="dbe85cedd8ee4217b64e9a0e4c2aef92ab8bcaaa41f20bde99781ff02e600002" ;;
    Linux-aarch64) platform="aarch64-unknown-linux-musl"; sha256="4af92c87ddf52f5f2d1258a3b4e56c7dcb8f1b2468df744976c5f139e031961f" ;;
    *)
        echo "error: no pinned rcodesign for $(uname -s)-$(uname -m) (on macOS, use Apple's codesign)" >&2
        exit 1
        ;;
esac

readonly NAME="apple-codesign-${RCODESIGN_VERSION}-${platform}"
readonly EXECUTABLE="${DEST_DIR}/${NAME}/rcodesign"

if [[ -x "${EXECUTABLE}" ]]; then
    echo "${EXECUTABLE}"
    exit 0
fi

mkdir -p "${DEST_DIR}"
archive="$(mktemp "${DEST_DIR}/rcodesign-download.XXXXXX")"
trap 'rm -f "${archive}"' EXIT

echo "Fetching rcodesign ${RCODESIGN_VERSION} (${platform}) to ${DEST_DIR}..." >&2
curl -fsSL -o "${archive}" "${RELEASE_URL}/${NAME}.tar.gz"

actual="$(sha256sum "${archive}" | cut -d' ' -f1)"
if [[ "${actual}" != "${sha256}" ]]; then
    echo "error: rcodesign archive SHA-256 mismatch (expected ${sha256}, got ${actual})" >&2
    exit 1
fi

tar -xzf "${archive}" -C "${DEST_DIR}"
if [[ ! -x "${EXECUTABLE}" ]]; then
    echo "error: ${EXECUTABLE} is missing from the archive" >&2
    exit 1
fi
echo "${EXECUTABLE}"
