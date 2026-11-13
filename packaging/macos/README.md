# Build a macOS release for Apple Silicon

`build-release.sh` bundles the Go CLI and AppKit launcher into a macOS PKG installer. The installer places the app at `/Applications/mdview.app` and creates the `/usr/local/bin/mdview` link so other users can run it directly from Terminal. Users need no Go, Node.js, or Xcode installation. The launcher uses the selected folder as its working directory. On quit, it sends SIGTERM to the CLI it started and waits for post-processing.

## Build environment

- macOS and Xcode or Command Line Tools (Swift, `notarytool`, `stapler`, and `iconutil`).
- Go (the toolchain in `go.mod` can download automatically), Node.js for the existing JS tests, and Python 3.
- A **Developer ID Application** certificate installed in Keychain with its private key.
- A **Developer ID Installer** certificate and private key for signing the installer.
- Apple notarization credentials via the `NOTARY_PROFILE` Keychain profile or the `APPLE_CREDENTIALS_JSON` path.

This distribution uses Developer ID signing. Sign the app with Developer ID Application and the hardened runtime, and sign the installer with Developer ID Installer. Apply secure timestamps to both. Mac App Store Apple Distribution certificates and iOS provisioning profiles are unused. Follow [Apple's notarization process](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution) to notarize the app ZIP and final PKG separately and staple the tickets.

## Official release build

```bash
cd cli/mdview
NOTARY_PROFILE="your-keychain-profile" ./packaging/macos/build-release.sh
```

To use the supplied App Store Connect credentials JSON, run the following command.

```bash
APPLE_CREDENTIALS_JSON="/external/path/to/account-and-credentials.json" \
  ./packaging/macos/build-release.sh
```

The script reads `key_id`, `issuer_id`, and `key_path` (or `installed_key_path`) under `app_store_connect_api`. It reads the private key from its external path without copying it into the project or output. Keep the JSON and private key out of Git.

To use another team's certificate, set `SIGN_IDENTITY` to the Developer ID Application name or SHA-1 fingerprint in Keychain. The default is `8102033CFEB5309D454652A5CCC4E20B784585D4`, the SHA-1 fingerprint of the supplied `developer-id-application-20261031.cer`. The fingerprint distinguishes certificates with the same name.

Set `INSTALLER_SIGN_IDENTITY` to specify the installer certificate. The default is `884593FADCB9B8FD13EB205B2DEEB724AEF0A78F`, the SHA-1 fingerprint of the `Developer ID Installer: Mediawave (23B9Q752EU)` certificate issued on 2026-11-09. Keep the certificate and its dedicated private key in the external `AppStore_등록_재사용` folder. Use the installer certificate to sign the PKG, following [Apple's package signing guidance](https://developer.apple.com/documentation/security/resolving-common-notarization-issues).

For a local packaging check, run the following command.

```bash
./packaging/macos/build-release.sh --no-notarize
```

A local build still requires Developer ID signing and has an `-unnotarized` suffix. Reserve these builds for local checks.

## Build outputs and verification

The build writes the following to `dist/mdview-<version>-macos-arm64/`.

- `mdview-<version>-macos-arm64.pkg` is the installer for users. It includes the app with its stapled notarization ticket and the CLI link.
- `INSTALL.md` and `START-HERE.html` are the English installation and user guides.
- `SHA256SUMS` contains SHA-256 checksums for the final PKG and installation guide.
- `THIRD-PARTY-NOTICES.txt` contains license notices for Go and its dependencies. The notices also ship in the app resources.
- `release.json` records the version, source commit, build time, supported OS, and notarization status.
- `verification/` contains Apple notarization responses and submission IDs.

Verification covers Go tests and vet, existing Node.js tests, the arm64 Mach-O build, CLI version output, nested signatures, the installer signature via `pkgutil`, notarization approval for the app and PKG, stapled tickets, and Gatekeeper assessment. The official output directory is created only after notarization is approved. An existing output for the same version causes the build to stop before overwriting it.

Git ignores `dist/`. Packaging excludes local `config.json`, recordings, and credentials, and bundles `config.example.json` as the sample configuration. Installation may require administrator authentication. The CLI link goes into `/usr/local/bin` on the default macOS PATH without editing per-user shell configuration. The app runs without administrator privileges. The installer adds no startup items or installation scripts.

Bundle relocation is disabled for the PKG. Even if a development build with the same bundle ID remains in Downloads or dist, the app installs at `/Applications/mdview.app` and the CLI links to that location. `Distribution.xml.in` sets the minimum macOS version of 13 and the arm64 requirement.

Update `VERSION` and the package filename in the installation guide when changing the version. The build embeds the version, commit, and UTC build time in `--version` output. Uncommitted changes append `-dirty` to the commit.

## Manual smoke test

Install the completed PKG using the macOS installer and run these checks.

1. Launch the app, select a file or folder, and verify that the browser opens and renders the document.
2. Open files with spaces and Korean characters in their names, including by dragging them into the app.
3. Reopen the browser, stop the viewer, and open another folder.
4. Use Finder's **Open With** menu and `open -a mdview <file>`.
5. Quit the app and check that the session's local port and CLI process have been released.
6. If you recorded audio, save it and wait for post-processing before quitting, then check the results under the selected folder.
7. Before launching the app, run `command -v mdview` and `mdview --version` in a new Terminal window. Verify that default zsh without user configuration resolves `/usr/local/bin/mdview`.
8. Inspect the package with `pkgutil --expand-full`. Verify that it contains only the app and the CLI link and that the link points to `/Applications/mdview.app/Contents/MacOS/mdview`.

The viewer still depends on CDN resources. The release targets arm64 Macs running macOS 13 or later. Other macOS versions and Intel environments require separate verification.
