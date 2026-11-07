# mdview 설치·사용 가이드 — Apple Silicon Mac

## 1. 설치 전 확인

- **지원 기기:** Apple Silicon Mac(M1·M2·M3·M4 및 이후 Apple Silicon 모델).
- **운영체제:** macOS 13 Ventura 이상.
- **배포 파일:** `mdview-1.0.3-macos-arm64.pkg`.
- **인터넷:** 현재 문서 뷰어는 마크다운·다이어그램·코드 강조·PDF 라이브러리를 CDN에서 불러오므로 인터넷 연결이 필요합니다. 오프라인 사용은 보장하지 않습니다.
- **별도 설치 불필요:** Go, Homebrew, Node.js, Xcode는 필요하지 않습니다.
- **선택 기능:** 웹 페이지 조작을 함께 녹화하려면 Google Chrome 또는 Chromium이 필요합니다. 일반 문서 열람에는 필요하지 않습니다.

이 버전은 Mac App Store 외부에서 배포하는 앱입니다. 앱에는 `Developer ID Application: Mediawave (23B9Q752EU)`, 설치 프로그램에는 `Developer ID Installer: Mediawave (23B9Q752EU)` 서명과 Apple 공증 티켓이 적용됩니다. 인증서·개인 키·개발자의 API 키는 배포 파일에 포함하지 않습니다.

## 2. 설치하기 — 터미널 없이

1. `mdview-1.0.3-macos-arm64.pkg`를 더블클릭합니다.
2. macOS 설치 프로그램에서 **계속 → 설치**를 누릅니다.
3. 요청하면 Mac 관리자 인증을 완료합니다.
4. 설치가 완료되면 **닫기**를 누릅니다.

앱과 CLI가 함께 설치됩니다. 이 Mac의 다른 사용자 계정에서도 사용할 수 있습니다.

| 항목 | 설치 위치 | 실행 방법 |
| --- | --- | --- |
| 데스크톱 앱 | `/Applications/mdview.app` | 응용 프로그램 → mdview |
| 터미널 명령 | `/usr/local/bin/mdview` | 터미널에서 `mdview` 입력 |

CLI 명령은 앱 내부의 실행 파일로 자동 연결됩니다. 표준 macOS 터미널에서는 PATH 수정, 수동 링크 생성, 앱 최초 실행이 필요하지 않습니다. 앱과 CLI의 설정·녹음은 사용자 계정별로 분리됩니다.

기존 1.0.0 DMG는 앱만 복사하는 방식이라 CLI가 자동 등록되지 않았습니다. 다른 사용자에게 전달할 때에는 **1.0.1 이상의 PKG 설치 파일**을 사용하세요.

## 3. 첫 문서 열기

1. 앱에서 **폴더 열기…**를 눌러 프로젝트나 문서 폴더를 선택합니다. 단일 문서는 **파일 열기…**로 선택합니다.
2. 기본 브라우저가 자동으로 열립니다. 폴더 모드에서는 왼쪽 목록에서 문서를 선택합니다.
3. 파일을 수정하면 브라우저에 실시간으로 반영됩니다.

HTML(`.html`, `.htm`) 파일은 CSS·스크립트·이미지가 적용된 미리보기로 표시됩니다. HTML은 뷰어와 격리된 영역에서 열리며 파일 변경도 자동 반영됩니다.

앱 창에 파일·폴더 하나를 끌어 놓아도 열 수 있습니다. Finder의 문서에서 **우클릭 → 다음으로 열기 → mdview**도 사용할 수 있습니다. 기본 문서 연결 앱은 설치 과정에서 강제로 변경하지 않습니다.

- 브라우저 탭을 닫았다면 앱의 **브라우저 다시 열기**를 누릅니다.
- 다른 문서를 열려면 먼저 **뷰어 중지**를 누른 뒤 다시 파일·폴더를 선택합니다.
- 사용을 마치면 **뷰어 중지** 또는 **⌘Q**로 종료합니다. 브라우저 탭만 닫으면 문서 서버는 계속 실행됩니다.
- 앱 창을 닫아도 앱과 문서 서버가 종료됩니다.

문서 서버는 이 Mac의 로컬 주소에서만 접속할 수 있습니다. 기본 주소는 `http://localhost:8642`이며, 사용 중이면 앱 창에 표시되는 다른 포트를 사용합니다.

## 4. 녹음·전사 사용하기

### 녹음

1. 브라우저 상단의 입력 장치 목록에서 마이크를 선택합니다.
2. 녹음 버튼을 누르고 브라우저의 마이크 사용 요청을 허용합니다.
3. 녹음 버튼을 다시 눌러 저장합니다. 전사·프롬프트 처리가 끝날 때까지 기다립니다.
4. 녹음 저장과 후처리가 끝난 뒤 앱을 중지하거나 종료합니다.

**저장 위치:** 앱과 CLI 모두 작업 폴더와 무관하게 `~/Library/Application Support/mdview/mdview-recordings/rec-<시각>/`에 저장합니다.

