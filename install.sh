#!/bin/bash

# mdview 빌드 및 설치 스크립트
# 테스트와 정적 검사를 통과한 뒤 바이너리를 빌드하고 설치합니다.
# 프로젝트의 config.json이 있으면 ~/.config/mdview/config.json 으로 함께 배포합니다.
#
# 사용법:
#   ./install.sh              # test + vet + build + ~/.local/bin 설치 + 설정 복사
#   ./install.sh --build-only # 빌드까지만 수행 (설치·설정 복사 생략)
#   PREFIX=/usr/local ./install.sh   # 설치 경로 변경 (기본: $HOME/.local)

set -euo pipefail
cd "$(dirname "$0")"

BIN="mdview"
LDFLAGS="-s -w"
PREFIX="${PREFIX:-$HOME/.local}"
INSTALL_DIR="$PREFIX/bin"
CONFIG_SRC="config.json"
CONFIG_DIR="$HOME/.config/mdview"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

if ! command -v go &> /dev/null; then
    echo -e "${RED}오류: Go가 설치되어 있지 않습니다.${NC}"
    echo "설치 방법: brew install go"
    exit 1
fi

echo -e "${GREEN}=== 테스트 ===${NC}"
go test ./...

echo -e "${GREEN}=== 정적 검사 ===${NC}"
go vet ./...

echo -e "${GREEN}=== 빌드 ===${NC}"
CGO_ENABLED=0 go build -ldflags="$LDFLAGS" -o "$BIN" ./cmd/
echo "생성: $(pwd)/$BIN ($(du -h "$BIN" | cut -f1 | tr -d ' '))"

if [[ "${1:-}" == "--build-only" ]]; then
    echo -e "${GREEN}완료 (빌드만 수행)${NC}"
    exit 0
fi

echo -e "${GREEN}=== 설치 ===${NC}"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$BIN" "$INSTALL_DIR/$BIN"
echo "설치: $INSTALL_DIR/$BIN"

# 설정 파일 배포: 프로젝트 config.json → ~/.config/mdview/config.json (API 키 보호를 위해 0600)
if [[ -f "$CONFIG_SRC" ]]; then
    mkdir -p "$CONFIG_DIR"
    install -m 0600 "$CONFIG_SRC" "$CONFIG_DIR/config.json"
    echo "설정 복사: $CONFIG_DIR/config.json"
else
    echo -e "${YELLOW}참고: $CONFIG_SRC 이 없어 설정 복사를 건너뜁니다 (config.example.json 참고).${NC}"
fi

if ! command -v "$BIN" &> /dev/null; then
    echo -e "${YELLOW}참고: '$INSTALL_DIR'가 PATH에 없습니다. 아래를 셸 설정에 추가하세요.${NC}"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi

echo -e "${GREEN}완료${NC}"
