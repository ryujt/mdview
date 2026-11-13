# mdview installation and user guide for Apple Silicon Macs

## 1. Before you install

- **Supported hardware** includes Apple Silicon Macs with an M1, M2, M3, M4, or later Apple Silicon chip.
- **Operating system** must be macOS 13 Ventura or later.
- **Installer** is `mdview-1.0.4-macos-arm64.pkg`.
- **Internet access** is required to render documents because some diagram, syntax highlighting, and PDF resources load from CDNs. Offline use is unsupported.
- **Dependencies** are bundled where needed. You can install without Go, Homebrew, Node.js, or Xcode.

This app is distributed outside the Mac App Store. The app is signed with `Developer ID Application: Mediawave (23B9Q752EU)` and the installer with `Developer ID Installer: Mediawave (23B9Q752EU)`. Both carry Apple notarization tickets. The distribution excludes certificates, private keys, and the developer's API key.

## 2. Install without Terminal

1. Double-click `mdview-1.0.4-macos-arm64.pkg`.
2. In the macOS installer, click **Continue → Install**.
3. Authenticate as a Mac administrator if prompted.
4. Click **Close** when installation is complete.

The installer adds both the app and CLI. Other user accounts on the Mac can use them too.

| Component | Installation path | How to launch |
| --- | --- | --- |
| Desktop app | `/Applications/mdview.app` | Open mdview from Applications |
| Terminal command | `/usr/local/bin/mdview` | Type `mdview` in Terminal |

The CLI command links to the executable inside the app. In a standard macOS Terminal, you can use it immediately without editing PATH, creating a link, or launching the app first. Settings and recordings are stored per user account.

The older 1.0.0 DMG only copied the app, so it did not register the CLI. Use a **PKG installer for 1.0.1 or later** when distributing mdview to others.

## 3. Open your first document

1. In the app, click **Open Folder…** and select a project or document folder. Use **Open File…** to select a single document.
2. Your default browser opens automatically. In folder mode, select a document from the list on the left.
3. Changes to the file appear in the browser as you edit it.

HTML (`.html`, `.htm`) opens in an isolated preview with its CSS, scripts, and images. The preview refreshes automatically when the file changes.

You can also drag a file or folder into the app window, or right-click a document in Finder and choose **Open With → mdview**. The installer leaves your default file associations in place.

- If you closed the browser tab, click **Reopen Browser** in the app.
- To open another document, click **Stop Viewer** and select another file or folder.
- When finished, click **Stop Viewer** or press **⌘Q** to quit. Closing only the browser tab leaves the document server running.
- Closing the app window exits the app and stops the document server.

The document server is accessible only from this Mac. The default address is `http://localhost:8642`. If that port is in use, use the address shown in the app window.

### Save as a Markdown ZIP

Open a Markdown document and choose **Export → Save as Markdown ZIP…** from the toolbar. The download contains the entire document.

- `document.md` contains the document with navigation (`navigation`) and state (`state`) diagrams converted to Mermaid. Existing Mermaid blocks are preserved.
- `images/` contains job flow (`jobflow`) PNGs referenced by relative paths in `document.md`.

Extract the ZIP and keep `document.md` and `images/` in their original folder structure. Open `document.md` in a Markdown viewer that supports Mermaid to view both the converted diagrams and the job flow images. The source document stays intact.

## 4. Record and transcribe audio

### Record audio

1. Choose a microphone from the input device list at the top of the browser view.
2. Click Record and allow the browser to use your microphone.
3. Click the recording button again to save. Wait for transcription and prompt generation to finish.
4. Stop the viewer or quit the app once saving and post-processing are complete.

**Storage location** for both the app and CLI is `~/Library/Application Support/mdview/mdview-recordings/rec-<timestamp>/`, regardless of the working folder.

Recordings are automatically deleted 7 days after they are saved. Copy any recordings you want to keep to another folder. When transcription completes, mdview copies all of `transcript.json` to the clipboard and displays a system notification.

The recording includes an audio file such as `audio.webm`, plus `events.json` and `prompt.txt`. Without an API key, you can still record audio and actions and generate `prompt.txt` from the action log.

Save the recording in the browser before quitting. Quitting while recording discards audio still in browser memory. The app waits for transcription and prompt generation already in progress, which can briefly delay shutdown.

### Optional speech transcription setup

Set your OpenAI API key to convert speech to text.

1. Click the **settings gear in the bottom-left corner** of the mdview web interface. It is in the same place in both file and folder modes.
2. Enter your key in **OpenAI API key**.
3. Choose a **Transcription model**. The default is **Whisper 1 (`whisper-1`)**.
4. Click **Save**. The changes apply to the next recording without restarting the app.

The browser shows that a key is configured without receiving its value. Leave the field empty when saving to keep your current key. To replace it, enter a new key and save. To remove it, choose **Delete saved key → Save**. Closing the dialog or pressing Esc discards unsaved changes.

Settings are stored in `~/.config/mdview/config.json` with read and write permissions for the account owner only (0600). The key stays out of browser storage. The desktop app and CLI share settings when run under the same account.

