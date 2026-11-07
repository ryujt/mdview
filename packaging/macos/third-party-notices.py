#!/usr/bin/env python3
"""Collect the Go toolchain and resolved module licenses for binary distribution."""

import json
from pathlib import Path
import subprocess
import sys


def go(*args):
    return subprocess.check_output(["go", *args], text=True).strip()


sections = ["mdview — Third-party notices\n\n"
            "The following notices cover Go and the resolved Go module graph.\n"
            "Some modules are used only during development or testing.\n"]
goroot = Path(go("env", "GOROOT"))
sections.append(f"Go {go('env', 'GOVERSION')}\n\n{(goroot / 'LICENSE').read_text()}")
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
Path(sys.argv[1]).write_text("\n\n".join("=" * 72 + "\n" + section for section in sections) + "\n")
