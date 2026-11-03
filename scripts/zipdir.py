#!/usr/bin/env python3
"""Zip one top-level directory, preserving its name inside the archive.

`zip -r` refuses some directory trees on this filesystem ("Nothing to do"), and a
release script must not depend on a tool's mood; this is 15 lines and portable.
"""
import os
import sys
import zipfile

src, out, top = sys.argv[1], sys.argv[2], sys.argv[3]
base = os.path.join(src, top)
count = 0
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk(base):
        for f in files:
            full = os.path.join(root, f)
            z.write(full, os.path.relpath(full, src))
            count += 1
if count == 0:
    sys.exit(f"zipdir: nothing under {base}")
print(f"zipdir: {out} ({count} files)")
