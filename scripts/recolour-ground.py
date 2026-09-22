#!/usr/bin/env python3
"""Recolour the flat, baked-in product/hero background from one ground colour
to another, and regenerate that source's responsive derivatives to match.

The renders (assets/media/products/*.webp, assets/media/hero/*.webp) were lit
warm, giving every pixel in the frame a shared colour cast on top of the flat
ground colour. This script corrects that cast the way a photographer would
correct white balance: a single per-channel gain, applied to every pixel in
the frame, uniformly, with no region detection of any kind.

    gain_c = to_c / from_c          (c in R, G, B)
    new_c  = clamp(round(pixel_c * gain_c), 0, 255)

By construction:
  - A pixel that was exactly the old ground colour lands exactly on the new
    ground colour (gain_c * from_c == to_c).
  - A soft shadow on the ground (some fraction of the ground's brightness)
    stays a shadow of the new ground, because the same gain scales it too.
  - The product's own colours pick up the same small cast correction --
    intended, not a side effect: the renders share one warm light source.
  - There is no boundary or region anywhere, so there is nothing for a
    feather to fix and no halo can form.

Deliberately NOT here: gamma-space math, chroma/connectivity thresholds, or
a masked/unmasked fallback. A uniform per-channel gain is the whole model.

Pillow only -- no numpy dependency is introduced or required by this script.

Usage:
    python3 scripts/recolour-ground.py --from '#efede9' --to '#f4f4f5' \\
        assets/media/products/koto-pad-mini-01.webp \\
        assets/media/hero/home-hero-01.webp
"""
from __future__ import annotations

import argparse
import subprocess
import sys
import tempfile
from pathlib import Path

from PIL import Image

CWEBP = "/opt/homebrew/bin/cwebp"
SIPS = "/usr/bin/sips"


def parse_hex(s: str) -> tuple[int, int, int]:
    s = s.strip().lstrip("#")
    if len(s) != 6:
        raise ValueError(f"not an #RRGGBB colour: {s!r}")
    return (int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16))


def channel_gains(from_rgb, to_rgb) -> tuple[float, float, float]:
    return tuple(to_rgb[i] / from_rgb[i] for i in range(3))


def _channel_lut(gain: float) -> list[int]:
    lut = []
    for v in range(256):
        nv = v * gain
        if nv < 0:
            nv = 0
        elif nv > 255:
            nv = 255
        lut.append(round(nv))
    return lut


def recolour_image(im: Image.Image, from_rgb, to_rgb) -> Image.Image:
    """Applies the uniform per-channel white-balance gain to every pixel.
    No masking, no region detection, no feathering -- see module docstring."""
    im = im.convert("RGB")
    gains = channel_gains(from_rgb, to_rgb)
    r, g, b = im.split()
    r2 = r.point(_channel_lut(gains[0]))
    g2 = g.point(_channel_lut(gains[1]))
    b2 = b.point(_channel_lut(gains[2]))
    return Image.merge("RGB", (r2, g2, b2))


def sips_dimensions(path: Path) -> tuple[int, int]:
    out = subprocess.run(
        [SIPS, "-g", "pixelWidth", "-g", "pixelHeight", str(path)],
        capture_output=True, text=True, check=True,
    ).stdout
    w = h = None
    for line in out.splitlines():
        line = line.strip()
        if line.startswith("pixelWidth:"):
            w = int(line.split(":")[1].strip())
        elif line.startswith("pixelHeight:"):
            h = int(line.split(":")[1].strip())
    if w is None or h is None:
        raise RuntimeError(f"sips did not report dimensions for {path}")
    return w, h


