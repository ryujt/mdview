# macOS Apple Silicon 배포 빌드

`build-release.sh`는 기존 Go CLI와 AppKit 런처를 묶어 macOS PKG 설치 프로그램을 만듭니다. 설치 시 `/Applications/mdview.app`과 `/usr/local/bin/mdview` 링크를 함께 배치하여 다른 사용자도 터미널에서 바로 실행할 수 있습니다. 사용자에게는 Go·Node.js·Xcode가 필요하지 않습니다. 런처는 사용자가 선택한 폴더를 작업 디렉토리로 사용하며 종료 시 자신이 시작한 CLI에 SIGTERM을 전달하고 후처리를 기다립니다.

## 빌드 환경

- macOS와 Xcode/Command Line Tools(Swift, `notarytool`, `stapler`, `iconutil`).
- Go(`go.mod`의 toolchain 자동 다운로드 가능), Node.js(기존 JS 테스트용), Python 3.
- 개인 키와 함께 키체인에 설치된 **Developer ID Application** 인증서.
- 설치 프로그램 서명용 **Developer ID Installer** 인증서와 개인 키.
- Apple 공증 인증: `NOTARY_PROFILE` 키체인 프로필 또는 `APPLE_CREDENTIALS_JSON` 경로.

Mac App Store용 `Apple Distribution` 인증서와 iOS provisioning profile은 이 배포에 사용하지 않습니다. 앱은 Developer ID Application과 hardened runtime으로, 설치 프로그램은 Developer ID Installer로 각각 서명하고 보안 타임스탬프를 적용합니다. 공증은 [Apple의 공증 절차](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)에 따라 앱 ZIP과 최종 PKG에 각각 수행하고 티켓을 부착합니다.

## 정식 빌드

```bash
cd cli/mdview
NOTARY_PROFILE="사용할-키체인-프로필" ./packaging/macos/build-release.sh
```

제공된 App Store Connect 인증 자료 JSON을 사용하는 경우:

```bash
APPLE_CREDENTIALS_JSON="/인증자료가/있는/외부경로/계정과_인증자료.json" \
  ./packaging/macos/build-release.sh
```

JSON은 `app_store_connect_api` 아래 `key_id`, `issuer_id`, `key_path`(또는 `installed_key_path`)를 읽습니다. 개인 키 파일은 해당 외부 경로에서만 사용하며 프로젝트나 결과물에 복사하지 않습니다. JSON·개인 키는 커밋하지 마세요.

다른 팀의 인증서를 사용하려면 `SIGN_IDENTITY` 환경변수에 키체인의 Developer ID Application 이름 또는 SHA-1 지문을 지정합니다. 기본값은 제공된 `developer-id-application-20261031.cer`의 SHA-1 지문 `8102033CFEB5309D454652A5CCC4E20B784585D4`입니다. 동일 이름의 다른 인증서와 혼동하지 않도록 지문으로 지정합니다.

