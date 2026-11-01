#!/usr/bin/env bash
# From rl-mii-renderer (scripts/fetch-zig.sh), by the same author.
# Downloads the pinned zig release for this host and verifies its SHA-256.
# Idempotent: if that zig is already extracted at the destination, does
# nothing and just prints its directory.
#
# zig is pinned to 0.15.2, not whatever is on PATH. 0.16.0 has two confirmed
# problems for this project: an LTO bug
# (codeberg.org/ziglang/zig/issues/31958) and a segfault in zig cc itself when
# linking a large shared library. Hashes come from
# https://ziglang.org/download/index.json.
#
# TODO(arian): investigate a less hardcoded way to acquire zig. This script
# keeps its own per-OS table of hosts, archive formats and hashes; something
# that discovers the host and resolves a pinned version generically (or comes
# from an existing project) would be shorter. tigerbeetle's zig/download.sh
# (Apache-2.0, github.com/tigerbeetle/tigerbeetle) was considered and has the
# same shape of table.
#
# Usage: fetch-zig.sh <dest-dir>
#   Prints the directory containing the zig executable to stdout on success.
#   Works from Linux, macOS, and the Windows bash environments (Git Bash,
#   MSYS2, Cygwin).

set -euo pipefail

readonly ZIG_VERSION="0.15.2"

if [[ $# -ne 1 ]]; then
    echo "Usage: $0 <dest-dir>" >&2
    exit 1
fi

readonly DEST_DIR="$1"

# Maps this host to zig's own "<arch>-<os>" naming, the release's archive
# format and its hash. Windows is detected through the OS variable that
# Windows itself sets to "Windows_NT" (inherited by Git Bash, MSYS2 and
# Cygwin), rather than by matching each environment's uname string.
host_os="$(uname -s)"
host_arch="$(uname -m)"
[[ "${OS:-}" == "Windows_NT" ]] && host_os="Windows"
zig_ext="tar.xz"
zig_exe="zig"
case "${host_os}-${host_arch}" in
    Darwin-arm64)   zig_host="aarch64-macos"; zig_sha256="3cc2bab367e185cdfb27501c4b30b1b0653c28d9f73df8dc91488e66ece5fa6b" ;;
    Darwin-x86_64)  zig_host="x86_64-macos";  zig_sha256="375b6909fc1495d16fc2c7db9538f707456bfc3373b14ee83fdd3e22b3d43f7f" ;;
    Linux-x86_64)   zig_host="x86_64-linux";  zig_sha256="02aa270f183da276e5b5920b1dac44a63f1a49e55050ebde3aecc9eb82f93239" ;;
    Linux-aarch64)  zig_host="aarch64-linux"; zig_sha256="958ed7d1e00d0ea76590d27666efbf7a932281b3d7ba0c6b01b0ff26498f667f" ;;
    Windows-x86_64)
        zig_host="x86_64-windows";  zig_ext="zip"; zig_exe="zig.exe"
        zig_sha256="3a0ed1e8799a2f8ce2a6e6290a9ff22e6906f8227865911fb7ddedc3cc14cb0c" ;;
    Windows-aarch64|Windows-arm64)
        zig_host="aarch64-windows"; zig_ext="zip"; zig_exe="zig.exe"
        zig_sha256="b926465f8872bf983422257cd9ec248bb2b270996fbe8d57872cca13b56fc370" ;;
    *)
        echo "error: no pinned zig ${ZIG_VERSION} for host ${host_os}-${host_arch}" >&2
        exit 1
        ;;
esac

readonly ZIG_NAME="zig-${zig_host}-${ZIG_VERSION}"
readonly ZIG_DIR="${DEST_DIR}/${ZIG_NAME}"
readonly ZIG_URL="https://ziglang.org/download/${ZIG_VERSION}/${ZIG_NAME}.${zig_ext}"

if [[ -x "${ZIG_DIR}/${zig_exe}" ]]; then
    echo "${ZIG_DIR}"
    exit 0
fi

# Hashes a file with whichever tool the host has (sha256sum on Linux,
# shasum on macOS).
sha256_of() {
    if command -v sha256sum > /dev/null; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

mkdir -p "${DEST_DIR}"
tmp_archive="$(mktemp "${DEST_DIR}/zig-download.XXXXXX")"
trap 'rm -f "${tmp_archive}"' EXIT

echo "Fetching zig ${ZIG_VERSION} (${zig_host}) to ${DEST_DIR}..." >&2
curl -fsSL -o "${tmp_archive}" "${ZIG_URL}"

actual_sha256="$(sha256_of "${tmp_archive}")"
if [[ "${actual_sha256}" != "${zig_sha256}" ]]; then
    echo "error: zig archive SHA-256 mismatch (expected ${zig_sha256}, got ${actual_sha256})" >&2
    exit 1
fi

# Extracts the downloaded archive into the destination. zip needs unzip, or
# bsdtar (which is what "tar" is on stock Windows 10+ and macOS).
extract_archive() {
    if [[ "${zig_ext}" == "zip" ]]; then
        if command -v unzip > /dev/null; then
            unzip -q "${tmp_archive}" -d "${DEST_DIR}"
        else
            tar -xf "${tmp_archive}" -C "${DEST_DIR}"
        fi
    else
        tar -xJf "${tmp_archive}" -C "${DEST_DIR}"
    fi
}
extract_archive
echo "${ZIG_DIR}"