def encode_to_target(im: Image.Image, target_size: int, tol: float = 0.10,
                      method: int = 6, forced_q: int | None = None):
    """Encodes `im` as webp via cwebp, searching -q (fixed -m) for the
    HIGHEST quality that lands within `tol` of target_size. Returns
    (bytes, q_used, size, within_tol)."""
    with tempfile.TemporaryDirectory() as td:
        png_path = Path(td) / "in.png"
        im.save(png_path, format="PNG")

        def encode_at(q: int) -> bytes:
            out_path = Path(td) / f"out-{q}.webp"
            subprocess.run(
                [CWEBP, "-quiet", "-q", str(q), "-m", str(method),
                 str(png_path), "-o", str(out_path)],
                check=True, capture_output=True,
            )
            return out_path.read_bytes()

        if forced_q is not None:
            data = encode_at(forced_q)
            size = len(data)
            within = abs(size - target_size) <= tol * target_size
            return data, forced_q, size, within

        # Scan from highest to lowest quality; the first one within
        # tolerance is kept, so ties prefer the higher quality.
        candidates = [95, 90, 85, 80, 75, 70, 65, 60, 55, 50, 45, 40, 35, 30, 25, 20]
        results = {}
        best = None
        for q in candidates:
            data = encode_at(q)
            size = len(data)
            results[q] = (data, size)
            if best is None or abs(size - target_size) < abs(results[best][1] - target_size):
                best = q
            if abs(size - target_size) <= tol * target_size:
                return data, q, size, True

        # Nothing in the 5-wide coarse sweep landed in tolerance, but the
        # coarse step (5) is wide enough to jump clean over a narrow
        # tolerance band. Fill in every quality between the two coarse
        # candidates the target size falls between, highest first, so nothing
        # in that band is skipped.
        sorted_q = sorted(results)
        lo_q = hi_q = None
        for i in range(len(sorted_q) - 1):
            qa, qb = sorted_q[i], sorted_q[i + 1]  # qa < qb
            sa, sb = results[qa][1], results[qb][1]  # size increases with q
            if sa <= target_size <= sb:
                lo_q, hi_q = qa, qb
                break
        if lo_q is not None:
            for q in range(hi_q - 1, lo_q, -1):
                if q in results:
                    continue
                data = encode_at(q)
                size = len(data)
                results[q] = (data, size)
                if abs(size - target_size) < abs(results[best][1] - target_size):
                    best = q
                if abs(size - target_size) <= tol * target_size:
                    return data, q, size, True

        data, size = results[best]
        within = abs(size - target_size) <= tol * target_size
        return data, best, size, within


def derivative_paths(source: Path) -> list[Path]:
    stem = source.stem
    parent = source.parent
    if parent.name == "products":
        suffixes = ["-400", "-800"]
    elif parent.name == "hero":
        suffixes = ["-720"]
    else:
        raise ValueError(f"unrecognised media directory: {parent}")
    return [parent / f"{stem}{suf}.webp" for suf in suffixes]


def process_source(source: Path, from_rgb, to_rgb, forced_q: int | None,
                    report: list[dict]) -> None:
    original_size = source.stat().st_size
    im = Image.open(source)
    final = recolour_image(im, from_rgb, to_rgb)

    data, q, size, within = encode_to_target(final, original_size, forced_q=forced_q)
    source.write_bytes(data)
    report.append({
        "file": str(source), "kind": "source",
        "before_size": original_size, "after_size": size,
        "q": q, "within_tol": within,
    })

    for dpath in derivative_paths(source):
        if not dpath.exists():
            report.append({"file": str(dpath), "kind": "derivative",
                            "error": "missing, skipped"})
            continue
        dw, dh = sips_dimensions(dpath)
        d_original_size = dpath.stat().st_size
        resized = final.resize((dw, dh), Image.LANCZOS)
        ddata, dq, dsize, dwithin = encode_to_target(
            resized, d_original_size, forced_q=forced_q)
        dpath.write_bytes(ddata)
        report.append({
            "file": str(dpath), "kind": "derivative",
            "before_size": d_original_size, "after_size": dsize,
            "q": dq, "within_tol": dwithin, "dims": f"{dw}x{dh}",
        })


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__,
                                  formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--from", dest="from_hex", required=True)
    ap.add_argument("--to", dest="to_hex", required=True)
    ap.add_argument("--quality", type=int, default=None,
                     help="Force this cwebp -q for every output instead of "
                          "auto-searching for the highest -q within +/-10%% "
                          "of the original size.")
    ap.add_argument("sources", nargs="+", type=Path)
    args = ap.parse_args(argv)

    from_rgb = parse_hex(args.from_hex)
    to_rgb = parse_hex(args.to_hex)

    report: list[dict] = []
    for source in args.sources:
        process_source(source, from_rgb, to_rgb, args.quality, report)

    for row in report:
        print(row)

    bad = [r for r in report if not r.get("within_tol", True) or r.get("error")]
    if bad:
        print(f"WARNING: {len(bad)} output(s) outside tolerance or errored",
              file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
