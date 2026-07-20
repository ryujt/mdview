# macOS .pkg 인스톨러 빌드 스크립트 설계

날짜: 2026-07-20
상태: 승인됨

## 목적

mdview를 Go가 설치되지 않은 맥에 더블클릭으로 설치할 수 있는 `.pkg` 배포 파일을 만든다.
기존 `install.sh`(소스 빌드·개발용)와 별개로, 배포 산출물 생성 전용 스크립트 `build-pkg.sh`를 추가한다.

## 결정 사항

- **형식**: `pkgbuild` 컴포넌트 패키지 (`.pkg`). 사용자 선택으로 확정.
- **바이너리**: `darwin/arm64` + `darwin/amd64` 크로스 빌드 후 `lipo`로 유니버설 바이너리 생성.
  Apple Silicon / Intel 맥 모두 단일 pkg로 지원. `CGO_ENABLED=0`, `-ldflags "-s -w"`, `-trimpath`.
- **설치 경로**: `/usr/local/bin/mdview` (pkg는 root 권한으로 설치되므로 시스템 경로 사용).
- **식별자**: `com.ryujt.mdview`
- **버전**: `VERSION` 환경변수 > `git describe --tags`(v 접두사 제거) > 기본 `1.0.0`.
- **산출물**: `dist/mdview-<버전>-macos-universal.pkg`
- **서명·공증** (2026-07-20 변경: himytv `create_dmg.sh` 방식으로 통일):
  기본값으로 Mediawave Developer ID 인증서(팀 `23B9Q752EU`)를 사용한다.
  키체인에 인증서가 있으면 자동으로 바이너리 `codesign`(runtime+timestamp) →
  `productsign` → `notarytool` 공증(`AC_PASSWORD` 키체인 프로필, himytv와 동일) → `stapler` 순으로 수행.
  인증서가 없으면 미서명 pkg를 생성하고 인증서 설치·자격 증명 설정 방법을 안내한다.
  `APP_SIGN_IDENTITY` / `PKG_SIGN_IDENTITY` / `TEAM_ID` / `NOTARY_PROFILE`로 재정의 가능.
- **config.json 미포함**: API 키가 담긴 개인 설정이므로 배포물에 넣지 않는다.
  pkg는 root로 설치되어 사용자별 `~/.config` 배치에도 부적합하다.

## 검증

- 빌드 전 `go test ./...` + `go vet ./...` 통과 필수 (install.sh와 동일 기조).
- 생성 후 `lipo -info`로 유니버설 여부, `pkgutil --payload-files`로 페이로드 경로 확인.

## 문서화

README에 "맥 배포 패키지 빌드" 섹션 추가 (빌드 방법, 서명 옵션, 미서명 pkg 설치 방법).
