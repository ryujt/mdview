# mdview

마크다운 파일을 브라우저에서 실시간으로 렌더링하는 CLI 도구입니다.
단일 파일 감시 모드와 현재 디렉토리 전체를 사이드바로 탐색하는 폴더 모드를 모두 지원합니다.

## 기능

- **파일 모드** (`mdview <file>`): 지정한 파일을 브라우저로 열고, 파일 변경을 실시간 반영합니다. 마크다운은 렌더링되고, 소스코드·설정 파일은 코드뷰어로 표시됩니다.
- **폴더 모드** (`mdview`): 현재 디렉토리를 기준으로 왼쪽 사이드바에 프로젝트 파일 트리를 표시합니다. 마크다운(`.md`) 외에 소스코드(`.go`, `.js`, `.py` 등)와 설정 파일(YAML, JSON, `.env`, `Dockerfile` 등)도 함께 표시됩니다. 파일을 클릭하면 메인 영역에 표시되며, 선택된 파일이 변경되면 자동으로 다시 렌더링됩니다.
- 마크다운 파일은 렌더링된 화면으로, 소스코드/설정 파일은 syntax highlighting과 라인 번호가 있는 코드뷰어로 표시됩니다.
- 표준 마크다운 (GFM) + 코드 블록 syntax highlighting + 복사 버튼
- 커스텀 다이어그램: `mermaid`, `state`, `navigation`, `layout`, `jobflow`
- **오디오 녹화**: 상단 툴바의 녹화 버튼으로 문서/코드를 보며 음성 설명을 녹음하고, 녹음 중 참조한 위치(파일 열람·텍스트 복사·라인 클릭)를 오디오 시간축에 맞춰 JSON으로 기록합니다. 입력 장치(마이크)를 드롭다운으로 선택할 수 있고, 녹음 중 입력 레벨이 표시됩니다.
- **전사**: 녹화 저장 시 OpenAI `whisper-1` API로 **자동 전사**되어(API 키 필요) 발화 세그먼트 타임스탬프와 참조 이벤트를 병합한 `transcript.json` 문서가 생성됩니다. 어느 시점에 어느 라인을 가리키고 어떤 텍스트를 복사했는지 발화와 함께 확인할 수 있습니다. 진행 상태는 툴바 칩으로 표시되고, 완료 시 뷰어에서 바로 열 수 있습니다. 수동 실행은 `mdview transcribe`.
- **상세 로깅**: `DEBUG_MODE`/`LOG_TO_FILE` 환경변수로 2계층 로깅(텍스트 로그 + 카테고리별 상세 아티팩트 + 세션 프로세스 다이어그램)을 켤 수 있습니다.

## 빌드 및 설치

```bash
cd cli/mdview
./install.sh                # test + vet + build + ~/.local/bin 설치 + 설정 복사
./install.sh --build-only   # 빌드까지만
PREFIX=/usr/local ./install.sh   # 설치 경로 변경 ($PREFIX/bin)
```

프로젝트 루트에 `config.json`(git 무시됨, `config.example.json` 참고)이 있으면
설치 시 `~/.config/mdview/config.json`으로 함께 복사됩니다 (권한 0600).

## 맥 배포 패키지 빌드 (.pkg)

Go가 설치되지 않은 맥에 배포할 때는 `.pkg` 인스톨러를 만듭니다.
arm64 + Intel 유니버설 바이너리가 `/usr/local/bin/mdview`로 설치됩니다.

```bash
./build-pkg.sh                 # dist/mdview-<버전>-macos-universal.pkg 생성
VERSION=1.2.3 ./build-pkg.sh   # 버전 지정 (기본: git tag → 1.0.0)
```

### 서명·공증

himytv와 동일한 Mediawave Developer ID 인증서(팀 `23B9Q752EU`)를 기본으로 사용합니다.
키체인에 인증서가 있으면 자동으로 **바이너리 codesign → pkg productsign → notarytool 공증(`AC_PASSWORD` 프로필) → staple**까지
수행하고, 없으면 미서명 pkg를 만들고 안내를 출력합니다.

- 필요한 인증서 (Xcode 또는 developer.apple.com에서 키체인에 설치):
  - `Developer ID Application: Mediawave (23B9Q752EU)` — 바이너리 서명
  - `Developer ID Installer: Mediawave (23B9Q752EU)` — pkg 서명
