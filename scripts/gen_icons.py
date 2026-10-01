#!/usr/bin/env python3
"""
LombokDNSProxy — extension icon generator.

Renders the shield mark used across the UI (brand green #1a7f5a) at every
size Chrome/Firefox ask for, plus greyed-out variants the background script
switches to when the daemon is unreachable.

Regenerate with:  python3 scripts/gen_icons.py

Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
"""

import os
from PIL import Image, ImageDraw

SIZES = [16, 32, 48, 128]
OUT_DIR = os.path.join(
    os.path.dirname(__file__), "..", "src", "browser-ext", "public", "icons"
)

BRAND = (26, 127, 90, 255)      # #1a7f5a
BRAND_DARK = (8, 80, 65, 255)   # #085041
GREY = (140, 140, 145, 255)
GREY_DARK = (105, 105, 110, 255)
WHITE = (255, 255, 255, 255)


def shield_polygon(size):
    """Shield outline scaled to `size`, matching the 24x24 SVG path in the UI."""
    s = size / 24.0
    return [
        (12 * s, 1.2 * s),    # top point
        (3.2 * s, 5 * s),     # upper left
        (3.2 * s, 11 * s),    # left side
        (6.5 * s, 19.5 * s),  # lower left curve
        (12 * s, 22.8 * s),   # bottom point
        (17.5 * s, 19.5 * s), # lower right curve
        (20.8 * s, 11 * s),   # right side
        (20.8 * s, 5 * s),    # upper right
    ]


def draw_icon(size, fill, accent, checkmark=True):
    # Render at 4x then downsample — gives clean edges without antialias hacks
    scale = 4
    big = size * scale
    img = Image.new("RGBA", (big, big), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    d.polygon(shield_polygon(big), fill=fill)

    # Inner bevel for depth (skipped at 16px where it just muddies the pixels)
    if size >= 32:
        inset = [(x * 0.82 + big * 0.09, y * 0.82 + big * 0.09)
                 for (x, y) in shield_polygon(big)]
        d.polygon(inset, fill=accent)

    if checkmark and size >= 32:
        u = big / 24.0
        d.line(
            [(8.2 * u, 12.0 * u), (10.8 * u, 14.8 * u), (16.0 * u, 8.8 * u)],
            fill=WHITE,
            width=max(2, int(1.9 * u)),
            joint="curve",
        )

    return img.resize((size, size), Image.LANCZOS)


def main():
    os.makedirs(OUT_DIR, exist_ok=True)
    written = []

    for size in SIZES:
        active = draw_icon(size, BRAND, BRAND_DARK)
        path = os.path.join(OUT_DIR, f"icon{size}.png")
        active.save(path, "PNG", optimize=True)
        written.append(path)

        # Greyed variant — background.ts swaps to these when the daemon drops
        if size in (16, 48, 128):
            grey = draw_icon(size, GREY, GREY_DARK)
            gpath = os.path.join(OUT_DIR, f"icon{size}-grey.png")
            grey.save(gpath, "PNG", optimize=True)
            written.append(gpath)

    for p in written:
        print(f"  {os.path.relpath(p, os.path.join(os.path.dirname(__file__), '..'))}"
              f"  ({os.path.getsize(p)} bytes)")
    print(f"\n{len(written)} icons written.")


if __name__ == "__main__":
    main()
