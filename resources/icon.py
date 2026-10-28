#!/usr/bin/env python3
"""
The Quire app icon, drawn once and rendered two ways.

    python3 resources/icon.py

writes

    resources/icon.svg           the vector source of the shipped icon
    resources/icon.png           1024×1024 raster for `mygo build`
    resources/icon-light.svg     the same mark on paper instead of ink
    resources/icon-light.png
    resources/icons/*.png        16…512 px, and an .ico for Windows
    resources/icon-sheet.png     the sizes side by side, for review

The mark is a *quire*: a gathering of folded sheets opened to its two-page
spread. The spread is the reader's own signature layout, the cream is the Calm
paper, the terracotta line is the accent — the line you are on — and the corner
of the right-hand sheet is turned down, the page you left.

Geometry is defined once, in logical 1024×1024 units, and both writers read it:
the SVG emits the same flattened polygons as the raster, so the two files are
one drawing instead of two drawings that resemble each other.
"""

import pathlib

from PIL import Image, ImageChops, ImageDraw, ImageFilter

# --- geometry, in logical 1024×1024 units --------------------------------

CANVAS = 1024
BODY = (100, 100, 924, 924)  # the squircle macOS icons live in
BODY_R = 184  # ≈22.4% of the body: Apple's corner radius

GUTTER_X = 512  # the fold down the middle of the spread
OUT_L, OUT_R = 272, 752  # the outer edges of the two pages
TOP_OUT, TOP_GUT = 354, 375  # the sheets ride up towards their outer edges
BOT_GUT, BOT_OUT = 649, 670
PAGE_R = 12  # page corners
FOLD = 84  # how far the dog-ear reaches along the bottom and right edges
K = 1.15  # how much of the body the spread takes up

# Text lines: (x0, x1, top, height), one list per page. The head line is
# heavier, and the last line on the right page is the accent.
TEXT_LEFT = [
    (316, 444, 420, 21),
    (316, 468, 464, 14),
    (316, 456, 500, 14),
    (316, 468, 536, 14),
    (316, 404, 572, 14),
]
TEXT_RIGHT = [
    (556, 690, 420, 21),
    (556, 708, 464, 14),
    (556, 700, 500, 14),
    (556, 708, 536, 14),
    (556, 634, 572, 14),
]
ACCENT_LINE = 4  # index into TEXT_RIGHT

# Page gradients run from the gutter (0) to the outer edge (1): the sheets are
# lit from their outer edges and fall into shadow along the fold.
VARIANTS = {
    "icon": {
        "body": [(0.00, "#3A322A"), (1.00, "#1F1A16")],
        "body_edge": ("#FFF0D8", 0.10),  # the hairline of light Big Sur icons have
        "left": [(0.00, "#D6C5A4"), (0.30, "#EBDFC4"), (1.00, "#F0E5CC")],
        "right": [(0.00, "#D9C7A3"), (0.34, "#F5EAD3"), (1.00, "#F9F0DE")],
        "leaf": "#DAC9A8",  # multiplies the paper of the sheet underneath
        "seam": ("#FFF4DE", 0.14),
        "ink": "#5C4A33",
        "head": "#3F3222",
        "accent": "#C05039",
        "flap": "#F7ECD6",  # the back of the turned-down corner
        "under": "#C9B18A",  # the sheet showing through where it was
        "fold_shade": ("#3A2A16", 0.34),
        "shadow": ("#000000", 0.45),
    },
    "icon-light": {
        "body": [(0.00, "#EFE4CE"), (1.00, "#D9C8A6")],
        "body_edge": ("#FFFFFF", 0.55),
        "left": [(0.00, "#D8C7A5"), (0.28, "#FBF5E7"), (1.00, "#FFFCF3")],
        "right": [(0.00, "#E1D0AE"), (0.30, "#FFFAF0"), (1.00, "#FFFFFF")],
        "leaf": "#EDE1C8",
        "seam": ("#FFFFFF", 0.55),
        "ink": "#6B5A42",
        "head": "#4A3A28",
        "accent": "#C05039",
        "flap": "#FDF7EA",
        "under": "#D6C09A",
        "fold_shade": ("#6B5535", 0.24),
        "shadow": ("#3A2C18", 0.22),
    },
}

SS = 3  # supersampling: drawn at 3×, downsampled with LANCZOS


