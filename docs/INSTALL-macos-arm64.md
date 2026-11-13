# mdview Installation and Usage Guide — Apple Silicon Mac

## 1. Before you install

- **Supported hardware:** Apple Silicon Macs (M1, M2, M3, M4, and later Apple Silicon models).
- **Operating system:** macOS 13 Ventura or later.
- **Release file:** `mdview-1.0.4-macos-arm64.pkg`.
- **Internet:** Some diagram, code highlighting, and PDF resources are loaded from CDNs, so rendering documents requires an internet connection. Offline use is not guaranteed.
- **Nothing else to install:** Go, Homebrew, Node.js, and Xcode are not required.

This version is distributed outside the Mac App Store. The app is signed with `Developer ID Application: Mediawave (23B9Q752EU)` and the installer with `Developer ID Installer: Mediawave (23B9Q752EU)`, and both carry an Apple notarization ticket. Certificates, private keys, and the developer's API keys are not included in the release files.

## 2. Installing — no Terminal needed

1. Double-click `mdview-1.0.4-macos-arm64.pkg`.
2. In the macOS Installer, click **Continue → Install**.
3. If prompted, complete Mac administrator authentication.
4. When installation finishes, click **Close**.

The app and the CLI are installed together, and they are available to other user accounts on this Mac as well.

| Item | Location | How to run |
| --- | --- | --- |
| Desktop app | `/Applications/mdview.app` | Applications → mdview |
| Terminal command | `/usr/local/bin/mdview` | Type `mdview` in Terminal |

The CLI command is linked automatically to the executable inside the app. In the standard macOS Terminal, you don't need to edit PATH, create links manually, or launch the app first. Settings and recordings for the app and the CLI are kept separately for each user account.

The earlier 1.0.0 DMG only copied the app, so the CLI was not registered automatically. When sharing mdview with others, use the **1.0.1 or later PKG installer**.

## 3. Opening your first document

1. In the app, click **Open Folder…** and choose a project or documents folder. To open a single document, use **Open File…**.
2. Your default browser opens automatically. In folder mode, select a document from the list on the left.
3. When you edit the file, the change shows up in the browser live.

HTML files (`.html`, `.htm`) are shown as a preview with their CSS, scripts, and images applied. HTML opens in an area isolated from the viewer, and file changes are reflected automatically.

You can also drag a single file or folder onto the app window to open it, or use **right-click → Open With → mdview** on a document in Finder. The installer does not forcibly change your default app for any document type.

- If you closed the browser tab, click **Reopen Browser** in the app.
- To open another document, first click **Stop Viewer**, then choose a file or folder again.
- When you're done, quit with **Stop Viewer** or **⌘Q**. Closing only the browser tab leaves the document server running.
- Closing the app window also quits the app and the document server.

The document server is reachable only from local addresses on this Mac. The default address is `http://localhost:8642`; if that port is in use, a different port is used and shown in the app window.

### Saving as a Markdown ZIP

Open a Markdown document and click **Export → Save as Markdown ZIP…** at the top. A ZIP containing the entire current document is downloaded.

- `document.md`: the document with navigation (`navigation`) and state (`state`) diagrams converted to Mermaid code. Existing Mermaid code is kept as is.
- `images/`: job flow (`jobflow`) PNGs, referenced from `document.md` via relative paths.

Extract the ZIP and keep `document.md` and `images/` in the same folder structure. Open `document.md` in any Markdown viewer that supports Mermaid to see the converted diagrams and the job flow images together. Your original document file is not modified.

## 4. Using recording and transcription

### Recording

1. Choose a microphone from the input device list at the top of the browser.
2. Press the record button and allow the browser's microphone request.
3. Press the record button again to save. Wait until transcription and prompt processing finish.
4. Stop or quit the app only after the recording has been saved and post-processing has finished.

**Save location:** Both the app and the CLI save to `~/Library/Application Support/mdview/mdview-recordings/rec-<timestamp>/`, regardless of the working folder.

Recordings are deleted automatically 7 days after they are saved. Copy any recordings you want to keep to another folder. When transcription finishes, the full contents of `transcript.json` are copied to the clipboard automatically and a system notification lets you know.

The audio file (such as `audio.webm`), `events.json`, and `prompt.txt` are saved. Even without an API key, you can record, save the action log, and generate a `prompt.txt` based on the action log.

If you quit the app while recording, audio still held in the browser's memory is not saved. Always save the recording in the browser first. Transcription and prompt post-processing that have already started are allowed to finish before the app quits, so quitting may take a moment.

### Speech transcription setup — optional

To convert speech to text, set your own OpenAI API key.

1. Click the **gear icon (Settings) at the bottom left** of the mdview web UI. It's in the same place in both file mode and folder mode.
2. Enter your key in **OpenAI API key**.
3. Choose a **Transcription model**. The current default is **Whisper 1 (`whisper-1`)**.
4. Click **Save**. It takes effect from the next recording's transcription, without restarting the app.

A saved key is never sent back to the page; it is only shown as "Set". If you leave the key field empty and save, the existing key is kept. To change the key, enter a new one and save; to delete it, click **Delete saved key → Save**. Close or Esc discards unsaved changes.

Settings are stored in `~/.config/mdview/config.json` in your user account, and the API key file is saved with permissions that allow only that user to read and write it (0600). The key is never kept in browser storage. The desktop app and the CLI share settings when run under the same account.

