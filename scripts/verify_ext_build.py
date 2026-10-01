#!/usr/bin/env python3
"""
LombokDNSProxy — extension build verifier.

A browser extension that builds "successfully" can still be rejected at load
time if the manifest points at a file the bundler never emitted. Vite only
treats scripts and HTML as entry points, so icons and the declarativeNetRequest
rule file reach the output through publicDir — and a wrong path there fails
silently at build time and loudly in the browser.

This walks each built manifest, collects every path it references, and checks
that the file actually exists in that build directory.

Usage (from src/browser-ext/):
    python3 ../../scripts/verify_ext_build.py

Exits non-zero if anything is missing, so CI fails on a broken bundle.

Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
"""

import glob
import json
import os
import sys

# (directory, human label) — skipped if the directory isn't present, so the
# script works whether you built one target or both.
BUILDS = [
    ("dist", "Chrome MV3"),
    ("dist-firefox", "Firefox MV2"),
]

# Manifest keys whose values are file paths.
PATH_KEYS = {"service_worker", "default_popup", "options_page", "page", "path"}
LIST_KEYS = {"js", "css", "scripts", "resources", "web_accessible_resources"}
MAP_KEYS = {"default_icon", "icons"}


def collect_refs(node, out):
    """Recursively gather every file path a manifest node refers to."""
    if isinstance(node, dict):
        for key, value in node.items():
            if key in PATH_KEYS and isinstance(value, str):
                out.add(value)
            elif key in LIST_KEYS and isinstance(value, list):
                out.update(v for v in value if isinstance(v, str))
            elif key in MAP_KEYS and isinstance(value, dict):
                out.update(v for v in value.values() if isinstance(v, str))
            else:
                collect_refs(value, out)
    elif isinstance(node, list):
        for item in node:
            collect_refs(item, out)


def verify(build_dir, label):
    """Return (checked, missing) for one build directory."""
    manifest_path = os.path.join(build_dir, "manifest.json")
    if not os.path.isfile(manifest_path):
        print(f"  {label}: no manifest.json in {build_dir}/ — build did not run")
        return 0, ["manifest.json"]

    with open(manifest_path, encoding="utf-8") as fh:
        manifest = json.load(fh)

    refs = set()
    collect_refs(manifest, refs)

    missing = []
    for ref in sorted(refs):
        # Strip any query/fragment a manifest path might carry.
        clean = ref.split("?")[0].split("#")[0]

        # web_accessible_resources legitimately accepts glob patterns
        # ("icons/*.png"), so a pattern passes if it matches at least one file
        # rather than having to exist as a literal path.
        if any(ch in clean for ch in "*?["):
            matches = glob.glob(os.path.join(build_dir, clean))
            exists = bool(matches)
            suffix = f"  ({len(matches)} match)" if exists else ""
            print(f"  {'ok  ' if exists else 'MISS'}  {clean}{suffix}")
        else:
            exists = os.path.isfile(os.path.join(build_dir, clean))
            print(f"  {'ok  ' if exists else 'MISS'}  {clean}")

        if not exists:
            missing.append(clean)

    return len(refs), missing


def main():
    any_built = False
    failures = {}

    for build_dir, label in BUILDS:
        if not os.path.isdir(build_dir):
            continue
        any_built = True
        mv = "?"
        try:
            with open(os.path.join(build_dir, "manifest.json"), encoding="utf-8") as fh:
                mv = json.load(fh).get("manifest_version", "?")
        except (OSError, json.JSONDecodeError):
            pass

        print(f"\n{label}  ({build_dir}/, manifest_version {mv})")
        checked, missing = verify(build_dir, label)
        if missing:
            failures[label] = missing
        else:
            print(f"  → {checked} referenced files, all present")

    if not any_built:
        print("No build directories found. Run `npm run build` first.", file=sys.stderr)
        return 1

    print()
    if failures:
        for label, missing in failures.items():
            print(f"FAIL  {label}: {len(missing)} missing file(s): {', '.join(missing)}",
                  file=sys.stderr)
        print("\nA manifest referencing a file the bundler never emitted will be "
              "rejected at load time. Check publicDir in vite.config.ts.",
              file=sys.stderr)
        return 1

    print("All builds verified — every manifest-referenced file is present.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
