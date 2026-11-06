#!/usr/bin/env bash
# Builds one release download of hwclient, from a macOS or Linux host. CI
# runs it once per target; it runs the same way on a local machine.
#
# Windows and Linux are pure Go (no cgo), so they cross-compile with nothing
# but Go. macOS needs cgo, because the menu bar icon (fyne.io/systray) is
# Objective-C there. Those two slices are compiled with zig cc against a
# headers-only macOS SDK, merged into a universal binary and wrapped in an
# .app bundle. zig and the SDK are downloaded into a toolchains directory,
# never into system paths.
#
# Host tools expected: bash, go, npm, curl, python3, unzip, zip (and on
# macOS, codesign from the Xcode command line tools).
#
# Usage: build-release.sh <target> [out-dir]
#   target:  windows-x64, windows-arm64, linux-x86_64, linux-aarch64 or
#            macos-universal.
#   out-dir: defaults to <repo>/dist.
#
# File names leave out the version (hwclient-<target>[.exe|.zip]), so a
# link to releases/latest/download/<name> always gets the newest release.
# The version is still stamped inside the program and the bundle.
#
# Environment overrides:
#   VERSION         version to stamp (default: git describe, else "dev")
#   TOOLCHAINS_DIR  where zig and the macOS SDK go (default <repo>/toolchains)
#   SKIP_WEB=1      reuse an existing web/dist instead of rebuilding the UI

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
readonly TOOLCHAINS_DIR="${TOOLCHAINS_DIR:-${REPO_DIR}/toolchains}"

# The program's file name, its package, and the macOS bundle identifier.
readonly BINARY_NAME="hwclient"
readonly MAIN_PACKAGE="./cmd/hwclient"
readonly BUNDLE_ID="net.ariankordi.hwclient"
# The icon used for the macOS app bundle (the menu bar icon is embedded).
readonly APP_ICON="${REPO_DIR}/cmd/hwclient/icon.png"
# Oldest macOS the bundle claims to support. Go 1.25 and later need 12.
readonly MACOS_MIN_VERSION="12.0"
# zig target triples of the two macOS slices, carrying that floor.
readonly MACOS_ARM64_TRIPLE="aarch64-macos.${MACOS_MIN_VERSION}-none"
readonly MACOS_X86_64_TRIPLE="x86_64-macos.${MACOS_MIN_VERSION}-none"
# The macOS SDK's Cocoa stub has its re-exports stripped
# (hexops-graveyard/sdk-macos-12.0), so what systray's "-framework Cocoa"
# would normally bring in is linked directly. The header padding leaves room
# for the bundle signature's load command, which rcodesign cannot make
# itself (zig's linker leaves none spare).
readonly MACOS_EXTRA_LDFLAGS="-framework AppKit -framework Foundation -framework CoreFoundation -lobjc -Wl,-headerpad_max_install_names"

readonly USAGE="Usage: $0 <windows-x64|windows-arm64|linux-x86_64|linux-aarch64|macos-universal> [out-dir]"

