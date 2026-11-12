#!/bin/bash
# Apple Silicon distribution: app + system CLI, signed and notarized Installer package.
set -euo pipefail
cd "$(dirname "$0")/../.."

NOTARIZE=1
if [[ "${1:-}" == "--no-notarize" && $# == 1 ]]; then
    NOTARIZE=0
elif [[ $# != 0 ]]; then
    echo "Usage: $0 [--no-notarize]" >&2
    exit 1
fi
if [[ "$(uname -s)" != Darwin ]]; then
    echo "This release must be built on macOS with Xcode command line tools." >&2
    exit 1
fi
if [[ "$NOTARIZE" == 1 && -z "${NOTARY_PROFILE:-}" && -z "${APPLE_CREDENTIALS_JSON:-}" ]]; then
    echo "Set NOTARY_PROFILE or APPLE_CREDENTIALS_JSON. Use --no-notarize only for local checks." >&2
    exit 1
fi

VERSION="$(tr -d '\r\n' < VERSION)"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "VERSION must contain a numeric major.minor.patch version." >&2
    exit 1
fi
SIGN_IDENTITY="${SIGN_IDENTITY:-8102033CFEB5309D454652A5CCC4E20B784585D4}"
INSTALLER_SIGN_IDENTITY="${INSTALLER_SIGN_IDENTITY:-884593FADCB9B8FD13EB205B2DEEB724AEF0A78F}"
COMMIT="$(git rev-parse --short=12 HEAD)"
if [[ -n "$(git status --porcelain -- .)" ]]; then COMMIT="$COMMIT-dirty"; fi
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
NAME="mdview-$VERSION-macos-arm64"
if [[ "$NOTARIZE" == 0 ]]; then NAME="$NAME-unnotarized"; fi
OUT="$PWD/dist/$NAME"
if [[ -e "$OUT" ]]; then
    echo "Output already exists: $OUT. Move it aside before rebuilding." >&2
    exit 1
fi
mkdir -p "$PWD/dist"
WORK="$(mktemp -d "$PWD/dist/.macos-build.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
APP="$WORK/mdview.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$WORK/reports"

echo "Checking Go and JavaScript…"
go test ./...
go vet ./...
node --test test/*.test.mjs

echo "Building ${NAME}…"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$BUILD_DATE" \
    -o "$APP/Contents/MacOS/mdview" ./cmd/
xcrun swiftc -swift-version 5 -O -target arm64-apple-macosx13.0 \
    -framework AppKit packaging/macos/Launcher.swift -o "$APP/Contents/MacOS/mdview-launcher"
cp packaging/macos/Info.plist "$APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION" "$APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $VERSION" "$APP/Contents/Info.plist"
cp docs/INSTALL-macos-arm64.md "$APP/Contents/Resources/INSTALL.md"
cp config.example.json "$APP/Contents/Resources/config.example.json"
python3 packaging/macos/third-party-notices.py "$APP/Contents/Resources/THIRD-PARTY-NOTICES.txt"
# Generate the icon from native drawing code, with no external asset dependencies.
xcrun swiftc -swift-version 5 -O -framework AppKit packaging/macos/Icon.swift -o "$WORK/make-icon"
"$WORK/make-icon" "$WORK/AppIcon.iconset"
iconutil -c icns "$WORK/AppIcon.iconset" -o "$APP/Contents/Resources/AppIcon.icns"

echo "Signing the embedded CLI and app…"
codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" \
    --identifier camp.tools.mdview.cli "$APP/Contents/MacOS/mdview"
codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$APP"
codesign --verify --deep --strict --verbose=2 "$APP"
"$APP/Contents/MacOS/mdview" --version

if [[ "$NOTARIZE" == 1 ]]; then
    ditto -c -k --keepParent "$APP" "$WORK/mdview-notarization.zip"
    python3 packaging/macos/notarize.py "$WORK/mdview-notarization.zip" --report "$WORK/reports/app-notarization.json"
    xcrun stapler staple "$APP"
    xcrun stapler validate "$APP"
    spctl --assess --type execute --verbose=2 "$APP"
fi

echo "Packaging the app and system-wide mdview command…"
mkdir -p "$WORK/pkgroot/Applications" "$WORK/pkgroot/usr/local/bin" "$WORK/packages"
ditto "$APP" "$WORK/pkgroot/Applications/mdview.app"
ln -s /Applications/mdview.app/Contents/MacOS/mdview "$WORK/pkgroot/usr/local/bin/mdview"
pkgbuild --analyze --root "$WORK/pkgroot" "$WORK/components.plist"
# Disable bundle relocation: an old build in Downloads/dist/Trash must never become
# the install destination. The CLI link always targets /Applications/mdview.app.
python3 - "$WORK/components.plist" "$VERSION" "$WORK/Distribution.xml" <<'PY'
import pathlib, plistlib, sys
components, version, output = sys.argv[1:]
path = pathlib.Path(components)
items = plistlib.loads(path.read_bytes())
for item in items:
    item['BundleIsRelocatable'] = False
    item['BundleOverwriteAction'] = 'upgrade'
path.write_bytes(plistlib.dumps(items))
template = pathlib.Path('packaging/macos/Distribution.xml.in').read_text()
pathlib.Path(output).write_text(template.replace('@VERSION@', version))
PY
pkgbuild --root "$WORK/pkgroot" --component-plist "$WORK/components.plist" \
    --identifier camp.tools.mdview.pkg --version "$VERSION" --install-location / \
    --ownership recommended "$WORK/packages/mdview-component.pkg"
productbuild --distribution "$WORK/Distribution.xml" --package-path "$WORK/packages" \
    --resources packaging/macos/installer-resources --sign "$INSTALLER_SIGN_IDENTITY" \
    --timestamp "$WORK/$NAME.pkg"
pkgutil --check-signature "$WORK/$NAME.pkg"
python3 packaging/macos/verify-package.py "$WORK/$NAME.pkg" "$VERSION"
if [[ "$NOTARIZE" == 1 ]]; then
    python3 packaging/macos/notarize.py "$WORK/$NAME.pkg" --report "$WORK/reports/pkg-notarization.json"
    xcrun stapler staple "$WORK/$NAME.pkg"
    xcrun stapler validate "$WORK/$NAME.pkg"
    spctl --assess --type install --verbose=2 "$WORK/$NAME.pkg"
fi

# Publish only a completed build. Never include local config.json, recordings or credentials.
mkdir "$OUT"
mv "$WORK/$NAME.pkg" "$OUT/"
cp docs/INSTALL-macos-arm64.md "$OUT/INSTALL.md"
cp packaging/macos/START-HERE.html "$OUT/시작하기.html"
cp "$APP/Contents/Resources/THIRD-PARTY-NOTICES.txt" "$OUT/THIRD-PARTY-NOTICES.txt"
cp -R "$WORK/reports" "$OUT/verification"
(
    cd "$OUT"
    shasum -a 256 "$NAME.pkg" INSTALL.md THIRD-PARTY-NOTICES.txt > SHA256SUMS
)
python3 - "$OUT" "$VERSION" "$COMMIT" "$BUILD_DATE" "$NOTARIZE" <<'PY'
import json, pathlib, sys
out, version, commit, date, notarized = sys.argv[1:]
manifest = dict(version=version, platform="macos", architecture="arm64", minimum_macos="13.0",
                commit=commit, build_date=date, notarized=notarized == "1",
                bundle_id="camp.tools.mdview", installer="macOS Installer pkg",
                app_path="/Applications/mdview.app", cli_path="/usr/local/bin/mdview",
                package_id="camp.tools.mdview.pkg")
pathlib.Path(out, "release.json").write_text(json.dumps(manifest, indent=2) + "\n")
PY
echo "Complete: $OUT"