- 공증 자격 증명 (최초 1회):
  ```bash
  xcrun notarytool store-credentials AC_PASSWORD \
    --apple-id YOUR_APPLE_ID --team-id 23B9Q752EU --password APP_SPECIFIC_PASSWORD
  ```
- 다른 인증서를 쓰려면 `APP_SIGN_IDENTITY` / `PKG_SIGN_IDENTITY` / `TEAM_ID` / `NOTARY_PROFILE`
  환경변수로 재정의합니다.

- 받는 사람은 pkg 파일을 더블클릭해 설치하면 됩니다. 미서명 pkg는 Gatekeeper 경고가 뜨므로
  Finder에서 **우클릭 → 열기**로 설치합니다 (AirDrop/USB 전달 시에는 경고 없음).
- `config.json`(API 키)은 개인 설정이므로 pkg에 포함되지 않습니다.
  전사 기능을 쓰려면 설치 후 `~/.config/mdview/config.json`을 직접 만들거나
  `OPENAI_API_KEY` 환경변수를 설정하세요.
- 제거: `sudo rm /usr/local/bin/mdview && sudo pkgutil --forget com.ryujt.mdview`

## 실행

```bash
# 단일 파일 모드
mdview README.md

# 폴더 모드 (현재 디렉토리)
mdview

# 포트 지정 (기본 8642, 사용 중이면 이후 포트 순차 시도, 0이면 임의 포트)
mdview -port 9000 README.md
```

실행하면 브라우저가 자동으로 열립니다. `Ctrl+C`로 종료합니다.

포트가 8642로 고정된 이유: 브라우저의 마이크 권한은 `localhost:포트` origin 단위로 기억되므로,
포트가 매번 바뀌면 녹화 시마다 권한 프롬프트가 다시 뜹니다. 고정 포트에서 한 번 허용하면 이후 실행에서도 유지됩니다.

## 오디오 녹화 및 전사

문서나 코드를 보면서 요구사항·질문을 음성으로 녹음하고, 설명 중 참조한 텍스트 위치를 함께 저장해
AI나 동료에게 전달하기 위한 기능입니다.

### 녹화

1. 상단 툴바의 ⏺ 버튼을 누르면 마이크 녹음이 시작됩니다 (버튼이 정지 아이콘으로 바뀌고 타이머·입력 레벨이 표시됩니다). 버튼 왼쪽 드롭다운에서 **입력 장치**를 선택할 수 있으며, 선택은 브라우저에 저장되어 다음 녹음에도 유지됩니다. 사용된 장치 이름은 `events.json`의 `device` 필드에 기록됩니다.
2. 녹화 중 다음 행동이 오디오 기준 시간(`t`, ms)과 함께 기록됩니다.
   - **open**: 사이드바에서 파일을 열람
   - **copy**: 텍스트 선택 후 `Ctrl+C`(⌘C) 또는 복사 버튼 사용 — 파일 내 라인 번호와 소스 기준 텍스트 저장 (마크다운은 렌더링 결과가 아닌 마크다운 코드 기준)
   - **focus**: 본문 클릭 — 클릭한 라인(코드뷰어) 또는 블록(마크다운)이 하이라이트되고 라인 번호가 저장됨
   - **word**: 본문 더블클릭 — 선택된 텍스트(단어)가 저장됨. 여백 등을 더블클릭해 선택된 텍스트가 없으면 클릭 위치 뒤의 첫 단어를 찾아 저장하고 그 단어를 선택 상태로 표시
3. 버튼을 다시 누르면 저장됩니다: `mdview-recordings/rec-<timestamp>/` (mdview를 실행한 터미널의 현재 디렉토리 기준)
   - `audio.webm` (브라우저에 따라 `.m4a`/`.ogg`) — 녹음 오디오
   - `events.json` — 이벤트 타임라인

`events.json` 예시:

```json
{
  "version": 1,
  "mode": "folder",
  "root": "/path/to/project",
  "startedAt": "2026-07-18T23:00:00.000Z",
  "savedAt": "2026-07-18T23:05:00+09:00",
  "durationMs": 300000,
  "audio": "audio.webm",
  "events": [
    { "t": 0, "type": "open", "file": "docs/guide.md" },
    { "t": 5200, "type": "focus", "file": "docs/guide.md", "line": 42, "lineEnd": 45, "text": "..." },
    { "t": 7300, "type": "word", "file": "docs/guide.md", "line": 43, "lineEnd": 43, "text": "오케스트레이터" },
    { "t": 9100, "type": "copy", "file": "main.go", "line": 10, "lineEnd": 14, "text": "<소스 라인>", "selection": "<복사된 문자열>" }
  ]
}
```

SmartMD 전처리로 라인 번호가 원본과 어긋나는 블록은 원본 텍스트 탐색으로 보정되며,
보정에 실패하면 `"approx": true`로 표시됩니다.

#### 마이크가 녹음되지 않을 때

녹음은 브라우저(getUserMedia)가 수행하므로 mdview 바이너리가 아니라 **브라우저의 마이크 권한**이 필요합니다.

1. 녹화 중 상단의 **입력 레벨 미터**(녹색 바)가 움직이는지 확인 — 움직이지 않으면 소리가 들어오지 않는 상태입니다.
2. 주소창의 사이트 권한 아이콘에서 `localhost:8642`의 마이크를 **허용**으로 변경 후 새로고침.
3. macOS: 시스템 설정 → 개인정보 보호 및 보안 → **마이크**에서 사용 중인 브라우저(Chrome/Safari 등)를 켜고 브라우저를 재시작.
4. 권한이 차단된 상태거나 장치가 없으면 녹화 버튼을 눌렀을 때 원인별 안내 토스트가 표시됩니다.

### 전사 (음성 → 텍스트)

**자동 전사**: API 키가 설정되어 있으면 녹화 저장 직후 서버가 백그라운드에서 자동으로 전사합니다.
툴바에 "전사 중" 칩이 표시되고, 완료되면 토스트의 **열기** 버튼으로 `transcript.md`를 바로 볼 수 있습니다.
(키가 없거나 오디오가 25MB를 넘으면 자동 전사가 생략되고 수동 명령이 안내됩니다. 상태 API: `GET /api/recording/status`)

수동 실행:

```bash
mdview transcribe mdview-recordings/rec-20260718-230000
mdview transcribe -lang ko <recording-dir>       # 언어 고정 (기본: 자동 감지)
mdview transcribe -model large-v3 <recording-dir>  # 로컬 whisper CLI 사용
```

- 기본 모델은 OpenAI Audio Transcriptions API의 **`whisper-1`**이며, `verbose_json` 응답의 세그먼트 타임스탬프(start/end)를 이벤트 타임라인과 병합합니다.
- API 키 설정 (우선순위: 환경변수 → 설정 파일):
  - 환경변수: `OPENAI_API_KEY`
  - 설정 파일: `~/.config/mdview/config.json` — 프로젝트 루트의 `config.json`을 `./install.sh`가 복사해 줍니다.
    ```json
    { "openai_api_key": "sk-..." }
    ```
- 결과: 녹화 디렉토리에 whisper 원본 결과(`audio.json`)와 발화·참조 이벤트가 시간순으로 병합된 `transcript.json`이 생성됩니다. AI가 그대로 해석할 수 있도록 발화(`kind: "speech"`)와 사용자 행동(`kind: "action"`)을 명시적으로 구분한 단일 타임라인이며, 행동 이름도 실제 동작 그대로(`open` 열람 / `click` 클릭 / `select` 더블클릭 선택 / `copy` 복사) 표기됩니다. 각 필드의 의미는 문서 안의 `guide` 항목에 자체 기술되어 있습니다.
- `audio.json`이 이미 있으면 API를 다시 호출하지 않고 재사용합니다.
- OpenAI API의 오디오 업로드 상한은 25MB입니다 (약 40분 분량의 webm/opus 녹음).
- 오프라인 전사가 필요하면 `-model large-v3`로 로컬 **openai-whisper** CLI(`openai/whisper-large-v3`)를 사용할 수 있습니다. 요구사항: `pip install -U openai-whisper` + `ffmpeg` (macOS: `brew install ffmpeg`).

`transcript.json` 타임라인 예시:

```json
{
  "timeline": [
    { "kind": "speech", "startMs": 4000, "endMs": 9000, "text": "본문 세 번째 줄에 있는 내용이 중요합니다." },
    { "kind": "action", "action": "click", "atMs": 6000, "file": "doc.md", "line": 3, "lineEnd": 3, "text": "본문 한 줄" },
    { "kind": "speech", "startMs": 9000, "endMs": 13000, "text": "마지막으로 복사한 메인 함수 코드를 확인해 주세요." },
    { "kind": "action", "action": "copy", "atMs": 11000, "file": "main.go", "line": 3, "lineEnd": 5, "text": "<소스 라인>", "selection": "<복사된 문자열>" }
  ]
}
```

## 상세 로깅

디버깅용 2계층 로깅 시스템을 내장합니다. 환경변수로 켭니다:

```bash
DEBUG_MODE=true LOG_TO_FILE=true LOG_LEVEL=DEBUG mdview
```

| 환경변수 | 기본값 | 설명 |
|---------|--------|------|
| `DEBUG_MODE` | `false` | `true`여야 텍스트 로그가 출력됨 |
| `LOG_TO_FILE` | `false` | `true`여야 파일(세션/아티팩트)에 기록됨 |
| `LOG_DIR` | `logs` | 로그 디렉토리 경로 |
| `LOG_LEVEL` | `INFO` | DEBUG/INFO/WARN/ERROR |

- **1계층 (텍스트 로그)**: 콘솔(200자 절삭)과 세션 `.log` 파일에 시간순 한 줄 메시지.
- **2계층 (상세 아티팩트)**: 한 줄로 담을 수 없는 데이터를 카테고리별 파일로 저장.
  파일명 `{시각}_{세션ID}_{카테고리}_{순번}_{ok|err}.md` — 목록만 봐도 순서·성공/실패가 보입니다.
- 세션 종료 시 프로세스 플로우 **Mermaid 다이어그램**이 자동 생성됩니다 (노드 클릭 → 해당 아티팩트로 이동).
  `mdview`로 `logs/` 디렉토리를 열면 다이어그램을 바로 렌더링해 볼 수 있습니다.
- 7일 지난 로그는 세션 시작 시 자동 삭제됩니다.

```
logs/
  └── 20260719-004740-001/                  <- 세션 디렉토리
      ├── 20260719-004740_f605e6ed.log      <- 1계층 세션 텍스트 로그
      ├── whisper/                          <- 전사 호출 상세 (요청/응답/에러)
      ├── recording/                        <- 녹화 업로드 상세 (이벤트 요약)
      ├── state/                            <- 상태 스냅샷 (JSON)
      └── process/                          <- 세션 프로세스 다이어그램 (Mermaid)
```

## 프로젝트 구조

```
cli/mdview/
  install.sh                 # 빌드 및 설치 스크립트
  cmd/
    main.go                  # Orchestrator (CLI, 서브커맨드, 와이어링)
  internal/
    mdview/
      server.go              # HTTP / WebSocket Gateway
      hub.go                 # WebSocket 브로드캐스트 Worker
      watcher.go             # 파일/디렉토리 감시 Worker
      browser.go             # 브라우저 오픈 Gateway
      tree.go                # 디렉토리 트리 구성 Worker
      filetypes.go           # 뷰어 대상 파일 타입 정책
      recording.go           # 녹화(오디오+이벤트) 저장 Worker
      transcribe.go          # 전사(whisper-1 API/로컬 CLI) 및 transcript.md 생성 Orchestrator
      config.go              # 사용자 설정 (~/.config/mdview/config.json)
      logging/               # 2계층 로깅 (Logger 싱글톤 + Mermaid 렌더러)
        logger.go
        process_event.go
      types.go               # 공유 타입
      static/                # 임베딩되는 프론트엔드 정적 파일
        index.html           # 툴바(녹화 버튼) 포함 레이아웃
        css/
        js/
          recorder.js        # 녹음 + 이벤트 타임라인 기록 Worker
          ...
```

## 아키텍처 메모

- `cmd/main.go`가 Orchestrator로서 Hub / Watcher / Server / Browser를 배선합니다.
- Worker는 상위 객체를 참조하지 않고 콜백(`OnChange`)을 통해 이벤트를 보고합니다.
- 파일 경로 검증 로직(`ResolveRelPath`)을 통해 폴더 모드에서의 path traversal을 차단합니다.

## 요구사항

- Go 1.24 이상
- macOS / Linux / Windows