저장 후 7일이 지난 녹화는 자동 삭제합니다. 보존할 녹화는 다른 폴더에 복사하세요. 전사 완료 시 `transcript.json` 전체 내용을 클립보드에 자동 복사하고 시스템 알림으로 알려줍니다.

`audio.webm` 등 오디오 파일, `events.json`, `prompt.txt`가 저장됩니다. API 키가 없더라도 녹음과 조작 기록 저장 및 조작 기록 기반 `prompt.txt` 생성은 가능합니다.

녹음 중 앱을 종료하면 아직 브라우저 메모리에 있는 녹음은 저장되지 않습니다. 반드시 브라우저에서 먼저 녹음을 저장하세요. 이미 시작한 전사·프롬프트 후처리는 앱이 마무리를 기다리며, 이때 종료가 잠시 지연될 수 있습니다.

### 음성 전사 설정 — 선택 사항

음성을 글로 변환하려면 본인의 OpenAI API 키를 설정합니다.

1. mdview 웹 화면 **왼쪽 하단의 톱니바퀴(설정)**를 누릅니다. 파일 모드와 폴더 모드 모두 같은 위치에 있습니다.
2. **OpenAI API 키**에 본인의 키를 입력합니다.
3. **전사 모델**을 선택합니다. 현재 기본값은 **Whisper 1 (`whisper-1`)**입니다.
4. **저장**을 누릅니다. 앱을 재시작하지 않아도 다음 녹음의 전사부터 적용됩니다.

저장된 키는 다시 화면으로 보내지 않으며 ‘설정됨’으로만 표시합니다. 키 입력란을 비워 두고 저장하면 기존 키를 유지합니다. 키를 바꾸려면 새 키를 입력하고 저장하고, 삭제하려면 **저장된 키 삭제 → 저장**을 누릅니다. 닫기나 Esc는 저장하지 않은 변경을 취소합니다.

설정은 사용자 계정의 `~/.config/mdview/config.json`에 저장되며 API 키 파일은 해당 사용자만 읽고 쓸 수 있는 권한(0600)으로 저장됩니다. 브라우저 저장소에는 키를 보관하지 않습니다. 같은 계정에서 실행하는 데스크톱 앱과 CLI가 설정을 공유합니다.

| 모델 | 전사 결과 |
| --- | --- |
| Whisper 1 (`whisper-1`, 기본) | 발화별 시간 정보를 문서 조작과 함께 타임라인에 기록 |
| GPT Transcribe (`gpt-transcribe`) | 전체 전사문 저장, 발화별 시간 정보 없음 |
| GPT-4o Transcribe (`gpt-4o-transcribe`) | 전체 전사문 저장, 발화별 시간 정보 없음 |
| GPT-4o mini Transcribe (`gpt-4o-mini-transcribe`) | 전체 전사문 저장, 발화별 시간 정보 없음 |

시간 정보가 없는 모델의 음성 전사는 `prompt.txt`의 별도 ‘음성 전사 (시간 정보 없음)’ 영역과 `transcript.json`의 `untimedSpeech`에 저장합니다. 문서·웹 조작 시간은 그대로 남습니다. 모델별 API 응답 형식은 [OpenAI 공식 전사 문서](https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create)를 따릅니다.

환경 변수 `OPENAI_API_KEY`를 설정한 CLI에서는 환경 변수의 키가 우선합니다. 설정 창에도 이를 안내합니다. Finder에서 실행한 앱에는 터미널 환경 변수가 보통 전달되지 않으므로 웹 설정에서 저장하는 편이 간단합니다.

음성 전사를 사용할 때 녹음 오디오는 OpenAI API로 전송되며 본인의 계정에 API 사용 요금이 부과됩니다. 키가 없거나 업로드 가능한 크기를 초과하면 전사를 생략하고 조작 기록만으로 프롬프트를 만듭니다. 키 없이도 문서 열람과 녹음은 사용할 수 있습니다.

### 웹 페이지 조작 함께 녹화 — 선택 사항

Google Chrome을 설치한 다음 브라우저 상단의 URL 입력란에 주소를 넣고 **웹 열기**를 누릅니다. mdview 전용 Chrome 프로필이 열리며 녹음 중 클릭·복사 등을 같은 타임라인에 기록합니다. 전용 프로필의 데이터는 `~/.config/mdview/chrome-profile`에 남습니다. 세부 동작과 제한은 프로젝트 `README.md`를 참고하세요.

## 5. 터미널에서 바로 사용하기

PKG 설치가 끝나면 터미널에서 바로 실행할 수 있습니다. 앱을 먼저 실행할 필요는 없습니다.

```bash
# 설치·버전 확인
mdview --version

# 현재 디렉토리 탐색
mdview

# 파일 열기
mdview README.md

# 포트 지정
mdview -port 9000 README.md
```

CLI는 **Ctrl+C**로 종료합니다. CLI로 시작한 녹음도 `~/Library/Application Support/mdview/mdview-recordings/`에 저장됩니다.

