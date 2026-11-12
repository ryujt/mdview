# mdview

mdview is a CLI tool that renders Markdown files live in your browser.
It can watch a single file or let you browse the entire current directory from a sidebar.

## Features

- **File mode** (`mdview <file>`) opens a file in your browser and reflects changes as they happen. Markdown is rendered, and source and configuration files open in the code viewer.
- **Folder mode** (`mdview`) shows a project tree rooted at the current directory in the left sidebar. In addition to Markdown (`.md`), it lists text (`.txt`), PDF (`.pdf`), source code (`.go`, `.js`, `.py`, and others), and configuration files (YAML, JSON, `.env`, `Dockerfile`, and others). Click a file to open it in the main pane. The viewer refreshes automatically when the selected file changes.
  - Expanding a folder loads only its immediate contents. This keeps the initial load light even for large projects and refreshes the listing when you expand the folder again.
  - Click the active file again to clear the selection and return to the initial view.
  - The viewer remembers each document's scroll position. A document you open for the first time starts at the top.
- Markdown files render as formatted documents. HTML (`.html`, `.htm`) opens in an isolated preview with CSS, scripts, and images, and refreshes as the file changes. Source code and configuration files open with syntax highlighting and line numbers. The built-in PDF viewer (PDF.js) supports text selection.
- Standard Markdown (GFM), with syntax highlighting and copy buttons for code blocks
- Custom diagrams including `mermaid`, `state`, `navigation`, `layout`, and `jobflow`
- **Audio recording** lets you narrate a document or code from the toolbar. File opens, text copies, and line clicks are saved as JSON events aligned to the audio timeline. Choose a microphone from the input dropdown and monitor the input level while recording.
- **Save as Markdown ZIP** from **Export ▾ → Save as Markdown ZIP…** while a Markdown document is open. The archive contains the full file as `document.md`. Navigation (`navigation`) and state (`state`) diagrams are converted to Mermaid. Job flows (`jobflow`) become PNG files under `images/` and are referenced by relative paths. Existing Mermaid blocks are preserved. Extract the archive, keep its folder structure, and open `document.md` in a Markdown viewer that supports Mermaid.
- **Save as PDF** from **Export ▾ → Save as PDF…** or with ⌘P / Ctrl+P. For Markdown and code, select **Save as PDF** in the browser's print dialog. HTML prints the source document without the viewer controls. PDF files download as-is.
- **Prompt generation** always creates a `prompt.txt` file after saving a recording. It lists speech and document actions in chronological order. With no API key, it contains only the actions. Select **Open prompt.txt** in the completion notification to view it and paste its contents into an AI tool.
- **Transcription** runs automatically when a recording is saved and an API key is available. OpenAI's `whisper-1` API returns speech segment timestamps, which mdview merges with reference events in `transcript.json`. On completion, mdview copies the full file to the system clipboard and displays a system notification and an in-app notification. A toolbar status indicator shows progress. Run `mdview transcribe` to transcribe manually.
- **OpenAI settings** let you save an API key and transcription model from the gear icon at the bottom left. The default remains `whisper-1`, and changes apply to the next recording. The saved key stays in the user configuration file (0600 permissions) and is never sent back to the browser.
- **Detailed logging** can be enabled with `DEBUG_MODE` and `LOG_TO_FILE`. It includes text logs, detailed artifacts by category, and session process diagrams.

## Install on Apple Silicon Macs

Double-click `mdview-1.0.4-macos-arm64.pkg` and complete the installation to install both the app and the CLI.
Launch mdview from Applications or type `mdview` in Terminal.
The installer links the CLI at `/usr/local/bin/mdview` so other users on the Mac can run it too.
The package supports Apple Silicon Macs running macOS 13 or later and requires no Go, Homebrew, or Terminal setup.
Document rendering currently requires an internet connection to load CDN resources.

See the [installation guide](docs/INSTALL-macos-arm64.md) for setup, recording, CLI usage, updates, and removal.
The [macOS release build guide](packaging/macos/README.md) covers rebuilding the distribution package, including Developer ID signing and Apple notarization.

## Build and install from source

```bash
cd cli/mdview
./install.sh                # Test, vet, build, install to ~/.local/bin, and copy the configuration
./install.sh --build-only   # Build only
PREFIX=/usr/local ./install.sh   # Set the installation directory ($PREFIX/bin)
```

If the repository root contains a Git-ignored `config.json` (see `config.example.json`),
installation copies it to `~/.config/mdview/config.json` with 0600 permissions.

## Run mdview

```bash
# Single-file mode
mdview README.md

# Folder mode (current directory)
mdview

# Set the port (defaults to 8642, tries subsequent ports if busy, or picks an available port with 0)
mdview -port 9000 README.md
```

The browser opens automatically. Press `Ctrl+C` to quit.

Browsers remember microphone permissions per `localhost:<port>` origin. The default port of 8642
allows that permission to carry over between runs. A different port may trigger a new permission prompt.

## Audio recording and transcription

Record spoken requirements or questions while viewing documents or code. The recording includes the
text locations you referenced so you can share the context with an AI tool or a colleague.

