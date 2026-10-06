# Architecture figures

English PNGs and SVGs for goen's architecture. Labels, layout and export
settings are defined in `render_diagrams.py`.

| File stem | PNG dimensions |
| --- | --- |
| `01-system-context` | 3200 × 2310 |
| `02-checkout-payment` | 3200 × 3000 |
| `03-stock-payment-race` | 3200 × 2380 |
| `04-durable-work` | 3200 × 2900 |
| `05-resource-boundaries` | 3200 × 2640 |

## Rebuild

Requirements: Python 3.10+, [Pillow](https://python-pillow.github.io/),
[Inkscape](https://inkscape.org/) 1.2+, and DejaVu Sans (regular and bold).
On Linux, the font package is usually `fonts-dejavu-core`.

```sh
python docs/architecture/render_diagrams.py
```

Options:

- `--svg-only`: regenerate SVGs without Inkscape.
- `--only 02`: regenerate one figure (`01` through `05`).

The script replaces matching files in this directory. PNGs are exported at
twice the SVG dimensions.
