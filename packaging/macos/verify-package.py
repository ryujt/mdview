#!/usr/bin/env python3
"""Verify the actual Installer payload without installing or executing package scripts."""

import argparse
import os
from pathlib import Path
import plistlib
import subprocess
import tempfile
import xml.etree.ElementTree as ET


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("package", type=Path)
    parser.add_argument("version")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="mdview-pkg-check-") as tmp:
        expanded = Path(tmp) / "expanded"
        subprocess.run(["pkgutil", "--expand-full", str(args.package.resolve()), str(expanded)], check=True)
        distribution = ET.parse(expanded / "Distribution").getroot()
        assert distribution.find("options").get("hostArchitectures") == "arm64"
        domains = distribution.find("domains")
        assert domains.get("enable_localSystem") == "true"
        assert domains.get("enable_currentUserHome") == "false"
        assert domains.get("enable_anywhere") == "false"
        assert distribution.find("volume-check/allowed-os-versions/os-version").get("min") == "13.0"
        components = list(expanded.rglob("PackageInfo"))
        assert len(components) == 1, components
        package_info = ET.parse(components[0]).getroot()
        assert package_info.get("identifier") == "camp.tools.mdview.pkg"
        assert package_info.get("version") == args.version
        assert package_info.get("install-location") == "/"
        assert package_info.find("scripts") is None
        assert package_info.get("relocatable") == "false"
        assert not package_info.findall("relocate/bundle"), "App relocation would break the absolute CLI link"
        payload = components[0].parent / "Payload"
        app = payload / "Applications/mdview.app"
        cli = payload / "usr/local/bin/mdview"
        assert cli.is_symlink()
        assert os.readlink(cli) == "/Applications/mdview.app/Contents/MacOS/mdview"
        info = plistlib.loads((app / "Contents/Info.plist").read_bytes())
        assert info["CFBundleShortVersionString"] == args.version
        assert info["CFBundleIdentifier"] == "camp.tools.mdview"
        assert info["CFBundleExecutable"] == "mdview-launcher"
        allowed = {
            "Contents/Info.plist", "Contents/MacOS/mdview", "Contents/MacOS/mdview-launcher",
            "Contents/Resources/AppIcon.icns", "Contents/Resources/INSTALL.md",
            "Contents/Resources/config.example.json", "Contents/Resources/THIRD-PARTY-NOTICES.txt",
            "Contents/_CodeSignature/CodeResources", "Contents/CodeResources",
        }
        app_files = {str(p.relative_to(app)) for p in app.rglob("*") if p.is_file()}
        assert app_files <= allowed, app_files - allowed
        for entry in payload.rglob("*"):
            if entry.is_file() or entry.is_symlink():
                relative = str(entry.relative_to(payload))
                assert relative.startswith("Applications/mdview.app/") or relative == "usr/local/bin/mdview", relative
        for name in ("mdview", "mdview-launcher"):
            binary = app / "Contents/MacOS" / name
            arch = subprocess.check_output(["/usr/bin/lipo", "-archs", str(binary)], text=True).strip()
            assert arch == "arm64", arch
            assert binary.stat().st_mode & 0o005 == 0o005, "Other users need read and execute access"
        subprocess.run(["codesign", "--verify", "--deep", "--strict", str(app)], check=True)
        print("PKG verified: fixed app destination, system CLI link, all-user domain, arm64, macOS 13+, no scripts or private files.")


if __name__ == "__main__":
    main()