### Record audio

1. Click ⏺ in the toolbar to start recording from your microphone. The button changes to a stop icon, and a timer and input level appear. Choose an **input device** from the dropdown to the left of the button. The browser remembers your selection for the next recording. The device name is recorded in the `device` field of `events.json`.
2. The following actions are recorded with their time relative to the audio (`t`, in milliseconds).
   - **open**: Open a file from the sidebar.
   - **copy**: Select text and use `Ctrl+C` (⌘C) or the copy button. The event records line numbers and text from the source file. For Markdown, the source text is the Markdown markup.
   - **focus**: Click the document body. The viewer highlights the line in code or the block in Markdown and records the line number.
   - **word**: Double-click the document body. The viewer records the selected text. If the click leaves nothing selected, such as in whitespace, the viewer finds, selects, and records the first word after that point.
   - In PDFs, a `pdf` field replaces line numbers. A focus event records the page number, coordinates (`page`, `x`, `y`) normalized to 0–1 from the top-left corner, and text near the click. Copy and double-click events record the page number and selected text.
3. Click the button again to save. File and folder modes both use a fixed location in the user's application data directory, regardless of the working directory.
   - macOS: `~/Library/Application Support/mdview/mdview-recordings/rec-<timestamp>/`
   - Linux: `${XDG_CONFIG_HOME:-~/.config}/mdview/mdview-recordings/rec-<timestamp>/`
   - `audio.webm` (`.m4a` or `.ogg` depending on the browser) contains the recorded audio.
   - `events.json` contains the event timeline.
   - `prompt.txt` contains the speech and action timeline for pasting into an AI tool. This file is always generated.

Recording folders and their contents are automatically deleted **7 days** after they are saved.
Cleanup runs on startup, before saving a recording, and hourly while mdview is running. Recordings that expire while mdview is closed are cleaned up on the next launch.
Recordings undergoing post-processing are kept until processing completes.

