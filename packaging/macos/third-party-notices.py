#!/usr/bin/env python3
"""Collect Go and bundled browser-library licenses for binary distribution."""

import json
from pathlib import Path
import subprocess
import sys


def go(*args):
    return subprocess.check_output(["go", *args], text=True).strip()


sections = ["mdview — Third-party notices\n\n"
            "The following notices cover Go, the resolved Go module graph, and bundled browser libraries.\n"
            "Some modules are used only during development or testing.\n"]
goroot = Path(go("env", "GOROOT"))
# Homebrew keeps the toolchain license beside libexec rather than inside GOROOT.
go_license = next((path for path in (goroot / "LICENSE", goroot.parent / "LICENSE")
                   if path.is_file()), None)
if go_license is None:
    raise SystemExit(f"Go license missing near {goroot}; inspect before distributing")
sections.append(f"Go {go('env', 'GOVERSION')}\n\n{go_license.read_text()}")
remaining = go("list", "-m", "-json", "all")
decoder = json.JSONDecoder()
while remaining.strip():
    module, end = decoder.raw_decode(remaining.lstrip())
    remaining = remaining.lstrip()[end:]
    if module.get("Main"):
        continue
    directory = Path(module["Dir"])
    licenses = [directory / name for name in ("LICENSE", "LICENSE.txt", "LICENSE.md", "COPYING")
                if (directory / name).is_file()]
    if not licenses:
        raise SystemExit(f"License missing for {module['Path']}; inspect before distributing")
    sections.append(f"{module['Path']} {module.get('Version', '')}\n\n" +
                    "\n\n".join(path.read_text() for path in licenses))
browser_licenses = Path(__file__).resolve().parents[2] / "internal/mdview/static/vendor/LICENSES.txt"
if not browser_licenses.is_file():
    raise SystemExit(f"Bundled browser-library licenses missing at {browser_licenses}; inspect before distributing")
sections.append("Bundled browser libraries\n\n" + browser_licenses.read_text(encoding="utf-8"))
Path(sys.argv[1]).write_text("\n\n".join("=" * 72 + "\n" + section for section in sections) + "\n")