명령을 찾지 못한다면 새 터미널 창에서 아래를 확인합니다.

```bash
rehash
command -v mdview
mdview --version
```

정상 설치라면 `/usr/local/bin/mdview`가 표시됩니다. `type -a mdview`에서 이전 개발용 설치(`~/.local/bin/mdview` 등)가 먼저 선택되면 이전 실행 파일을 제거한 뒤 다시 확인하세요. PATH를 직접 제한한 특수한 셸 환경에서는 `/usr/local/bin/mdview --version`으로 실행하고, 해당 셸의 PATH에 `/usr/local/bin`이 포함되었는지 확인합니다.

## 6. 업데이트·삭제

### 업데이트

1. 브라우저에서 녹음을 저장하고 후처리 완료를 기다립니다.
2. mdview 앱과 별도로 실행한 CLI를 모두 종료합니다.
3. 새 PKG 설치 프로그램을 실행합니다. 앱과 CLI 연결이 함께 갱신됩니다.

설정 파일과 기존 녹음은 앱 바깥에 있으므로 앱 교체 후에도 유지됩니다. 자동 업데이트 기능은 없습니다.

### 삭제

1. mdview 앱과 CLI를 종료합니다.
2. 응용 프로그램 폴더의 `mdview.app`을 휴지통으로 옮깁니다.
3. 자동 설치된 CLI 링크를 삭제합니다. 먼저 대상을 확인하세요.

```bash
ls -l /usr/local/bin/mdview
# 출력이 /Applications/mdview.app/Contents/MacOS/mdview를 가리키는 링크인 경우에만:
sudo rm /usr/local/bin/mdview

# 설치 기록도 제거하려면:
sudo pkgutil --forget camp.tools.mdview.pkg
```

개인 설정과 Chrome 프로필은 `~/.config/mdview`, 녹음은 `~/Library/Application Support/mdview/mdview-recordings`에 남습니다. 이전 버전에서 저장한 녹음은 당시 열었던 폴더의 `mdview-recordings`에 남습니다. 이 데이터까지 지우려면 Finder에서 위치를 확인한 뒤 필요한 파일을 백업하고 직접 삭제하세요.

## 7. 문제가 생겼을 때

| 증상 | 확인할 내용 |
| --- | --- |
| Intel Mac에서 실행되지 않음 | 이 배포 파일은 Apple Silicon 전용입니다. Intel Mac은 지원하지 않습니다. |
| macOS 버전이 지원되지 않음 | macOS 13 이상인지 ‘이 Mac에 관하여’에서 확인합니다. |
| `zsh: command not found: mdview` | 1.0.1 이상 PKG로 설치했는지 확인합니다. 기존 DMG의 앱 복사만으로는 명령이 등록되지 않습니다. 새 터미널에서 `command -v mdview`를 확인합니다. |
| 브라우저가 열리지 않음 | 앱에 표시된 `http://localhost:포트`로 직접 접속하거나 **브라우저 다시 열기**를 누릅니다. |
| 문서가 빈 화면이거나 PDF·다이어그램이 표시되지 않음 | 인터넷 연결과 회사 네트워크의 `cdn.jsdelivr.net`, `cdnjs.cloudflare.com` 접근 가능 여부를 확인합니다. |
| 마이크가 녹음되지 않음 | 브라우저의 localhost 마이크 권한과 시스템 설정 → 개인정보 보호 및 보안 → 마이크에서 해당 브라우저 권한을 확인합니다. |
| 녹음 저장 실패 | `~/Library/Application Support/mdview`의 쓰기 권한과 디스크 여유 공간을 확인합니다. |
| 전사만 되지 않음 | 왼쪽 하단 설정에서 본인의 API 키·전사 모델을 확인하고 API 이용 가능 상태·오디오 크기를 확인합니다. 일반 문서 열람과 녹음은 키 없이 사용할 수 있습니다. |
| ‘확인되지 않은 개발자’·‘손상된 앱’ 경고 | `unnotarized`가 붙지 않은 정식 배포본인지 확인하고 다시 다운로드합니다. 임의로 보안 기능을 끄기보다 아래 검증으로 서명·공증을 확인합니다. |
| 종료 중 상태가 지속됨 | 진행 중인 전사·프롬프트 처리를 기다립니다. 강제 종료하면 처리 중인 결과를 잃을 수 있습니다. |

다운로드 파일의 무결성은 배포 폴더의 `SHA256SUMS`와 대조할 수 있습니다.

```bash
cd "다운로드한 배포 폴더 경로"
shasum -a 256 -c SHA256SUMS
```

앱 서명과 Apple 공증 확인:

```bash
codesign --verify --deep --strict --verbose=2 /Applications/mdview.app
spctl --assess --type execute --verbose=2 /Applications/mdview.app
```

정상 정식 배포본은 `accepted`, `source=Notarized Developer ID`, `origin=Developer ID Application: Mediawave (23B9Q752EU)`를 표시합니다. 최초 실행 시 일반적인 다운로드 확인 창은 별도로 나타날 수 있습니다.