| Model | Transcription result |
| --- | --- |
| Whisper 1 (`whisper-1`, default) | Per-utterance timing recorded on the timeline together with document actions |
| GPT Transcribe (`gpt-transcribe`) | Full transcript saved, no per-utterance timing |
| GPT-4o Transcribe (`gpt-4o-transcribe`) | Full transcript saved, no per-utterance timing |
| GPT-4o mini Transcribe (`gpt-4o-mini-transcribe`) | Full transcript saved, no per-utterance timing |

For models without timing information, the transcript is saved in a separate "Speech Transcript (No Timing)" section of `prompt.txt` and in `untimedSpeech` in `transcript.json`. Document action timestamps are kept as is. The API response format for each model follows the [official OpenAI transcription documentation](https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create).

When the `OPENAI_API_KEY` environment variable is set for the CLI, the key from the environment variable takes precedence; the settings dialog points this out as well. Apps launched from Finder usually don't receive Terminal environment variables, so saving the key in the web settings is simpler.

When you use speech transcription, the recorded audio is sent to the OpenAI API and API usage fees are charged to your account. If there's no key or the audio exceeds the upload size limit, transcription is skipped and the prompt is built from the action log alone. You can still view documents and record without a key.

## 5. Using it directly from Terminal

Once the PKG installation finishes, you can run mdview from Terminal right away. You don't need to launch the app first.

```bash
# Check installation and version
mdview --version

# Browse the current directory
mdview

# Open a file
mdview README.md

# Specify a port
mdview -port 9000 README.md
```

Quit the CLI with **Ctrl+C**. Recordings started from the CLI are also saved to `~/Library/Application Support/mdview/mdview-recordings/`.

If the command can't be found, check the following in a new Terminal window.

```bash
rehash
command -v mdview
mdview --version
```

On a correct installation, `/usr/local/bin/mdview` is shown. If `type -a mdview` shows an older development install (such as `~/.local/bin/mdview`) being picked first, remove the old executable and check again. In unusual shell environments where PATH has been restricted manually, run `/usr/local/bin/mdview --version` and make sure that shell's PATH includes `/usr/local/bin`.

## 6. Updating and uninstalling

### Updating

1. Save any recording in the browser and wait for post-processing to finish.
2. Quit the mdview app and any CLI instances you started separately.
3. Run the new PKG installer. The app and the CLI link are updated together.

Config files and existing recordings live outside the app, so they are kept when the app is replaced. There is no automatic update feature.

### Uninstalling

1. Quit the mdview app and the CLI.
2. Move `mdview.app` from the Applications folder to the Trash.
3. Delete the automatically installed CLI link. Check its target first.

```bash
ls -l /usr/local/bin/mdview
# Only if the output is a link pointing to /Applications/mdview.app/Contents/MacOS/mdview:
sudo rm /usr/local/bin/mdview

# To also remove the install receipt:
sudo pkgutil --forget camp.tools.mdview.pkg
```

Personal settings remain in `~/.config/mdview` (including the `chrome-profile` created by the web-opening feature in earlier versions), and recordings remain in `~/Library/Application Support/mdview/mdview-recordings`. Recordings saved by earlier versions remain in the `mdview-recordings` folder inside whichever folder was open at the time. To delete this data as well, locate it in Finder, back up any files you need, and delete them yourself.

## 7. Troubleshooting

| Symptom | What to check |
| --- | --- |
| Doesn't run on an Intel Mac | This release is for Apple Silicon only. Intel Macs are not supported. |
| macOS version not supported | Check in "About This Mac" that you're running macOS 13 or later. |
| `zsh: command not found: mdview` | Make sure you installed with the 1.0.1 or later PKG. Copying the app from the older DMG doesn't register the command. Check `command -v mdview` in a new Terminal window. |
| Browser doesn't open | Go directly to the `http://localhost:<port>` shown in the app, or click **Reopen Browser**. |
| Document is blank or PDFs/diagrams don't appear | Check your internet connection and whether your network (e.g. a corporate network) allows access to `cdn.jsdelivr.net` and `cdnjs.cloudflare.com`. |
| Microphone isn't recording | Check the browser's microphone permission for localhost, and the browser's permission under System Settings → Privacy & Security → Microphone. |
| Recording fails to save | Check write permission for `~/Library/Application Support/mdview` and free disk space. |
| Only transcription fails | Check your API key and transcription model in the settings at the bottom left, and check your API availability and the audio size. Viewing documents and recording work without a key. |
| "Unidentified developer" or "damaged app" warning | Make sure you have the official release (without `unnotarized` in its name) and download it again. Rather than turning off security features, verify the signature and notarization as shown below. |
| Stuck in the "quitting" state | Wait for in-progress transcription and prompt processing to finish. Force-quitting may lose results that are still being processed. |

You can verify the integrity of the downloaded files against `SHA256SUMS` in the release folder.

```bash
cd "path/to/downloaded/release/folder"
shasum -a 256 -c SHA256SUMS
```

Verify the app signature and Apple notarization:

```bash
codesign --verify --deep --strict --verbose=2 /Applications/mdview.app
spctl --assess --type execute --verbose=2 /Applications/mdview.app
```

A genuine official release shows `accepted`, `source=Notarized Developer ID`, and `origin=Developer ID Application: Mediawave (23B9Q752EU)`. On first launch, the usual macOS confirmation dialog for downloaded apps may still appear.
