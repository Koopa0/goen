"""Inspect the actual Chrome PDFs and preserve raster pages for visual review."""
import json
from pathlib import Path
import subprocess
import xml.etree.ElementTree as ET

root = Path("layout-packing")
for locale in ("en", "zh-Hant"):
    pdf = root / f"{locale}.pdf"
    expected = json.loads(pdf.with_suffix(".json").read_text())
    subprocess.run(["pdftotext", "-bbox", str(pdf), str(pdf.with_suffix(".html"))], check=True)
    tree = ET.parse(pdf.with_suffix(".html"))
    pages = tree.findall(".//{*}page")
    assert pages, f"{pdf}: no printed pages"
    words = []
    for page in pages:
        width, height = float(page.attrib["width"]), float(page.attrib["height"])
        assert abs(width - 595.28) < 2 and abs(height - 841.89) < 2, f"{pdf}: not A4"
        for word in page.findall(".//{*}word"):
            box = {key: float(word.attrib[key]) for key in ("xMin", "xMax", "yMin", "yMax")}
            assert 35 <= box["xMin"] < box["xMax"] <= width - 35, f"{pdf}: horizontal clipping {word.text} {box}"
            assert 35 <= box["yMin"] < box["yMax"] <= height - 35, f"{pdf}: vertical clipping {word.text} {box}"
            words.append(word.text or "")
    actual = "".join("".join(words).split())
    wanted = "".join(expected["text"].split())
    assert actual == wanted, f"{pdf}: printed content differs from fulfilment fields: {actual!r} != {wanted!r}"
    subprocess.run(["pdftoppm", "-png", "-scale-to", "1400", str(pdf), str(root / locale)], check=True)
    print(f"{pdf}: {len(pages)} A4 pages, fulfilment text complete, all words within paper margins")
