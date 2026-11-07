#!/usr/bin/env python3
"""Submit and verify a notarization, without copying credentials into build artifacts."""

import argparse
import json
import os
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()

    profile = os.environ.get("NOTARY_PROFILE")
    credential_file = os.environ.get("APPLE_CREDENTIALS_JSON")
    if profile:
        auth = ["--keychain-profile", profile]
    elif credential_file:
        credentials = json.loads(Path(credential_file).read_text())["app_store_connect_api"]
        key = next((Path(credentials[k]) for k in ("installed_key_path", "key_path")
                    if credentials.get(k) and Path(credentials[k]).is_file()), None)
        if key is None:
            raise SystemExit("App Store Connect private key file not found")
        auth = ["--key", str(key), "--key-id", credentials["key_id"],
                "--issuer", credentials["issuer_id"]]
    else:
        raise SystemExit("Set NOTARY_PROFILE or APPLE_CREDENTIALS_JSON before notarizing")

    # No secret key data or authentication parameters are written to the report.
    print(f"Submitting {args.archive.name} to Apple…", flush=True)
    result = subprocess.run([
        "xcrun", "notarytool", "submit", str(args.archive), *auth,
        "--wait", "--timeout", "30m", "--output-format", "json",
    ], check=False, capture_output=True, text=True)
    try:
        report = json.loads(result.stdout)
    except json.JSONDecodeError:
        raise SystemExit(result.stderr or result.stdout or "No notarization response")
    args.report.write_text(json.dumps(report, indent=2) + "\n")
    print(f"Apple notarization: {report.get('status', 'unknown')} ({report.get('id', 'no ID')})", flush=True)
    if result.returncode != 0 or report.get("status") != "Accepted":
        if report.get("id"):
            log = args.report.with_suffix(".log.json")
            subprocess.run(["xcrun", "notarytool", "log", report["id"], *auth, str(log)], check=False)
        raise SystemExit("Notarization has not been accepted. See report; do not distribute this build.")


if __name__ == "__main__":
    main()