Example `events.json`

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
    { "t": 7300, "type": "word", "file": "docs/guide.md", "line": 43, "lineEnd": 43, "text": "orchestrator" },
    { "t": 9100, "type": "copy", "file": "main.go", "line": 10, "lineEnd": 14, "text": "<source lines>", "selection": "<copied text>" },
    { "t": 12000, "type": "open", "file": "docs/spec.pdf" },
    { "t": 13500, "type": "focus", "file": "docs/spec.pdf", "pdf": { "page": 2, "x": 0.41, "y": 0.27 } },
    { "t": 15000, "type": "copy", "file": "docs/spec.pdf", "pdf": { "page": 2 }, "selection": "<copied text>" }
  ]
}
```

When SmartMD preprocessing shifts line numbers, mdview searches the original text to correct them.
If the search fails, mdview marks the position with `"approx": true`.
When generating `prompt.txt` and `transcript.json`, mdview skips unknown event types,
including the web action events (`web-*`) that may appear in recordings saved by older versions.

#### Troubleshoot microphone input

The browser records audio through `getUserMedia`, so grant microphone permission to the **browser**.

1. Check whether the **input level meter** (green bar) in the toolbar moves while recording. A stationary meter indicates there is no audio input.
2. Use the site permissions icon in the address bar to set microphone access for `localhost:8642` to **Allow**, then reload.
3. On macOS, go to System Settings → Privacy & Security → **Microphone**, enable access for your browser (such as Chrome or Safari), and restart it.
4. If access is blocked or no microphone is available, clicking Record displays a notification explaining the cause.

### Transcribe speech to text

**Automatic post-processing** starts as soon as the recording is saved. The server generates `prompt.txt` in the background and transcribes audio up to 25 MB when an API key is available.
The toolbar shows a **Post-processing** indicator. After creating `transcript.json`, mdview **copies the full JSON to the clipboard**.
Check the system and in-app notifications, then paste the result where you need it. On macOS, this works even when the browser is out of focus because mdview uses `pbcopy` and `osascript`.
If copying fails, select **Copy transcript.json** in the notification to retry. Select **Open prompt.txt** to view the saved prompt.
With no key or audio larger than 25 MB, mdview skips transcription and builds `prompt.txt` from the actions alone. The status endpoint is `GET /api/recording/status`.

To run transcription manually, use the commands below. `mdview transcribe` also regenerates `prompt.txt` regardless of whether transcription runs.

```bash
mdview transcribe "$HOME/Library/Application Support/mdview/mdview-recordings/rec-20260718-230000"
mdview transcribe -lang ko <recording-dir>       # Set the language (default: auto-detect)
mdview transcribe -model large-v3 <recording-dir>  # Use the local Whisper CLI
```

- The default model is **`whisper-1`**. You can select a model in the settings at the bottom left. For `whisper-1`, mdview merges the segment start and end timestamps from the `verbose_json` response with the event timeline.
- Go to **Settings → OpenAI API Key and Transcription Model → Save** at the bottom left. mdview checks the environment variable first, then the user configuration file. GPT transcription models save the full transcript separately without timestamps. See [optional speech transcription setup](docs/INSTALL-macos-arm64.md#optional-speech-transcription-setup).
  - Environment variable: `OPENAI_API_KEY`
  - Configuration file: `~/.config/mdview/config.json`. `./install.sh` copies it from `config.json` at the project root.
    ```json
    { "openai_api_key": "sk-..." }
    ```
- The recording directory contains the raw Whisper result (`audio.json`) and `transcript.json`, a single chronological timeline of speech (`kind: "speech"`) and user actions (`kind: "action"`) that an AI tool can interpret directly. Action names reflect what happened (`open`, `click`, `select` for double-click selection, and `copy`). The `guide` field describes the fields.
- If `audio.json` already exists, mdview reuses it without calling the API again.
- The OpenAI API audio upload limit is 25 MB, approximately 40 minutes of WebM/Opus audio.
- For offline transcription, use `-model large-v3` with the local **openai-whisper** CLI (`openai/whisper-large-v3`). Install it with `pip install -U openai-whisper` and install `ffmpeg` (on macOS, `brew install ffmpeg`).

Example `transcript.json` timeline

```json
{
  "timeline": [
    { "kind": "speech", "startMs": 4000, "endMs": 9000, "text": "The third line of the document is important." },
    { "kind": "action", "action": "click", "atMs": 6000, "file": "doc.md", "line": 3, "lineEnd": 3, "text": "A line from the document" },
    { "kind": "speech", "startMs": 9000, "endMs": 13000, "text": "Please review the main function I just copied." },
    { "kind": "action", "action": "copy", "atMs": 11000, "file": "main.go", "line": 3, "lineEnd": 5, "text": "<source lines>", "selection": "<copied text>" }
  ]
}
```

## Detailed logging

Enable the two logging layers for debugging with environment variables.

```bash
DEBUG_MODE=true LOG_TO_FILE=true LOG_LEVEL=DEBUG mdview
```

| Environment variable | Default | Description |
|---------|--------|------|
| `DEBUG_MODE` | `false` | Set to `true` to enable text logging |
| `LOG_TO_FILE` | `false` | Set to `true` to write session logs and artifacts to files |
| `LOG_DIR` | `logs` | Log directory path |
| `LOG_LEVEL` | `INFO` | DEBUG/INFO/WARN/ERROR |

- **Layer 1 (text logs)** writes chronological one-line messages to the console (truncated to 200 characters) and to a session `.log` file.
- **Layer 2 (detailed artifacts)** writes data that requires more than one line to files organized by category.
  The filename `{timestamp}_{sessionID}_{category}_{sequence}_{ok|err}.md` shows the order and outcome in a directory listing.
- When a session ends, mdview generates a **Mermaid process flow diagram**. Click a node to open its artifact.
  Open the `logs/` directory with `mdview` to render the diagram.
- At session startup, mdview deletes logs older than 7 days.

```
logs/
  └── 20260719-004740-001/                  <- Session directory
      ├── 20260719-004740_f605e6ed.log      <- Layer 1 session text log
      ├── whisper/                          <- Transcription call details (request, response, error)
      ├── recording/                        <- Recording upload details (event summary)
      ├── state/                            <- State snapshots (JSON)
      └── process/                          <- Session process diagram (Mermaid)
```

## Project structure

```
cli/mdview/
  install.sh                 # Build and installation script
  cmd/
    main.go                  # Orchestrator (CLI, subcommands, wiring)
  internal/
    mdview/
      server.go              # HTTP / WebSocket Gateway
      hub.go                 # WebSocket broadcast worker
      watcher.go             # File and directory watcher
      browser.go             # Browser launch gateway
      tree.go                # Directory tree worker
      filetypes.go           # Policy for supported file types
      recording.go           # Recording storage worker (audio and events)
      recording_retention.go # Fixed storage location and 7-day retention
      recording_completion.go # Transcript clipboard copy and system notifications
      transcribe.go          # Transcription (whisper-1 API or local CLI) and transcript.json generation
      config.go              # User configuration (~/.config/mdview/config.json)
      pipeline.go            # Save, transcribe when possible, then generate prompt.txt
      prompt.go              # prompt.txt generation (pure function)
      logging/               # Two-layer logging (Logger singleton and Mermaid renderer)
        logger.go
        process_event.go
      types.go               # Shared types
      static/                # Embedded frontend assets
        index.html           # Layout with toolbar, export menu, and recording button
        css/
        js/
          recorder.js        # Audio recording and event timeline worker
          pdf-export.js      # Toolbar export controls for PDF and Markdown ZIP
          markdown-export.js # Mermaid conversion, job flow PNGs, and ZIP creation
          ...
```

## Architecture notes

- `cmd/main.go` orchestrates the Hub, Watcher, Server, and Browser.
- Workers report events through callbacks (`OnChange`) without referencing their parent objects.
- `ResolveRelPath` validates file paths to prevent path traversal in folder mode.

## Requirements

- Go 1.26 or later (`go 1.26` in go.mod, required by cdproto). With `GOTOOLCHAIN=auto`, an older local Go installation downloads the required toolchain on the first build.
- macOS / Linux / Windows
