#!/usr/bin/env python3
"""Render goen's architecture figures as editable SVG and 2x PNG.

Requirements: Python 3.10+, Pillow, Inkscape 1.2+ and DejaVu Sans.
Run from any directory: python docs/architecture/render_diagrams.py
Use --svg-only to regenerate vectors without invoking Inkscape.
All geometry, text and colors live in this file. No network access is used.
"""

from __future__ import annotations

import argparse
import html
from pathlib import Path
import shutil
import subprocess

from PIL import Image, ImageFont

ROOT = Path(__file__).resolve().parent
WIDTH = 1600
SCALE = 2
FONT = "DejaVu Sans"
BG = "#F8F7F2"
WHITE = "#FFFFFF"
NAVY = "#183442"
TEAL = "#126E65"
TEAL_BG = "#E8F2EE"
AMBER = "#926021"
AMBER_BG = "#FFF1DD"
MUTED = "#536973"
LINE = "#D4DEDA"
LIFELINE = "#AABDB9"
SOFT = "#F0F3F1"
FONT_DIR = Path("/usr/share/fonts/truetype/dejavu")


def font_path(bold: bool = False) -> str:
    filename = "DejaVuSans-Bold.ttf" if bold else "DejaVuSans.ttf"
    candidate = FONT_DIR / filename
    if candidate.exists():
        return str(candidate)
    if shutil.which("fc-match"):
        family = FONT + (":style=Bold" if bold else "")
        match = subprocess.check_output(
            ["fc-match", "-f", "%{file}", family], text=True
        ).strip()
        if match:
            return match
    raise RuntimeError("Install DejaVu Sans (e.g. fonts-dejavu-core).")


FONTS: dict[tuple[int, bool], ImageFont.FreeTypeFont] = {}


def measure(s: str, size: int, bold: bool = False) -> float:
    key = (size, bold)
    if key not in FONTS:
        FONTS[key] = ImageFont.truetype(font_path(bold), size)
    return FONTS[key].getlength(s)