# --- small canvas helpers -------------------------------------------------


def hex_rgb(h):
    h = h.lstrip("#")
    return tuple(int(h[i : i + 2], 16) for i in (0, 2, 4))


def color_at(stops, t):
    """Colour of a gradient at t ∈ [0,1], eased between its stops."""
    t = min(1.0, max(0.0, t))
    if t <= stops[0][0]:
        return hex_rgb(stops[0][1])
    for (p0, c0), (p1, c1) in zip(stops, stops[1:]):
        if t <= p1:
            u = 0.0 if p1 == p0 else (t - p0) / (p1 - p0)
            u = u * u * (3 - 2 * u)  # smoothstep: no banding at the stops
            a, b = hex_rgb(c0), hex_rgb(c1)
            return tuple(round(x + (y - x) * u) for x, y in zip(a, b))
    return hex_rgb(stops[-1][1])


def ramp(width, height, x0, x1, stops):
    """A horizontal gradient from x0 (stop 0) to x1 (stop 1), as RGBA."""
    img = Image.new("RGBA", (width, height))
    d = ImageDraw.Draw(img)
    for x in range(width):
        t = (x - x0) / (x1 - x0)
        r, g, b = color_at(stops, t)
        d.line([(x, 0), (x, height)], fill=(r, g, b, 255))
    return img


def flatten(points, r, segs=10):
    """A polygon with its corners rounded to r, as a point list.

    Both writers take the flattened list, so the SVG and the raster round the
    corners identically instead of each approximating the other.
    """
    if r <= 0:
        return list(points)
    n = len(points)
    out = []
    for i, b in enumerate(points):
        a, c = points[i - 1], points[(i + 1) % n]

        def toward(p, q):
            dx, dy = q[0] - p[0], q[1] - p[1]
            d = (dx * dx + dy * dy) ** 0.5
            return (p[0] + dx / d * r, p[1] + dy / d * r) if d else p

        p0, p1 = toward(b, a), toward(b, c)
        out.append(p0)
        for k in range(1, segs):
            t = k / segs
            u = 1 - t
            out.append(
                (
                    u * u * p0[0] + 2 * u * t * b[0] + t * t * p1[0],
                    u * u * p0[1] + 2 * u * t * b[1] + t * t * p1[1],
                )
            )
        out.append(p1)
    return out


def against_middle(p):
    """Scale a logical point about the middle of the icon by K."""
    return (512 + (p[0] - 512) * K, 512 + (p[1] - 512) * K)


def fit(points):
    return [against_middle(p) for p in points]


def shape(points, r=PAGE_R):
    """A rounded polygon, scaled into the spread's share of the body."""
    return fit(flatten(points, r))


def rounded(x0, y0, x1, y1, r):
    """A rounded rectangle as a flat point list, ready for either writer."""
    return flatten([(x0, y0), (x1, y0), (x1, y1), (x0, y1)], r)


LEFT_PAGE = shape([(OUT_L, TOP_OUT), (GUTTER_X, TOP_GUT), (GUTTER_X, BOT_GUT), (OUT_L, BOT_OUT)])
RIGHT_PAGE = shape([(GUTTER_X, TOP_GUT), (OUT_R, TOP_OUT), (OUT_R, BOT_OUT), (GUTTER_X, BOT_GUT)])

# The dog-ear. The corner of the right-hand sheet is turned down: the crease
# runs from a point FOLD down the bottom edge to one FOLD up the right edge, the
# triangle that held the tip now shows the sheet below, and that triangle folded
# over the crease — its mirror image — shows the back of the sheet on the page.
CORNER = (OUT_R, BOT_OUT)
FOLD_A = (  # on the bottom edge, which slopes towards the fold
    OUT_R - FOLD,
    BOT_GUT + (OUT_R - FOLD - GUTTER_X) * (BOT_OUT - BOT_GUT) / (OUT_R - GUTTER_X),
)
FOLD_B = (OUT_R, BOT_OUT - FOLD)  # on the right edge


def mirror(p, a, b):
    """p reflected across the line through a and b."""
    dx, dy = b[0] - a[0], b[1] - a[1]
    d = (dx * dx + dy * dy) ** 0.5
    vx, vy = p[0] - a[0], p[1] - a[1]
    along = (vx * dx + vy * dy) / d
    return (a[0] + 2 * along * dx / d - vx, a[1] + 2 * along * dy / d - vy)


