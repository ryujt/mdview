#!/bin/bash

# mdview 맥 배포용 .pkg 인스톨러 빌드 스크립트
# arm64 + amd64 유니버설 바이너리를 만들어 /usr/local/bin 에 설치하는 pkg 를 생성합니다.
# 받는 사람은 Go 설치 없이 더블클릭으로 설치할 수 있습니다.
#
# 서명·공증은 himytv(create_dmg.sh)와 동일한 Mediawave Developer ID 인증서를 사용합니다.
# 키체인에 인증서가 있으면 자동으로 서명·공증하고, 없으면 미서명 pkg 를 만들고 안내를 출력합니다.
#
# 사용법:
#   ./build-pkg.sh                        # dist/mdview-<버전>-macos-universal.pkg 생성
#   VERSION=1.2.3 ./build-pkg.sh          # 버전 지정 (기본: git tag → 1.0.0)
#
# 서명 관련 환경변수 (기본값: himytv 와 동일한 Mediawave 인증서):
#   APP_SIGN_IDENTITY   바이너리 codesign 용 "Developer ID Application: ..."
#   PKG_SIGN_IDENTITY   pkg productsign 용 "Developer ID Installer: ..."
#   TEAM_ID             Apple Developer 팀 ID
#   NOTARY_PROFILE      notarytool 키체인 프로필 (기본 AC_PASSWORD)
#
# config.json 은 API 키가 담긴 개인 설정이므로 pkg 에 포함하지 않습니다.

set -euo pipefail
cd "$(dirname "$0")"

BIN="mdview"
IDENTIFIER="com.ryujt.mdview"
LDFLAGS="-s -w"
DIST_DIR="dist"

TEAM_ID="${TEAM_ID:-23B9Q752EU}"
APP_SIGN_IDENTITY="${APP_SIGN_IDENTITY:-Developer ID Application: Mediawave ($TEAM_ID)}"
PKG_SIGN_IDENTITY="${PKG_SIGN_IDENTITY:-Developer ID Installer: Mediawave ($TEAM_ID)}"
NOTARY_PROFILE="${NOTARY_PROFILE:-AC_PASSWORD}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

if [[ "$(uname)" != "Darwin" ]]; then
    echo -e "${RED}오류: 이 스크립트는 macOS에서만 실행할 수 있습니다 (lipo/pkgbuild 필요).${NC}"
    exit 1
fi

if ! command -v go &> /dev/null; then
    echo -e "${RED}오류: Go가 설치되어 있지 않습니다.${NC}"
    echo "설치 방법: brew install go"
    exit 1
fi

# 버전 결정: VERSION 환경변수 > git tag (v 접두사 제거) > 1.0.0
if [[ -z "${VERSION:-}" ]]; then
    VERSION="$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || true)"
    VERSION="${VERSION:-1.0.0}"
fi

PKG_NAME="$BIN-$VERSION-macos-universal.pkg"
PKG_PATH="$DIST_DIR/$PKG_NAME"

echo -e "${GREEN}=== 테스트 ===${NC}"
go test ./...

echo -e "${GREEN}=== 정적 검사 ===${NC}"
go vet ./...

echo -e "${GREEN}=== 빌드 (universal: arm64 + amd64) ===${NC}"
BUILD_DIR="$(mktemp -d)"
PKGROOT="$BUILD_DIR/pkgroot"
trap 'rm -rf "$BUILD_DIR"' EXIT

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$LDFLAGS" -o "$BUILD_DIR/$BIN-arm64" ./cmd/
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="$LDFLAGS" -o "$BUILD_DIR/$BIN-amd64" ./cmd/

mkdir -p "$PKGROOT/usr/local/bin"
lipo -create -output "$PKGROOT/usr/local/bin/$BIN" "$BUILD_DIR/$BIN-arm64" "$BUILD_DIR/$BIN-amd64"
chmod 0755 "$PKGROOT/usr/local/bin/$BIN"
lipo -info "$PKGROOT/usr/local/bin/$BIN"

# 키체인에 Developer ID 인증서가 있는지 확인 (없으면 미서명 pkg 생성)
CAN_SIGN=false
if security find-identity -v | grep -qF "$APP_SIGN_IDENTITY"; then
    CAN_SIGN=true
fi

if $CAN_SIGN; then
    echo -e "${GREEN}=== 바이너리 서명 ===${NC}"
    codesign --force --options runtime --timestamp \
        --sign "$APP_SIGN_IDENTITY" \
        "$PKGROOT/usr/local/bin/$BIN"
    codesign --verify --strict "$PKGROOT/usr/local/bin/$BIN"
    echo "    codesign 검증 완료."
fi

echo -e "${GREEN}=== 패키징 ===${NC}"
# 제거 가능한 extended attribute(quarantine 등)를 정리한다.
# com.apple.provenance 는 보호 속성이라 남는데, 페이로드 목록에 AppleDouble(._*) 항목으로
# 보이지만 설치 시 파일이 아닌 xattr 로만 복원되므로 무해하다.
xattr -cr "$PKGROOT"
mkdir -p "$DIST_DIR"
RAW_PKG="$BUILD_DIR/$PKG_NAME"
pkgbuild \
    --root "$PKGROOT" \
    --identifier "$IDENTIFIER" \
    --version "$VERSION" \
    --install-location / \
    "$RAW_PKG"

if $CAN_SIGN; then
    echo -e "${GREEN}=== pkg 서명 ===${NC}"
    productsign --sign "$PKG_SIGN_IDENTITY" "$RAW_PKG" "$PKG_PATH"
    pkgutil --check-signature "$PKG_PATH"

    echo -e "${GREEN}=== 공증 ===${NC}"
    NOTARIZE_RESULT=$(xcrun notarytool submit "$PKG_PATH" \
        --team-id "$TEAM_ID" \
        --keychain-profile "$NOTARY_PROFILE" \
        --wait 2>&1) || true

    echo "$NOTARIZE_RESULT"

    if echo "$NOTARIZE_RESULT" | grep -q "Accepted"; then
        xcrun stapler staple "$PKG_PATH"
        echo "    공증 완료 (stapled)."
    else
        echo ""
        echo -e "${YELLOW}    공증을 건너뛰었거나 실패했습니다.${NC}"
        echo "    공증 자격 증명 설정 방법:"
        echo "      xcrun notarytool store-credentials $NOTARY_PROFILE \\"
        echo "        --apple-id YOUR_APPLE_ID \\"
        echo "        --team-id $TEAM_ID \\"
        echo "        --password APP_SPECIFIC_PASSWORD"
    fi
else
    cp "$RAW_PKG" "$PKG_PATH"
    echo -e "${YELLOW}참고: 키체인에 '$APP_SIGN_IDENTITY' 인증서가 없어 미서명 pkg를 생성했습니다.${NC}"
    echo -e "${YELLOW}      받은 맥에서는 Finder에서 우클릭 → 열기로 설치해야 합니다.${NC}"
    echo "      서명하려면 Xcode 또는 developer.apple.com에서 다음 두 인증서를 키체인에 설치하세요:"
    echo "        - Developer ID Application: Mediawave ($TEAM_ID)  (바이너리 서명)"
    echo "        - Developer ID Installer: Mediawave ($TEAM_ID)   (pkg 서명)"
fi

echo -e "${GREEN}=== 검증 ===${NC}"
pkgutil --payload-files "$PKG_PATH"

echo -e "${GREEN}완료: $PKG_PATH ($(du -h "$PKG_PATH" | cut -f1 | tr -d ' '))${NC}"