| Model | Transcription result |
| --- | --- |
| Whisper 1 (`whisper-1`, default) | Speech timestamps appear alongside document actions in the timeline |
| GPT Transcribe (`gpt-transcribe`) | Full transcript without speech timestamps |
| GPT-4o Transcribe (`gpt-4o-transcribe`) | Full transcript without speech timestamps |
| GPT-4o mini Transcribe (`gpt-4o-mini-transcribe`) | Full transcript without speech timestamps |

For models that return no timestamps, the transcript goes into a separate untimed speech section in `prompt.txt` and the `untimedSpeech` field in `transcript.json`. Document action timestamps are preserved. See the [OpenAI transcription API reference](https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create) for model response formats.

The CLI uses `OPENAI_API_KEY` when the environment variable is set. The settings dialog also explains this priority. Apps launched from Finder typically do not inherit Terminal environment variables, so saving the key in the web settings is simpler.

Transcription sends recorded audio to the OpenAI API and incurs API usage charges on your account. When the key is missing or the audio exceeds the upload limit, mdview generates the prompt from actions alone. You can still view documents and record audio without a key.

## 5. Use the CLI in Terminal

After installing the PKG, you can run the CLI directly from Terminal without launching the app first.

```bash
# Verify the installation and version
mdview --version

# Browse the current directory
mdview

# Open a file
mdview README.md

# Set the port
mdview -port 9000 README.md
```

Press **Ctrl+C** to quit the CLI. Recordings made through the CLI also go to `~/Library/Application Support/mdview/mdview-recordings/`.

If the command is missing, open a new Terminal window and run the following checks.

```bash
rehash
command -v mdview
mdview --version
```

A successful installation shows `/usr/local/bin/mdview`. If `type -a mdview` lists an older development installation first, such as `~/.local/bin/mdview`, remove the old executable and check again. In a shell with a restricted PATH, run `/usr/local/bin/mdview --version` and check that the shell's PATH includes `/usr/local/bin`.

## 6. Update or uninstall

### Update

1. Save your recording in the browser and wait for post-processing to finish.
2. Quit the mdview app and any independently running CLI instances.
3. Run the new PKG installer. It updates both the app and the CLI link.

Settings and existing recordings are stored outside the app, so they survive replacement. Updates are installed manually.

### Uninstall

1. Quit the mdview app and CLI.
2. Move `mdview.app` from Applications to the Trash.
3. Remove the CLI link created by the installer after confirming its target.

```bash
ls -l /usr/local/bin/mdview
# Run this only if the link points to /Applications/mdview.app/Contents/MacOS/mdview:
sudo rm /usr/local/bin/mdview

# To also remove the installation receipt:
sudo pkgutil --forget camp.tools.mdview.pkg
```

User settings remain in `~/.config/mdview`, including the `chrome-profile` created by the web-opening feature in older releases. Recordings remain in `~/Library/Application Support/mdview/mdview-recordings`. Recordings saved by older versions remain in the `mdview-recordings` directory under the folder that was open at the time. To remove these files too, locate them in Finder, back up anything you need, and delete them manually.

## 7. Troubleshooting

| Symptom | What to check |
| --- | --- |
| The app will not run on an Intel Mac | This package supports Apple Silicon Macs only. |
| macOS version is unsupported | In About This Mac, check that you are running macOS 13 or later. |
| `zsh: command not found: mdview` | Verify that you installed a PKG for version 1.0.1 or later. Copying the app from the older DMG does not register the command. Run `command -v mdview` in a new Terminal window. |
| The browser does not open | Visit the `http://localhost:<port>` address shown in the app or click **Reopen Browser**. |
| The document is blank, or PDFs or diagrams do not render | Check your internet connection and whether your work network can reach `cdn.jsdelivr.net` and `cdnjs.cloudflare.com`. |
| The microphone does not record | Check the browser's microphone permission for localhost, then check the browser's access in System Settings → Privacy & Security → Microphone. |
| The recording fails to save | Check write permissions for `~/Library/Application Support/mdview` and available disk space. |
| Only transcription fails | Check your API key and transcription model in the bottom-left settings. Verify API access and audio size. Viewing documents and recording audio work without a key. |
| An unidentified developer or damaged app warning appears | Check that you have an official release without `unnotarized` in its name and download it again. Use the verification steps below to check signing and notarization before changing any security settings. |
| The app stays in the shutting-down state | Wait for transcription and prompt generation to finish. Force-quitting can lose results that are still being processed. |

Verify the download against the `SHA256SUMS` file in the release directory.

```bash
cd "path/to/downloaded/release-folder"
shasum -a 256 -c SHA256SUMS
```

Verify the app signature and Apple notarization with the following commands.

```bash
codesign --verify --deep --strict --verbose=2 /Applications/mdview.app
spctl --assess --type execute --verbose=2 /Applications/mdview.app
```

An official release shows `accepted`, `source=Notarized Developer ID`, and `origin=Developer ID Application: Mediawave (23B9Q752EU)`. The usual download confirmation dialog may still appear on first launch.