FOLD_UNDER = shape([FOLD_A, FOLD_B, CORNER])
FOLD_FLAP = shape([FOLD_A, FOLD_B, mirror(CORNER, FOLD_A, FOLD_B)])

# Every line of text, with the colour it is set in, in drawing order.
TEXT = [(t, "head" if t[3] > 20 else "ink") for t in TEXT_LEFT] + [
    (t, "accent" if i == ACCENT_LINE else ("head" if t[3] > 20 else "ink"))
    for i, t in enumerate(TEXT_RIGHT)
]


def line_points(t):
    x0, x1, top, h = t
    return fit(rounded(x0, top, x1, top + h, h / 2))


# --- the raster -----------------------------------------------------------


def render(v, size=1024, ss=SS):
    w = size * ss
    s = w / CANVAS

    def px(points):
        return [(x * s, y * s) for x, y in points]

    def blank():
        return Image.new("RGBA", (w, w), (0, 0, 0, 0))

    def draw_into(layer, points, fill, blur=0, offset=(0, 0), alpha=255):
        """Paint one polygon into a layer, softened and offset if asked."""
        mask = Image.new("L", (w, w), 0)
        ImageDraw.Draw(mask).polygon(px(points), fill=255)
        if blur:
            mask = mask.filter(ImageFilter.GaussianBlur(blur * s))
        if alpha != 255:
            mask = mask.point(lambda p: p * alpha // 255)
        ink = Image.new("RGBA", (w, w), fill + (255,))
        layer.paste(ink, (round(offset[0] * s), round(offset[1] * s)), mask)

    # The body: a squircle with a top-to-bottom gradient.
    top, bottom = hex_rgb(v["body"][0][1]), hex_rgb(v["body"][-1][1])
    strip = Image.new("RGBA", (1, w))
    for y in range(w):
        u = y / (w - 1)
        u = u * u * (3 - 2 * u)
        strip.putpixel(
            (0, y),
            tuple(round(a + (b - a) * u) for a, b in zip(top, bottom)) + (255,),
        )
    body_mask = Image.new("L", (w, w), 0)
    ImageDraw.Draw(body_mask).rounded_rectangle(
        tuple(c * s for c in BODY), radius=BODY_R * s, fill=255
    )
    canvas = blank()
    canvas.paste(strip.resize((w, w), Image.BILINEAR), (0, 0), body_mask)

    def clip(layer):
        canvas.alpha_composite(Image.composite(layer, blank(), body_mask))

    # What the open sheets cast on the body.
    cast = blank()
    draw_into(
        cast,
        LEFT_PAGE,
        hex_rgb(v["shadow"][0]),
        blur=26,
        offset=(0, 14),
        alpha=round(255 * v["shadow"][1]),
    )
    draw_into(
        cast,
        RIGHT_PAGE,
        hex_rgb(v["shadow"][0]),
        blur=26,
        offset=(0, 14),
        alpha=round(255 * v["shadow"][1]),
    )
    clip(cast)

    # The two pages, lit from their outer edges and shadowed at the fold.
    def sheet(page, stops, outer):
        g = ramp(w, w, GUTTER_X * s, against_middle((outer, 0))[0] * s, stops)
        mask = Image.new("L", (w, w), 0)
        ImageDraw.Draw(mask).polygon(px(page), fill=255)
        layer = blank()
        layer.paste(g, (0, 0), mask)
        return layer

    pages = [
        sheet(LEFT_PAGE, v["left"], OUT_L),
        sheet(RIGHT_PAGE, v["right"], OUT_R),
    ]

    # The sheets still folded under the open one: the same paper, one step
    # down, showing along the top edges.
    under_leaf = blank()
    for layer in pages:
        dim = ImageChops.multiply(
            layer.convert("RGB"), Image.new("RGB", (w, w), hex_rgb(v["leaf"]))
        ).convert("RGBA")
        dim.putalpha(layer.getchannel("A"))
        lifted = blank()
        lifted.paste(dim, (0, round(-11 * s)), dim)
        under_leaf.alpha_composite(lifted)
    clip(under_leaf)

    for layer in pages:
        clip(layer)

    # The crease: a hair of light down the middle of the fold.
    c, a = v["seam"]
    crease = blank()
    ImageDraw.Draw(crease).rectangle(
        [GUTTER_X * s - 0.75 * s, against_middle((0, TOP_GUT))[1] * s,
         GUTTER_X * s + 0.75 * s, against_middle((0, BOT_GUT))[1] * s],
        fill=hex_rgb(c) + (round(255 * a),),
    )
    clip(crease)

    # The text, printed on the paper and clipped to it.
    paper = Image.new("L", (w, w), 0)
    paper_draw = ImageDraw.Draw(paper)
    for page in (LEFT_PAGE, RIGHT_PAGE):
        paper_draw.polygon(px(page), fill=255)
    lines = blank()
    for t, kind in TEXT:
        draw_into(lines, line_points(t), hex_rgb(v[kind]))
    printed = blank()
    printed.paste(lines, (0, 0), paper)
    clip(printed)

    # The turned-down corner: the sheet below first, then its shadow, then the
    # back of the folded sheet lying over the page.
    underside = blank()
    draw_into(underside, FOLD_UNDER, hex_rgb(v["under"]))
    clip(underside)

    fold_shade = blank()
    draw_into(
        fold_shade,
        FOLD_FLAP,
        hex_rgb(v["fold_shade"][0]),
        blur=7,
        offset=(4, 5),
        alpha=round(255 * v["fold_shade"][1]),
    )
    clip(fold_shade)

    flap = blank()
    draw_into(flap, FOLD_FLAP, hex_rgb(v["flap"]))
    clip(flap)

    # The rim of light along the body's edge.
    rim = blank()
    c, a = v["body_edge"]
    ImageDraw.Draw(rim).rounded_rectangle(
        tuple(BODY[i] * s + (2 * s if i < 2 else -2 * s) for i in range(4)),
        radius=BODY_R * s,
        outline=hex_rgb(c) + (round(255 * a),),
        width=max(1, round(3 * s)),
    )
    canvas.alpha_composite(Image.composite(rim, blank(), body_mask))

    if ss != 1:
        canvas = canvas.resize((size, size), Image.LANCZOS)
    return canvas


# --- the vector -----------------------------------------------------------


def dimmed(stops, by):
    """The stops of a gradient multiplied by a filter colour."""
    f = hex_rgb(by)
    out = []
    for p, c in stops:
        r, g, b = hex_rgb(c)
        out.append((p, "#%02x%02x%02x" % tuple(round(x * y / 255) for x, y in zip((r, g, b), f))))
    return out


def svg(v):
    def points(pts):
        return " ".join(f"{x:.2f},{y:.2f}" for x, y in pts)

    def poly(pts, fill, extra=""):
        return f'<polygon points="{points(pts)}" fill="{fill}"{extra}/>'

    def stops(stops):
        out = []
        for p, c in stops:
            r, g, b = hex_rgb(c)
            out.append(f'<stop offset="{p:.3f}" stop-color="#{r:02x}{g:02x}{b:02x}"/>')
        return "".join(out)

    body_x, body_y, body_x1, body_y1 = BODY
    body_w, body_h = body_x1 - body_x, body_y1 - body_y
    gutter = GUTTER_X
    outer_l = against_middle((OUT_L, 0))[0]
    outer_r = against_middle((OUT_R, 0))[0]

    text = "\n    ".join(poly(line_points(t), v[kind]) for t, kind in TEXT)

    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {CANVAS} {CANVAS}" width="{CANVAS}" height="{CANVAS}">
  <title>Quire</title>
  <desc>An open quire: two facing pages of a folded gathering, its right-hand corner turned down, the line being read in terracotta.</desc>
  <defs>
    <linearGradient id="body" x1="0" y1="0" x2="0" y2="1">{stops(v["body"])}</linearGradient>
    <linearGradient id="left" gradientUnits="userSpaceOnUse" x1="{gutter}" y1="0" x2="{outer_l:.2f}" y2="0">{stops(v["left"])}</linearGradient>
    <linearGradient id="right" gradientUnits="userSpaceOnUse" x1="{gutter}" y1="0" x2="{outer_r:.2f}" y2="0">{stops(v["right"])}</linearGradient>
    <linearGradient id="left-under" gradientUnits="userSpaceOnUse" x1="{gutter}" y1="0" x2="{outer_l:.2f}" y2="0">{stops(dimmed(v["left"], v["leaf"]))}</linearGradient>
    <linearGradient id="right-under" gradientUnits="userSpaceOnUse" x1="{gutter}" y1="0" x2="{outer_r:.2f}" y2="0">{stops(dimmed(v["right"], v["leaf"]))}</linearGradient>
    <filter id="drop" x="-30%" y="-30%" width="160%" height="160%"><feGaussianBlur stdDeviation="18"/></filter>
    <filter id="soft" x="-30%" y="-30%" width="160%" height="160%"><feGaussianBlur stdDeviation="5"/></filter>
    <clipPath id="body-clip"><rect x="{body_x}" y="{body_y}" width="{body_w}" height="{body_h}" rx="{BODY_R}"/></clipPath>
  </defs>

  <g clip-path="url(#body-clip)">
    <rect x="{body_x}" y="{body_y}" width="{body_w}" height="{body_h}" fill="url(#body)"/>

    <!-- the shadow the open sheets cast on the body -->
    <g filter="url(#drop)" opacity="{v["shadow"][1]}" transform="translate(0 14)">
      {poly(LEFT_PAGE, v["shadow"][0])}
      {poly(RIGHT_PAGE, v["shadow"][0])}
    </g>

    <!-- the sheets still folded under the open one -->
    <g transform="translate(0 -11)">
      {poly(LEFT_PAGE, "url(#left-under)")}
      {poly(RIGHT_PAGE, "url(#right-under)")}
    </g>

    <!-- the spread -->
    {poly(LEFT_PAGE, "url(#left)")}
    {poly(RIGHT_PAGE, "url(#right)")}
    <rect x="{gutter - 0.75}" y="{against_middle((0, TOP_GUT))[1]:.2f}" width="1.5" height="{against_middle((0, BOT_GUT))[1] - against_middle((0, TOP_GUT))[1]:.2f}" fill="{v["seam"][0]}" opacity="{v["seam"][1]}"/>

    <!-- the text of the two pages -->
    {text}

    <!-- the turned-down corner, and the shadow it lifts off the page -->
    {poly(FOLD_UNDER, v["under"])}
    <g filter="url(#soft)" opacity="{v["fold_shade"][1]}" transform="translate(4 5)">{poly(FOLD_FLAP, v["fold_shade"][0])}</g>
    {poly(FOLD_FLAP, v["flap"])}
  </g>

  <rect x="{body_x + 2}" y="{body_y + 2}" width="{body_w - 4}" height="{body_h - 4}" rx="{BODY_R}" fill="none" stroke="{v["body_edge"][0]}" stroke-opacity="{v["body_edge"][1]}" stroke-width="3"/>
</svg>
"""


# --- what the script writes ----------------------------------------------


def main():
    here = pathlib.Path(__file__).resolve().parent
    icons = here / "icons"
    icons.mkdir(exist_ok=True)

    for name, v in VARIANTS.items():
        (here / f"{name}.svg").write_text(svg(v), encoding="utf-8")
        big = render(v)
        big.save(here / f"{name}.png")
        for px in (512, 256, 128, 64, 48, 32, 16):
            big.resize((px, px), Image.LANCZOS).save(icons / f"{name}-{px}.png")
        print("wrote", name)

    # Windows wants one file holding every size.
    sheets = [Image.open(icons / f"icon-{px}.png") for px in (16, 32, 48, 64, 128, 256)]
    sheets[-1].save(icons / "icon.ico", sizes=[(s.width, s.height) for s in sheets])

    # A contact sheet at the sizes the icon is actually seen at.
    sizes = (512, 128, 64, 32, 16)
    pad = 40
    rows = [render(VARIANTS["icon"]), render(VARIANTS["icon-light"])]
    row_h = max(sizes) + 2 * pad
    sheet_w = sum(sizes) + pad * (len(sizes) + 1)
    sheet = Image.new("RGBA", (sheet_w, row_h * len(rows)), hex_rgb("#8A8A8E") + (255,))
    for row, art in enumerate(rows):
        x = pad
        for px in sizes:
            sheet.alpha_composite(art.resize((px, px), Image.LANCZOS), (x, row * row_h + pad))
            x += px + pad
    sheet.save(here / "icon-sheet.png")
    print("wrote icon-sheet.png")


if __name__ == "__main__":
    main()
