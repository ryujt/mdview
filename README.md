# mdview

Preview Markdown, code, HTML, and PDF files in your browser — live, as you edit.
Record a voice explanation while you browse, and mdview turns it into a timestamped transcript and an AI-ready prompt.

## Features

- **Live preview** — files re-render automatically when they change.
- **Two modes** — `mdview <file>` opens one file; `mdview` opens the current folder with a file-tree sidebar.
- **Many file types** — Markdown (GFM), HTML (isolated preview), PDF (built-in viewer), and source/config files (syntax highlighting + line numbers).
- **Diagrams** — `mermaid`, `state`, `navigation`, `layout`, `jobflow` code blocks.
- **Export** — **Export ▾ → Save as PDF…** or **Save as Markdown ZIP…** (custom diagrams converted to Mermaid or PNG).
- **Voice recording** — records your voice plus what you open, click, select, and copy.
- **Transcription** — OpenAI `whisper-1` by default; produces `transcript.json` and `prompt.txt`.

## Quick start

### Install

| Platform | How |
| --- | --- |
| macOS (Apple Silicon, 13+) | Run `mdview-<version>-macos-arm64.pkg`. Installs the app and the `mdview` CLI. See the [install guide](docs/INSTALL-macos-arm64.md). |
| Windows 10/11 x64 | Run `mdview-<version>-windows-x64-setup.exe`. See [packaging/windows/INSTALL.txt](packaging/windows/INSTALL.txt). |
| From source | `./install.sh` (requires Go 1.26+). Installs to `~/.local/bin`. |

```bash
./install.sh                     # test + vet + build + install
./install.sh --build-only        # build only
PREFIX=/usr/local ./install.sh   # install to $PREFIX/bin
```

### Run

```bash
mdview README.md        # single file
mdview                  # current folder
mdview -port 9000       # custom port (default 8642)
mdview -version
```

Your browser opens automatically. Press `Ctrl+C` to stop.

> The port stays at 8642 so the browser remembers microphone permission between runs. If it's taken, the next free port is used.

Rendering loads libraries (marked, Mermaid, highlight.js) from a CDN, so an internet connection is required.

## Recording and transcription

1. Click **⏺** in the toolbar (choose a microphone from the dropdown next to it).
2. Browse and talk. mdview logs what you **open**, **click**, **double-click**, and **copy**, with line numbers (or page and position for PDFs).
3. Click again to stop. mdview saves the recording, transcribes it in the background, and copies `transcript.json` to the clipboard when done.

Recordings are saved here and deleted after **7 days**:

- macOS: `~/Library/Application Support/mdview/mdview-recordings/rec-<timestamp>/`
- Linux: `${XDG_CONFIG_HOME:-~/.config}/mdview/mdview-recordings/rec-<timestamp>/`

| File | Contents |
| --- | --- |
| `audio.webm` | Recorded audio (`.m4a`/`.ogg` on some browsers) |
| `events.json` | Your actions, timed against the audio |
| `transcript.json` | Speech and actions merged into one timeline |
| `prompt.txt` | Plain-text timeline to paste into an AI (always created) |

### OpenAI API key

Set it in the web UI (the gear button at the bottom left) or with either of these:

- Environment variable `OPENAI_API_KEY`
- `~/.config/mdview/config.json`: `{ "openai_api_key": "sk-..." }` — `install.sh` copies a `config.json` from the repo root here (see `config.example.json`)

Without a key (or if the audio exceeds 25 MB), transcription is skipped and `prompt.txt` is built from your actions alone.

### Manual transcription

```bash
mdview transcribe <recording-dir>
mdview transcribe -lang en <recording-dir>         # set the language (default: auto-detect)
mdview transcribe -model large-v3 <recording-dir>  # local whisper CLI (pip install -U openai-whisper; needs ffmpeg)
```

**Microphone not working?** Allow the microphone for `localhost:8642` in your browser, and on macOS enable your browser under System Settings → Privacy & Security → Microphone. More in the [install guide](docs/INSTALL-macos-arm64.md).

## Debug logging

```bash
DEBUG_MODE=true LOG_TO_FILE=true LOG_LEVEL=DEBUG mdview
```

| Variable | Default | Effect |
| --- | --- | --- |
| `DEBUG_MODE` | `false` | Print text logs |
| `LOG_TO_FILE` | `false` | Write session logs and detailed artifacts to files |
| `LOG_DIR` | `logs` | Log directory |
| `LOG_LEVEL` | `INFO` | `DEBUG` / `INFO` / `WARN` / `ERROR` |

Each session writes a `.log` file, per-category detail files, and a Mermaid process diagram. Logs older than 7 days are removed.

## Development

```bash
go test ./...
go vet ./...
node --test test/*.test.mjs
```

```
cmd/main.go                 # CLI entry point; wires the components together
internal/mdview/
  server.go                 # HTTP + WebSocket server
  watcher.go, hub.go        # file watching and change broadcast
  tree.go, filetypes.go     # folder tree and supported file types
  recording*.go             # saving, completion notice, 7-day cleanup
  pipeline.go               # save → transcribe → prompt.txt
  transcribe.go, prompt.go  # transcription and prompt generation
  config.go, settings.go    # user settings (~/.config/mdview/config.json)
  logging/                  # debug logging
  static/                   # embedded web UI (HTML/CSS/JS)
packaging/                  # macOS and Windows release builds
```

## Docs

- [macOS install guide](docs/INSTALL-macos-arm64.md) — install, recording setup, transcription models, update, uninstall
- [macOS release build](packaging/macos/README.md) — signing and notarization
- [Windows install](packaging/windows/INSTALL.txt) · [Windows build](packaging/windows/BUILD.txt)