class SVG:
    def __init__(self, name: str, height: int, title: str, subtitle: str):
        self.name, self.height = name, height
        self.parts = [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{WIDTH}" height="{height}" viewBox="0 0 {WIDTH} {height}" role="img" aria-labelledby="title desc">',
            f'<title id="title">{html.escape(title)}</title>',
            f'<desc id="desc">{html.escape(subtitle)}</desc>',
            '<defs>',
        ]
        for key, color in [("navy", NAVY), ("teal", TEAL), ("amber", AMBER), ("muted", MUTED)]:
            self.parts.append(
                f'<marker id="arrow-{key}" markerUnits="userSpaceOnUse" markerWidth="13" markerHeight="13" refX="11" refY="6.5" orient="auto-start-reverse" viewBox="0 0 13 13"><path d="M 1 1 L 12 6.5 L 1 12 Z" fill="{color}"/></marker>'
            )
        self.parts.append('</defs>')
        self.rect(0, 0, WIDTH, height, fill=BG, stroke="none", radius=0)
        self.text(60, 79, title, size=41, bold=True)
        self.line([(60, 125), (1540, 125)], color=LINE, width=1.5)
        self.parts.append('<g transform="translate(0,-60)">')

    def rect(self, x, y, w, h, *, fill=WHITE, stroke=LINE, radius=17, width=1.6):
        self.parts.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{radius}" fill="{fill}" stroke="{stroke}" stroke-width="{width}"/>')

    def text(self, x, y, s, *, size=26, bold=False, fill=NAVY, anchor="start"):
        text_width = measure(s, size, bold)
        left = x if anchor == "start" else x - text_width if anchor == "end" else x - text_width / 2
        if left < -1 or left + text_width > WIDTH + 1:
            raise ValueError(f"Text outside canvas: {self.name}: {s!r}")
        self.parts.append(f'<text x="{x}" y="{y}" font-family="{FONT}, Arial, sans-serif" font-size="{size}" font-weight="{700 if bold else 400}" fill="{fill}" text-anchor="{anchor}">{html.escape(s)}</text>')

    def para(self, x, y, s, w, *, size=25, leading=37, bold=False, fill=MUTED, anchor="start"):
        lines = []
        for paragraph in s.split("\n"):
            current = ""
            for word in paragraph.split():
                trial = (current + " " + word).strip()
                if current and measure(trial, size, bold) > w:
                    lines.append(current)
                    current = word
                else:
                    current = trial
            lines.append(current)
        for i, line in enumerate(lines):
            if measure(line, size, bold) > w + 1:
                raise ValueError(f"Unwrappable text in {self.name}: {line}")
            self.text(x, y + i * leading, line, size=size, bold=bold, fill=fill, anchor=anchor)
        return y + len(lines) * leading

    def line(self, points, *, color=NAVY, width=2.7, dashed=False, end=False, start=False):
        coords = " ".join(f"{x},{y}" for x, y in points)
        key = {NAVY: "navy", TEAL: "teal", AMBER: "amber", MUTED: "muted"}.get(color, "navy")
        attrs = ''
        if end:
            attrs += f' marker-end="url(#arrow-{key})"'
        if start:
            attrs += f' marker-start="url(#arrow-{key})"'
        if dashed:
            attrs += ' stroke-dasharray="8 8"'
        self.parts.append(f'<polyline points="{coords}" fill="none" stroke="{color}" stroke-width="{width}" stroke-linejoin="round" stroke-linecap="round"{attrs}/>')

    def arrow(self, points, **kw):
        self.line(points, end=True, **kw)

    def pill(self, x, y, label, *, fill=TEAL_BG, color=TEAL, size=20, width=None):
        w = width or measure(label, size, True) + 28
        self.rect(x, y, w, 36, fill=fill, stroke="none", radius=9)
        self.text(x + 14, y + 25, label, size=size, bold=True, fill=color)

    def card(self, x, y, w, h, title, body="", *, fill=WHITE, accent=None, title_size=28, body_size=24, body_top=78, title_fill=NAVY, body_fill=MUTED):
        self.rect(x, y, w, h, fill=fill)
        if accent:
            self.rect(x + 24, y + 22, 7, 30, fill=accent, stroke="none", radius=3)
            tx = x + 47
        else:
            tx = x + 26
        self.text(tx, y + 46, title, size=title_size, bold=True, fill=title_fill)
        if body:
            self.para(x + 26, y + body_top, body, w - 52, size=body_size, leading=35, fill=body_fill)

    def save(self, png=True):
        path = ROOT / f"{self.name}.svg"
        path.write_text("\n".join(self.parts) + "\n</g>\n</svg>\n", encoding="utf-8")
        if png:
            if not shutil.which("inkscape"):
                raise RuntimeError("Inkscape is required for PNG export. Use --svg-only for SVG output.")
            temporary = ROOT / (self.name + ".tmp.png")
            target = ROOT / (self.name + ".png")
            try:
                subprocess.run([
                    "inkscape", "--batch-process", str(path), "--export-type=png",
                    f"--export-filename={temporary}",
                    f"--export-width={WIDTH * SCALE}",
                ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
                with Image.open(temporary) as rendered:
                    rendered.verify()
                with Image.open(temporary) as rendered:
                    rendered.load()
                    if rendered.size != (WIDTH * SCALE, self.height * SCALE):
                        raise RuntimeError(f"Unexpected PNG dimensions: {rendered.size}")
                    encoded = rendered.convert("RGB")
                encoded.save(temporary, format="PNG", optimize=True)
                with Image.open(temporary) as checked:
                    checked.verify()
                temporary.replace(target)
            finally:
                temporary.unlink(missing_ok=True)
        return path


def system_context():
    s = SVG("01-system-context", 1155, "System context", "goen, PostgreSQL and external providers.")
    s.arrow([(195, 285), (195, 225), (1400, 225), (1400, 300)], color=TEAL)
    s.rect(669, 208, 274, 34, fill=BG, stroke="none", radius=0)
    s.text(806, 232, "Hosted checkout", size=23, bold=True, fill=TEAL, anchor="middle")

    s.card(60, 285, 270, 135, "Shopper", "Browse\nOrder · pay", accent=TEAL)
    s.card(60, 495, 270, 130, "Staff", "Operate\nReconcile", accent=TEAL)

    s.rect(440, 285, 720, 640, fill=TEAL_BG, stroke="#A9C9BE", width=2)
    s.text(470, 328, "goen", size=27, bold=True, fill=TEAL)
    s.card(470, 365, 660, 160, "HTTP", "Storefront · accounts · cart · checkout\nPayments · back office", title_size=29, body_size=25)
    s.card(470, 575, 660, 110, "Assets + media", "Embedded assets · local renditions", title_size=28, body_size=25)
    s.card(470, 740, 660, 130, "Workers", "Outbox · invoice recovery · cleanup\nRecommendation refresh", title_size=27, body_size=25)

    s.arrow([(330, 350), (385, 350), (385, 420), (470, 420)])
    s.arrow([(330, 550), (405, 550), (405, 480), (470, 480)])
    s.arrow([(800, 525), (800, 575)], color=MUTED)

    s.card(1270, 300, 270, 195, "Stripe", "Hosted payment\nCheckout API\nSigned webhooks", title_size=29, body_size=24)
    s.arrow([(1130, 395), (1270, 395)])
    s.text(1200, 376, "API", size=22, fill=MUTED, anchor="middle")
    s.arrow([(1270, 455), (1130, 455)], color=TEAL, dashed=True)
    s.text(1200, 438, "webhook", size=22, fill=TEAL, anchor="middle")
    s.card(1270, 520, 270, 95, "Google", "Optional sign-in", title_size=27, body_size=22)
    s.line([(1130, 490), (1255, 490), (1255, 567), (1270, 567)], color=MUTED, dashed=True, end=True)

    s.card(1270, 640, 270, 170, "ECPay", "Invoices · lookup\nBarcode checks\nStore map", title_size=29, body_size=24)
    s.line([(1130, 510), (1240, 510), (1240, 675), (1270, 675)], color=NAVY, start=True, end=True)
    s.line([(1130, 780), (1200, 780), (1200, 715), (1270, 715)], color=NAVY, start=True, end=True)
    s.card(1270, 830, 270, 120, "SMTP", "Transactional mail", title_size=29, body_size=23)
    s.arrow([(1130, 840), (1210, 840), (1210, 890), (1270, 890)])

    s.arrow([(800, 925), (800, 1020)], color=TEAL)
    s.text(830, 981, "store · admin · maintenance", size=23, fill=TEAL)
    s.card(440, 1020, 720, 145, "PostgreSQL", "Orders · inventory · payments\nJobs · media · read models", fill=NAVY, title_size=28, title_fill=WHITE, body_fill="#D8E6E1")
    return s


def checkout_payment():
    s = SVG("02-checkout-payment", 1500, "Checkout and payment", "Normal card-payment flow; each marked commit is local.")
    xs = {"Buyer": 160, "goen": 565, "PostgreSQL": 1015, "Stripe": 1440}
    # Phase backgrounds are drawn first. Label backplates mask lifelines later.
    phases = [
        (335, 263, "A  ·  PLACE ORDER", TEAL, TEAL_BG),
        (625, 425, "B  ·  START PAYMENT — NEW REQUEST", NAVY, SOFT),
        (1080, 270, "C  ·  APPLY SIGNED WEBHOOK", TEAL, TEAL_BG),
    ]
    for y, h, label, color, fill in phases:
        s.rect(60, y, 1480, h, fill=fill, stroke="none", radius=15)
    for name, x in xs.items():
        s.rect(x - 110, 225, 220, 80, fill=NAVY if name in ("goen", "PostgreSQL") else WHITE, stroke=LINE)
        s.text(x, 275, name, size=26, bold=True, fill=WHITE if name in ("goen", "PostgreSQL") else NAVY, anchor="middle")
        s.line([(x, 305), (x, 1350)], color=LIFELINE, width=1.7, dashed=True)
    for y, h, label, color, fill in phases:
        s.rect(78, y + 12, measure(label, 21, True) + 10, 30, fill=fill, stroke="none", radius=0)
        s.text(83, y + 35, label, size=21, bold=True, fill=color)

    def msg(a, b, y, label, *, ret=False, color=NAVY, size=22):
        x1, x2 = xs[a], xs[b]
        s.arrow([(x1, y), (x2, y)], color=color, dashed=ret)
        lines = label.split("\n")
        for i, line in enumerate(lines):
            baseline = y - 15 - (len(lines) - 1 - i) * 28
            back = TEAL_BG if y < 600 or y > 1080 else SOFT
            lw = measure(line, size)
            s.rect((x1 + x2 - lw) / 2 - 5, baseline - size, lw + 10, size + 5, fill=back, stroke="none", radius=0)
            s.text((x1 + x2) / 2, baseline, line, size=size, fill=color, anchor="middle")

    msg("Buyer", "goen", 413, "Confirm checkout")
    msg("goen", "PostgreSQL", 482, "Recheck quote + reserve stock\nSnapshot order + write outbox")
    msg("PostgreSQL", "goen", 535, "Commit A", ret=True, color=TEAL)
    msg("goen", "Buyer", 583, "Redirect to payment page")

    msg("Buyer", "goen", 704, "Start payment")
    msg("goen", "Stripe", 761, "Create Checkout Session · idempotency key")
    msg("Stripe", "goen", 818, "Session result", ret=True)
    msg("goen", "PostgreSQL", 881, "OpenPayment: admit + bind Session", size=21)
    msg("PostgreSQL", "goen", 932, "Commit B", ret=True, color=TEAL)
    msg("goen", "Stripe", 985, "Retrieve current Session")
    msg("goen", "Buyer", 1038, "Redirect to hosted checkout", size=21)

    msg("Stripe", "goen", 1156, "Signed webhook", color=TEAL)
    msg("goen", "PostgreSQL", 1225, "Claim event + verify payment\nCapture + rewards + outbox")
    msg("PostgreSQL", "goen", 1282, "Commit C", ret=True, color=TEAL)
    msg("goen", "Stripe", 1338, "200 after commit")

    s.card(60, 1390, 920, 120, "Early webhook", "A webhook may precede commit B. Both paths lock the provider reference.", fill=WHITE, title_size=24, body_size=22, body_top=76)
    s.card(1010, 1390, 530, 120, "Browser return ≠ payment proof", "Paid state follows verified provider evidence.", fill=AMBER_BG, title_size=23, body_size=22, body_top=76)
    return s


def stock_race():
    s = SVG("03-stock-payment-race", 1190, "Payment–stock race", "Payment capture and stock release serialize on the order lock.")
    s.card(70, 235, 635, 150, "Payment capture", "Verified provider payment.", accent=TEAL, title_size=28, body_size=25)
    s.card(895, 235, 635, 150, "Reservation release", "Expiry or cancellation.", accent=AMBER, title_size=28, body_size=25)
    s.arrow([(388, 385), (388, 425), (690, 425), (690, 465)], color=TEAL)
    s.arrow([(1212, 385), (1212, 425), (910, 425), (910, 465)], color=AMBER)
    s.card(490, 465, 620, 135, "Order lock", "First committed transition wins.", fill=NAVY, title_size=30, body_size=25, title_fill=WHITE, body_fill="#D8E6E1")

    s.arrow([(670, 600), (670, 640), (390, 640), (390, 700)], color=TEAL)
    s.arrow([(930, 600), (930, 640), (1210, 640), (1210, 700)], color=AMBER)
    s.rect(294, 649, 192, 38, fill=BG, stroke="none", radius=0)
    s.rect(1114, 649, 192, 38, fill=BG, stroke="none", radius=0)
    s.text(390, 678, "Capture wins", size=24, bold=True, fill=TEAL, anchor="middle")
    s.text(1210, 678, "Release wins", size=24, bold=True, fill=AMBER, anchor="middle")

    s.rect(70, 700, 650, 305, fill=TEAL_BG, stroke="#AACBBF")
    s.text(102, 747, "ORDER COMMITTED", size=25, bold=True, fill=TEAL)
    s.para(102, 802, "Verified payment captured.", 582, size=27, bold=True, fill=NAVY)
    s.line([(102, 855), (688, 855)], color="#B6D1C7", width=1.5)
    s.para(102, 903, "Release refused; stock stays reserved.", 582, size=26, leading=39)

    s.rect(880, 700, 650, 305, fill=AMBER_BG, stroke="#DBC8AB")
    s.text(912, 747, "RESERVATION RELEASED", size=25, bold=True, fill=AMBER)
    s.para(912, 802, "Release persisted.", 582, size=27, bold=True, fill=NAVY)
    s.line([(912, 855), (1498, 855)], color="#DECDB4", width=1.5)
    s.para(912, 903, "Late capture is refused locally; an external charge may exist.", 582, size=26, leading=39)

    s.arrow([(390, 1005), (390, 1050)], color=TEAL)
    s.arrow([(1210, 1005), (1210, 1050)], color=AMBER)
    s.card(70, 1050, 650, 150, "Fulfillment can continue", "Payment and stock commitment agree.", fill=WHITE, title_size=25, body_size=24)
    s.card(880, 1050, 650, 150, "Durable exception → operator refund", "Reconcile the charge without reviving released stock.", fill=WHITE, title_size=24, body_size=24)
    return s


def durable_work():
    s = SVG("04-durable-work", 1450, "Durable background work", "Outbox delivery and invoice issuance are tracked separately.")
    s.rect(60, 225, 1480, 180, fill=TEAL_BG, stroke="#A9C9BE", width=2)
    s.text(88, 267, "PostgreSQL transaction", size=23, bold=True, fill=TEAL)
    s.card(88, 290, 560, 87, "Business records", title_size=28)
    s.text(688, 348, "+", size=34, bold=True, fill=TEAL, anchor="middle")
    s.card(728, 290, 784, 87, "outbox_messages", title_size=28)
    s.arrow([(800, 405), (800, 465)], color=TEAL)

    s.card(435, 465, 730, 175, "Claim due outbox work", "SKIP LOCKED · durable lease · owner token\nBatch 6; sequential handlers", title_size=28, body_size=25)
    s.arrow([(650, 640), (650, 686), (345, 686), (345, 745)])
    s.rect(266, 698, 158, 35, fill=BG, stroke="none", radius=0)
    s.text(345, 726, "mail topics", size=23, bold=True, fill=TEAL, anchor="middle")
    s.arrow([(1010, 640), (1010, 686), (1175, 686), (1175, 745)])
    s.rect(1096, 698, 158, 35, fill=BG, stroke="none", radius=0)
    s.text(1175, 726, "invoice.due · void_due", size=23, bold=True, fill=TEAL, anchor="middle")

    s.card(60, 745, 570, 205, "SMTP delivery", "Send → mark delivered\nA crash in between may duplicate delivery.", title_size=29, body_size=25)
    s.card(810, 745, 730, 170, "ClaimDue · ClaimVoidDue", "Still owed: commit invoice_operations.\nMet or withdrawn: no-op.\nThen acknowledge.", title_size=26, body_size=25)

    s.rect(60, 1010, 570, 130, fill=AMBER_BG, stroke="#DECBAE")
    s.text(88, 1058, "Outbox ack ≠ invoice issued", size=25, bold=True, fill=AMBER)
    s.text(88, 1112, "Handoff or no work owed.", size=25, fill=MUTED)

    s.rect(810, 985, 730, 475, fill=TEAL_BG, stroke="#A9C9BE")
    s.text(838, 1027, "Invoice processing", size=23, bold=True, fill=TEAL)
    s.arrow([(1450, 915), (1450, 1050)], color=TEAL)
    s.text(1430, 971, "if still owed", size=22, fill=TEAL, anchor="end")
    s.card(838, 1050, 674, 105, "invoice_operations", "Pending work · attention", title_size=27, body_size=23, body_top=79)
    s.arrow([(1015, 1155), (1015, 1203)], color=TEAL)
    s.card(838, 1203, 354, 140, "Reconciler", "Lookup · submit\nVerify evidence", title_size=28, body_size=24)
    s.card(1274, 1203, 238, 140, "ECPay", "Invoice API", title_size=28, body_size=24)
    s.arrow([(1192, 1245), (1274, 1245)])
    s.arrow([(1274, 1298), (1192, 1298)], color=TEAL, dashed=True)
    s.arrow([(1015, 1343), (1015, 1378)], color=TEAL)
    s.card(838, 1378, 674, 64, "Verified invoice_documents", title_size=25)
    return s


def resource_boundaries():
    s = SVG("05-resource-boundaries", 1320, "Resource budgets", "Per-process limits and shared PostgreSQL resources.")
    s.rect(60, 225, 1480, 560, fill=TEAL_BG, stroke="#A9C9BE", width=2)
    s.text(88, 271, "goen", size=24, bold=True, fill=TEAL)
    s.text(88, 317, "Request budget 25 s · media 25 s read, 30 s render · webhooks exempt", size=24, fill=MUTED)

    positions = [(100, "Storefront + outbox", "Auth · cart · checkout\nOutbox · customer cleanup", "store", "Max 25 connections", "15 s / statement"),
                 (585, "Back office + invoices", "Staff operations\nReconciliation · media cleanup", "admin", "Max 10 connections", "30 s / statement"),
                 (1070, "Recommendation refresh", "Co-purchase projection\nAdvisory lock", "maintenance", "Max 2 connections", "5 min / statement")]
    for x, title, body, pool, n, timeout in positions:
        s.card(x, 365, 430, 157, title, body, title_size=24, body_size=23)
        s.arrow([(x + 215, 522), (x + 215, 581)], color=TEAL)
        s.rect(x, 581, 430, 160, fill=WHITE)
        s.text(x + 26, 624, pool, size=28, bold=True, fill=TEAL)
        s.text(x + 26, 672, n, size=29, bold=True)
        s.text(x + 26, 711, timeout, size=24, fill=MUTED)

    # Admin authentication remains on the store pool: a compact cross-boundary edge.
    s.arrow([(585, 450), (530, 450)], color=AMBER, dashed=True)
    s.text(557, 435, "auth", size=20, bold=True, fill=AMBER, anchor="middle")
    for x in [315, 800, 1285]:
        s.arrow([(x, 741), (x, 866)], color=TEAL)

    s.rect(100, 866, 1400, 145, fill=NAVY, stroke=NAVY)
    s.text(130, 913, "PostgreSQL", size=28, bold=True, fill=WHITE)
    s.text(130, 962, "Shared CPU · memory · I/O · WAL · locks", size=31, bold=True, fill=WHITE)

    s.card(60, 1075, 715, 255, "Image limits", "64 MiB encoded cache per renderer\nRender slots: max(1, min(GOMAXPROCS, 4))\nUpload slots: 2\nsingleflight joins identical misses", title_size=28, body_size=24)
    s.card(815, 1075, 725, 255, "More processes", "N processes → up to 37N app connections\nWorkers run in each process\nCaches and rate limits are local", title_size=28, body_size=23)
    return s


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--svg-only", action="store_true", help="Do not invoke Inkscape.")
    parser.add_argument("--only", choices=["01", "02", "03", "04", "05"], help="Render one figure.")
    args = parser.parse_args()
    figures = [system_context, checkout_payment, stock_race, durable_work, resource_boundaries]
    for idx, make in enumerate(figures, start=1):
        if args.only and int(args.only) != idx:
            continue
        figure = make()
        path = figure.save(png=not args.svg_only)
        print(f"{path.name}: {WIDTH} × {figure.height} SVG; PNG scale {SCALE}×")


if __name__ == "__main__":
    main()
