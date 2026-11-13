# macOS Apple Silicon release build

`build-release.sh` bundles the existing Go CLI and the AppKit launcher into a macOS PKG installer. Installation places both `/Applications/mdview.app` and a `/usr/local/bin/mdview` link, so other users can run it from Terminal right away. Users don't need Go, Node.js, or Xcode. The launcher uses the folder the user selects as the working directory, and on quit it sends SIGTERM to the CLI it started and waits for post-processing to finish.

## Build environment

- macOS with Xcode/Command Line Tools (Swift, `notarytool`, `stapler`, `iconutil`).
- Go (the `go.mod` toolchain can be downloaded automatically), Node.js (for the existing JS tests), and Python 3.
- A **Developer ID Application** certificate installed in the keychain together with its private key.
- A **Developer ID Installer** certificate and private key for signing the installer.
- Apple notarization credentials: a `NOTARY_PROFILE` keychain profile or an `APPLE_CREDENTIALS_JSON` path.

The Mac App Store `Apple Distribution` certificate and iOS provisioning profiles are not used for this release. The app is signed with Developer ID Application and the hardened runtime, the installer with Developer ID Installer, and both get a secure timestamp. Following [Apple's notarization process](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution), the app ZIP and the final PKG are each notarized and stapled with a ticket.

## Official build

```bash
cd cli/mdview
NOTARY_PROFILE="your-keychain-profile" ./packaging/macos/build-release.sh
```

When using a provided App Store Connect credentials JSON:

```bash
APPLE_CREDENTIALS_JSON="/external/path/to/credentials/account_and_credentials.json" \
  ./packaging/macos/build-release.sh
```

The script reads `key_id`, `issuer_id`, and `key_path` (or `installed_key_path`) under `app_store_connect_api` in the JSON. The private key file is used only from that external path and is never copied into the project or the build output. Do not commit the JSON or private keys.

To use another team's certificate, set the `SIGN_IDENTITY` environment variable to the Developer ID Application name in the keychain or its SHA-1 fingerprint. The default is the SHA-1 fingerprint `8102033CFEB5309D454652A5CCC4E20B784585D4` of the provided `developer-id-application-20261031.cer`. It is specified by fingerprint to avoid confusion with other certificates of the same name.

The installer certificate is set with `INSTALLER_SIGN_IDENTITY`. The default is the SHA-1 fingerprint `884593FADCB9B8FD13EB205B2DEEB724AEF0A78F` of the `Developer ID Installer: Mediawave (23B9Q752EU)` certificate issued on 2026-11-09. The certificate and its dedicated private key are kept in the external `AppStore_등록_재사용` folder. Never sign the PKG with the app certificate. Follow [Apple's package signing guidance](https://developer.apple.com/documentation/security/resolving-common-notarization-issues).

To check local packaging only:

```bash
./packaging/macos/build-release.sh --no-notarize
```

Developer ID signing is still required in this case, and `-unnotarized` is appended to the output name. Do not use it as an official release for users.

## Output and verification

The following are created in `dist/mdview-<version>-macos-arm64/`:

- `mdview-<version>-macos-arm64.pkg`: the installer to give to users. It contains the notarized and stapled app and the CLI link.
- `INSTALL.md`, `START-HERE.html`: installation and usage guides.
- `SHA256SUMS`: SHA-256 checksums of the final PKG and the installation guide.
- `THIRD-PARTY-NOTICES.txt`: license notices for Go and dependent modules. Also included in the app resources.
- `release.json`: version, source commit, build time, supported OS, and notarization status.
- `verification/`: Apple notarization responses and submission IDs.

Verification steps: Go tests and vet, the existing Node.js tests, Mach-O arm64 build, running the CLI version command, nested signature verification, `pkgutil` installer signature verification, confirming app and PKG notarization approval, ticket validation, and Gatekeeper assessment. If notarization is not approved, the official output folder is not created. If output for the same version already exists, the build stops instead of overwriting it.

`dist/` is excluded from Git. Local `config.json`, recordings, and credentials are never packaged; only `config.example.json` is included as an app resource. Installation may require administrator authentication. The installer does not modify per-user shell config files; it installs the link into `/usr/local/bin`, which is on macOS's default PATH, and running the app does not require administrator privileges. No install scripts or login items are added.

Bundle relocation is disabled in the PKG. Even if a development build with the same bundle ID is left in Downloads or dist, the app is always installed to `/Applications/mdview.app`, and the CLI link points to that path. The minimum macOS 13 and arm64 requirements are specified in `Distribution.xml.in`.

The version is managed in `VERSION`. When you change the version, also update the release file name in the installation guide. At build time, the version, commit, and UTC time are injected into `--version`. If there are uncommitted changes, `-dirty` is appended to the commit info.

## Manual run checks

After installing the finished PKG with the macOS Installer, check the following.

1. Launch the app → choose a file/folder → the browser opens and the document renders.
2. Documents whose file names contain spaces or non-ASCII characters (e.g. Korean), and opening files by drag and drop.
3. "Reopen Browser", "Stop Viewer", and opening a different folder.
4. Finder's "Open With" and `open -a mdview <file>`.
5. After quitting the app, confirm that the session's local port and CLI process are cleaned up.
6. If you used recording, quit after saving and post-processing, and check the results under the selected folder.
7. Before launching the app, check `command -v mdview` and `mdview --version` in a new Terminal window. Confirm that `/usr/local/bin/mdview` is picked even in a default zsh with no user configuration.
8. Inspect the package with `pkgutil --expand-full` to confirm that it contains only the app and the CLI link, and that the link points to `/Applications/mdview.app/Contents/MacOS/mdview`.

The viewer's current CDN dependencies remain, so this is not a fully offline release. The release targets arm64 on macOS 13 or later; other macOS versions and Intel environments need separate verification.
