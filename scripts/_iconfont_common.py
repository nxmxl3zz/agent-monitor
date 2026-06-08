"""Shared helpers for the OpenUsage icon-font scripts.

Both ``gen-icon-font.py`` (which builds the standalone
``internal/tmux/assets/openusage-icons.ttf``) and ``patch-terminal-font.py``
(which grafts the same glyphs onto a user's terminal font) need to:

  * extract ``<path d>`` data from the monochrome provider SVGs,
  * record those paths into a single pen,
  * measure the *true* ink bounding box (real bezier extrema), and
  * build an affine transform that flips the SVG y-axis, scales the ink to
    fill the line height, and centers it.

That code is algorithm-identical between the two callers, so it lives here.
The only piece that legitimately differs is the target height / box size /
optional width clamp, so ``ink_transform`` is parameterized for that.

This is an internal helper module (underscore prefix); import it with::

    from _iconfont_common import (
        INK_FILL, SVG_VIEWBOX, CU2QU_MAX_ERR,
        extract_path_ds, record_svg, measure_ink_bbox, ink_transform,
        replay_quadratic, replay_cff,
    )
"""

from __future__ import annotations

import xml.etree.ElementTree as ET

from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.cu2quPen import Cu2QuPen
from fontTools.pens.recordingPen import RecordingPen
from fontTools.pens.t2CharStringPen import T2CharStringPen
from fontTools.pens.transformPen import TransformPen
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.svgLib.path import parse_path

# Fraction of the line height (em or ascender) that the ink height should
# occupy. We strip ALL whitespace around the icon (measuring true ink bounds)
# and fill almost the entire box so the glyph is as large as possible; a hair
# of margin keeps it from visually touching the very top/bottom edge.
INK_FILL = 0.98

# The provider SVGs always use viewBox="0 0 24 24".
SVG_VIEWBOX = 24.0

# Cubic->quadratic conversion tolerance, in font units. ~1 unit at upem=1000
# is well below pixel-perceptible at icon sizes.
CU2QU_MAX_ERR = 1.0


def extract_path_ds(svg_path):
    """Return the ``d`` attribute of every ``<path>`` element in an SVG file.

    Namespace-agnostic: handles documents with and without the SVG namespace
    declared (e.g. ``{http://www.w3.org/2000/svg}path`` and bare ``path``).
    """
    tree = ET.parse(svg_path)
    root = tree.getroot()
    ds = []
    for el in root.iter():
        tag = el.tag
        if not isinstance(tag, str):
            continue
        # Strip any '{namespace}' prefix: '{http://...}path' -> 'path'.
        local = tag.rsplit("}", 1)[-1]
        if local == "path":
            d = el.get("d")
            if d:
                ds.append(d)
    return ds


def record_svg(ds):
    """Parse every SVG path in *ds* into one RecordingPen and return it.

    Raises ``ValueError`` if *ds* is empty (nothing to record).
    """
    if not ds:
        raise ValueError("no <path d> data to record")
    rec = RecordingPen()
    for d in ds:
        parse_path(d, rec)
    return rec


def measure_ink_bbox(rec, src_label=""):
    """Measure the true ink bbox (real bezier extrema) of a recorded outline.

    Uses ``BoundsPen`` so curve extrema are accounted for, not just the control
    points. Returns ``(xmin, ymin, xmax, ymax)`` in SVG coordinates (y-down).

    Raises ``ValueError`` (including *src_label*) if the outline is empty or the
    bbox is degenerate (zero width or height).
    """
    bounds = BoundsPen(None)
    rec.replay(bounds)
    where = (" in %s" % src_label) if src_label else ""
    if bounds.bounds is None:
        raise ValueError("empty outline%s" % where)
    xmin, ymin, xmax, ymax = bounds.bounds
    if (xmax - xmin) <= 0 or (ymax - ymin) <= 0:
        raise ValueError("degenerate ink bbox%s" % where)
    return xmin, ymin, xmax, ymax


def ink_transform(rec, *, target_h, box_w, box_h, max_w=None, src_label=""):
    """Build the y-flip + scale-to-fill-height + center transform.

    The ink bbox (SVG coords, y-down) is measured, then scaled uniformly so the
    ink HEIGHT maps to *target_h*. If *max_w* is given, the scale is clamped so
    the ink WIDTH does not exceed it. The glyph is then centered horizontally on
    *box_w* and vertically on *box_h*.

    Returns ``(transform, scaled_w, scaled_h)`` where ``transform`` is an
    ``(a, b, c, d, e, f)`` affine tuple.
    """
    xmin, ymin, xmax, ymax = measure_ink_bbox(rec, src_label=src_label)
    ink_w = xmax - xmin
    ink_h = ymax - ymin

    # Uniform scale so the ink HEIGHT maps to target_h, preserving aspect ratio.
    scale = target_h / ink_h
    # Optional clamp so the width stays within the cell tolerance.
    if max_w is not None and ink_w * scale > max_w:
        scale = max_w / ink_w

    scaled_w = ink_w * scale
    scaled_h = ink_h * scale
    # Center the scaled ink inside the box on both axes.
    x_pad = (box_w - scaled_w) / 2.0
    y_pad = (box_h - scaled_h) / 2.0

    # Affine mapping svg(x, y) -> font(X, Y), with Y flipped (svg y is down):
    #   X = scale*(x - xmin) + x_pad
    #   Y = scale*(ymax - y) + y_pad
    # In (a, b, c, d, e, f) form (X = a*x + c*y + e ; Y = b*x + d*y + f):
    #   a = scale, c = 0, e = x_pad - scale*xmin
    #   b = 0, d = -scale, f = y_pad + scale*ymax
    transform = (
        scale,
        0.0,
        0.0,
        -scale,
        x_pad - scale * xmin,
        y_pad + scale * ymax,
    )
    return transform, scaled_w, scaled_h


def replay_quadratic(rec, transform):
    """Replay *rec* through *transform* into a quadratic ``glyf`` glyph.

    SVG paths use cubic beziers; the ``glyf`` table needs quadratics, so the
    outline is converted via ``Cu2QuPen`` (tolerance ``CU2QU_MAX_ERR``).
    """
    pen = TTGlyphPen(None)
    cu2qu = Cu2QuPen(pen, max_err=CU2QU_MAX_ERR, reverse_direction=True)
    tpen = TransformPen(cu2qu, transform)
    rec.replay(tpen)
    return pen.glyph()


def replay_cff(rec, transform, advance):
    """Replay *rec* through *transform* into a CFF (Type2) charstring."""
    t2_pen = T2CharStringPen(advance, None)
    tpen = TransformPen(t2_pen, transform)
    rec.replay(tpen)
    return t2_pen.getCharString()