설치 프로그램 인증서는 `INSTALLER_SIGN_IDENTITY`로 지정합니다. 기본값은 2026-11-09 발급한 `Developer ID Installer: Mediawave (23B9Q752EU)` 인증서의 SHA-1 지문 `884593FADCB9B8FD13EB205B2DEEB724AEF0A78F`입니다. 인증서와 대응하는 전용 개인 키는 외부 `AppStore_등록_재사용` 폴더에 보관합니다. 앱용 인증서로 PKG를 서명해서는 안 됩니다. [Apple의 패키지 서명 안내](https://developer.apple.com/documentation/security/resolving-common-notarization-issues)를 따릅니다.

로컬 패키징 확인만 할 때:

```bash
./packaging/macos/build-release.sh --no-notarize
```

이 경우에도 Developer ID 서명은 필요하며, 결과 이름에 `-unnotarized`가 붙습니다. 사용자에게 전달하는 정식 배포본으로 사용하지 마세요.

## 결과물과 검증

`dist/mdview-<버전>-macos-arm64/`에 다음을 생성합니다.

- `mdview-<버전>-macos-arm64.pkg`: 사용자에게 전달할 설치 프로그램. 공증 티켓이 부착된 앱과 CLI 링크가 포함됩니다.
- `INSTALL.md`, `시작하기.html`: 한국어 설치·사용 안내.
- `SHA256SUMS`: 최종 PKG와 설치 가이드의 SHA-256.
- `THIRD-PARTY-NOTICES.txt`: Go 및 의존 모듈의 라이선스 고지. 앱 리소스에도 포함됩니다.
- `release.json`: 버전·소스 커밋·빌드 시각·지원 OS·공증 여부.
- `verification/`: Apple 공증 응답과 제출 ID.

검증 단계는 Go 테스트·vet, 기존 Node.js 테스트, Mach-O arm64 빌드, CLI 버전 실행, 중첩 서명 검증, `pkgutil` 설치 프로그램 서명 검증, 앱·PKG 공증 승인 확인, 티켓 검증, Gatekeeper 평가입니다. 공증이 승인되지 않으면 정식 결과 폴더를 생성하지 않습니다. 동일 버전의 기존 결과물이 있으면 덮어쓰지 않고 중단합니다.

`dist/`는 Git에서 제외됩니다. 로컬 `config.json`, 녹음, 인증 자료는 패키징 대상에 포함하지 않습니다. `config.example.json`만 앱 리소스로 포함합니다. 설치에는 관리자 인증이 필요할 수 있습니다. 사용자별 셸 설정 파일을 수정하지 않고 macOS 기본 PATH의 `/usr/local/bin`에 링크를 설치하며 앱 실행에는 관리자 권한이 필요하지 않습니다. 설치 스크립트·자동 시작 항목은 추가하지 않습니다.

PKG의 bundle relocation은 비활성화합니다. 같은 번들 ID의 개발 빌드가 Downloads나 dist에 남아 있어도 앱은 항상 `/Applications/mdview.app`에 설치되며 CLI 링크도 이 경로를 가리킵니다. 최소 macOS 13과 arm64 조건은 `Distribution.xml.in`에서 지정합니다.

버전은 `VERSION`에서 관리합니다. 버전을 바꾸면 설치 가이드의 배포 파일 이름도 함께 갱신하세요. 빌드 시 `--version`에 버전·커밋·UTC 시각을 주입합니다. 커밋하지 않은 변경이 있으면 커밋 정보에 `-dirty`를 덧붙입니다.

## 수동 실행 확인

완성된 PKG를 macOS 설치 프로그램으로 설치한 뒤 아래를 확인합니다.

1. 앱 실행 → 파일/폴더 선택 → 브라우저 열림·문서 렌더링.
2. 파일명에 공백·한글이 있는 문서 및 파일 드래그 열기.
3. ‘브라우저 다시 열기’, ‘뷰어 중지’, 다른 폴더 열기.
4. Finder의 ‘다음으로 열기’ 및 `open -a mdview <파일>`.
5. 앱 종료 뒤 해당 세션의 로컬 포트와 CLI 프로세스가 정리되는지 확인.
6. 녹음을 사용했다면 저장·후처리 후 종료하고, 선택한 폴더 아래 결과를 확인.
7. 앱을 실행하기 전에 새 터미널에서 `command -v mdview`, `mdview --version` 확인. 사용자 설정이 없는 기본 zsh에서도 `/usr/local/bin/mdview`가 선택되는지 확인.
8. `pkgutil --expand-full`로 패키지를 검사해 앱·CLI 링크만 포함되는지, 링크가 `/Applications/mdview.app/Contents/MacOS/mdview`를 가리키는지 확인.

현재 뷰어의 CDN 의존성은 유지됩니다. 완전한 오프라인 배포는 아닙니다. 배포 대상은 macOS 13 이상 arm64이며 다른 macOS 버전·Intel 환경은 별도 검증이 필요합니다.