if [[ $# -lt 1 || $# -gt 2 ]]; then
    echo "${USAGE}" >&2
    exit 1
fi
readonly TARGET="$1"
OUT_DIR="${2:-${REPO_DIR}/dist}"
mkdir -p "${OUT_DIR}"
OUT_DIR="$(cd "${OUT_DIR}" && pwd)"
readonly OUT_DIR

# The display name comes from the one place the program defines it, so a
# rename never has to touch this script.
APP_NAME="$(sed -n 's/^const Name = "\(.*\)"$/\1/p' "${REPO_DIR}/internal/app/app.go")"
if [[ -z "${APP_NAME}" ]]; then
    echo "error: could not read 'const Name' from internal/app/app.go" >&2
    exit 1
fi
readonly APP_NAME

VERSION="${VERSION:-$(git -C "${REPO_DIR}" describe --tags --always --dirty 2>/dev/null || echo dev)}"
readonly VERSION

# Prints the numeric x.y.z part of VERSION for places that accept nothing
# else (the bundle's version keys), or 0.0.0 for an untagged build.
numeric_version() {
    local numeric
    numeric="$(echo "${VERSION#v}" | sed -n 's/^\([0-9]*\.[0-9]*\.[0-9]*\).*/\1/p')"
    echo "${numeric:-0.0.0}"
}

# Builds the web UI that gets embedded in the binary.
build_web() {
    if [[ "${SKIP_WEB:-}" == "1" ]]; then
        echo "=== web UI: skipped (SKIP_WEB=1) ===" >&2
        return
    fi
    echo "=== web UI ===" >&2
    (cd "${REPO_DIR}/web" && npm ci --no-audit --no-fund && npm run build) >&2
}

# Builds the program for one platform into the given file. Further
# arguments are extra -ldflags. The environment decides cgo and the compiler.
go_build() {
    local goos="$1" goarch="$2" output="$3"
    shift 3
    echo "=== ${goos}/${goarch} -> ${output} ===" >&2
    (cd "${REPO_DIR}" && GOOS="${goos}" GOARCH="${goarch}" go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION} $*" -o "${output}" "${MAIN_PACKAGE}")
}

# A pure Go build: Windows and Linux need no C compiler.
build_pure_go() {
    local goos="$1" goarch="$2" output="$3"
    shift 3
    CGO_ENABLED=0 go_build "${goos}" "${goarch}" "${output}" "$@"
}

# Builds one macOS slice with zig cc as the C compiler and linker.
build_macos_slice() {
    local goarch="$1" triple="$2" output="$3" sysroot="$4"
    local cc="zig cc -target ${triple} -isysroot ${sysroot} -isystem ${sysroot}/usr/include"
    cc+=" -F${sysroot}/System/Library/Frameworks -L${sysroot}/usr/lib"
    CGO_ENABLED=1 CC="${cc}" CGO_LDFLAGS="${MACOS_EXTRA_LDFLAGS}" \
        go_build darwin "${goarch}" "${output}"
}

# Escapes the characters XML does not allow in text.
xml_escape() {
    local text="$1"
    text="${text//&/&amp;}"
    text="${text//</&lt;}"
    text="${text//>/&gt;}"
    echo "${text}"
}

# Writes the bundle's Info.plist. LSUIElement keeps the app out of the Dock:
# it lives in the menu bar only.
write_info_plist() {
    local path="$1" numeric name
    numeric="$(numeric_version)"
    name="$(xml_escape "${APP_NAME}")"
    cat > "${path}" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>en</string>
    <key>CFBundleDisplayName</key>
    <string>${name}</string>
    <key>CFBundleExecutable</key>
    <string>${BINARY_NAME}</string>
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundleName</key>
    <string>${name}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>${numeric}</string>
    <key>CFBundleVersion</key>
    <string>${numeric}</string>
    <key>LSMinimumSystemVersion</key>
    <string>${MACOS_MIN_VERSION}</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
EOF
}

# Ad-hoc signs the whole bundle (no certificate involved). The linker's
# signature covers only the executable; without one over the bundle, macOS
# reports the app as not signed at all and the Info.plist as not bound.
# Apple's codesign is used where it exists, rcodesign everywhere else.
sign_bundle() {
    local bundle="$1"
    echo "=== ad-hoc signing ===" >&2
    if [[ "$(uname -s)" == "Darwin" ]]; then
        codesign --force --sign - --identifier "${BUNDLE_ID}" "${bundle}" >&2
    else
        local rcodesign
        rcodesign="$("${SCRIPT_DIR}/fetch-rcodesign.sh" "${TOOLCHAINS_DIR}")"
        "${rcodesign}" sign "${bundle}" >&2
    fi
}

# Builds both macOS slices, merges them and packs the .app into a zip.
build_macos_universal() {
    local zig_dir sysroot work bundle
    zig_dir="$("${SCRIPT_DIR}/fetch-zig.sh" "${TOOLCHAINS_DIR}")"
    sysroot="$("${SCRIPT_DIR}/fetch-macos-sdk.sh" "${TOOLCHAINS_DIR}/macos-sdk")"
    # zig's caches stay with the toolchain, so CI can cache them together.
    export PATH="${zig_dir}:${PATH}"
    export ZIG_GLOBAL_CACHE_DIR="${TOOLCHAINS_DIR}/zig-cache"
    export ZIG_LOCAL_CACHE_DIR="${TOOLCHAINS_DIR}/zig-cache"

    work="${OUT_DIR}/.macos-build"
    rm -rf "${work}"
    mkdir -p "${work}"
    build_macos_slice arm64 "${MACOS_ARM64_TRIPLE}" "${work}/${BINARY_NAME}-arm64" "${sysroot}"
    build_macos_slice amd64 "${MACOS_X86_64_TRIPLE}" "${work}/${BINARY_NAME}-x86_64" "${sysroot}"

    bundle="${work}/${APP_NAME}.app"
    mkdir -p "${bundle}/Contents/MacOS" "${bundle}/Contents/Resources"
    echo "=== ${APP_NAME}.app ===" >&2
    # Each slice keeps the ad-hoc signature its linker gave it; Apple Silicon
    # refuses to run unsigned code.
    python3 "${SCRIPT_DIR}/make-fat-macho.py" --x86_64 "${work}/${BINARY_NAME}-x86_64" \
        --arm64 "${work}/${BINARY_NAME}-arm64" --output "${bundle}/Contents/MacOS/${BINARY_NAME}"
    chmod +x "${bundle}/Contents/MacOS/${BINARY_NAME}"
    write_info_plist "${bundle}/Contents/Info.plist"
    python3 "${SCRIPT_DIR}/png-to-icns.py" "${APP_ICON}" "${bundle}/Contents/Resources/AppIcon.icns"
    sign_bundle "${bundle}"

    local archive="${OUT_DIR}/${BINARY_NAME}-macos-universal.zip"
    rm -f "${archive}"
    # -y keeps symbolic links as links; -X leaves out host-specific extras.
    (cd "${work}" && zip -q -r -y -X "${archive}" "${APP_NAME}.app")
    rm -rf "${work}"
    echo "${archive}"
}

# --- main ---

build_web
case "${TARGET}" in
    windows-x64 | windows-arm64)
        goarch="amd64"
        [[ "${TARGET}" == "windows-arm64" ]] && goarch="arm64"
        output="${OUT_DIR}/${BINARY_NAME}-${TARGET}.exe"
        # windowsgui: no console window opens next to the tray icon.
        build_pure_go windows "${goarch}" "${output}" -H=windowsgui
        echo "${output}"
        ;;
    linux-x86_64 | linux-aarch64)
        goarch="amd64"
        [[ "${TARGET}" == "linux-aarch64" ]] && goarch="arm64"
        output="${OUT_DIR}/${BINARY_NAME}-${TARGET}"
        build_pure_go linux "${goarch}" "${output}"
        echo "${output}"
        ;;
    macos-universal)
        build_macos_universal
        ;;
    *)
        echo "error: unknown target '${TARGET}'" >&2
        echo "${USAGE}" >&2
        exit 1
        ;;
esac
