#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Cheap, pure-Python checks of a .pptx before it is rendered.

It needs only python-pptx (the pptx import) and Pillow, which every sandbox
profile has. It does not render anything, so it cannot tell that a deck looks
good. It catches the mistakes that make a deck unreadable and that a model
cannot see: shapes that collapsed to nothing, shapes outside the slide and text
that does not fit its box.

Why it exists: python-pptx reads a bare number as EMU (914400 per inch). A
model that writes ``add_shape(..., 0.5, 1.0, 4.0, 2.0)`` makes shapes of about
a thousandth of an inch. Use ``inch()`` for every position and size.

Use in a build script::

    from pptx_guard import inch, check
    slide.shapes.add_textbox(inch(0.5), inch(1), inch(4), inch(2))
    ...
    prs.save(path)
    check(path)          # raises DeckProblems with one line for each problem

Command line (exit 0 when clean, 1 when there are problems, 2 on a usage
error)::

    python3 pptx_guard.py deck.pptx [--min-size 0.2]

Text fit is an estimate. It wraps the words with Pillow at the font of the
text: the named font when a file for it (or its metric-compatible twin, such as
Carlito for Calibri) is installed, otherwise Liberation Sans, otherwise DejaVu
Sans, otherwise the font Pillow ships. A size that the file does not give (for
example a size inherited from a layout) counts as DEFAULT_FONT_PT. Line height is
1.2 times the size unless the paragraph sets a spacing. Tables, charts and
pictures are checked for size and position only. Render the deck and look at it
for everything else.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys

EMU_PER_INCH = 914400
EMU_PER_PT = 12700
# Used when neither the run, the paragraph nor the shape names a size.
DEFAULT_FONT_PT = 18.0
# A shape smaller than this in either direction is a mistake, except a line.
DEFAULT_MIN_SIZE_IN = 0.2
# A shape may stick out of the slide by this much before it is reported.
OFF_SLIDE_TOLERANCE_IN = 0.02
# The text of a box may be this much taller than the box before it is reported.
FIT_TOLERANCE = 1.03
LINE_HEIGHT = 1.2

# Fonts that Office documents name, and the free font with the same metrics (or
# the nearest) that the office profile installs.
FONT_TWINS = {
    "calibri": "Carlito",
    "cambria": "Caladea",
    "arial": "Liberation Sans",
    "helvetica": "Liberation Sans",
    "times new roman": "Liberation Serif",
    "courier new": "Liberation Mono",
}
FALLBACK_FAMILIES = ("Liberation Sans", "DejaVu Sans")


class DeckProblems(ValueError):
    """The deck has problems. ``problems`` lists them, one string each."""

    def __init__(self, problems):
        self.problems = list(problems)
        super().__init__(
            "the deck has %d problem(s):\n  - %s" % (len(self.problems), "\n  - ".join(self.problems)))


def inch(value):
    """Inches to EMU as an int. Give every position and size through it."""
    return int(round(float(value) * EMU_PER_INCH))


def _file_for(family, bold, italic):
    """Path of an installed font file for the family, or None."""
    style = ("Bold " if bold else "") + ("Italic" if italic else "")
    pattern = "%s:style=%s" % (family, style.strip() or "Regular")
    try:
        out = subprocess.run(["fc-match", "-f", "%{family}\n%{file}", pattern],
                             capture_output=True, text=True, timeout=10, check=False)
    except (OSError, subprocess.SubprocessError):
        return None
    got = out.stdout.split("\n", 1)
    if out.returncode != 0 or len(got) != 2:
        return None
    # fc-match always answers; accept only the family asked for or its twin.
    wanted = {family.lower(), FONT_TWINS.get(family.lower(), "").lower()}
    if not any(name.strip().lower() in wanted for name in got[0].split(",")):
        return None
    return got[1].strip() or None


_font_cache = {}


def _font(family, size_pt, bold, italic):
    """A Pillow font. One unit of its size is one point (scaled by 4 for accuracy)."""
    from PIL import ImageFont

    key = (family, bold, italic)
    if key not in _font_cache:
        path = None
        names = [family] if family else []
        if family and family.lower() in FONT_TWINS:
            names.insert(0, FONT_TWINS[family.lower()])
        for name in names + list(FALLBACK_FAMILIES):
            path = _file_for(name, bold, italic)
            if path:
                break
        _font_cache[key] = path
    path = _font_cache[key]
    pixels = max(1, int(round(size_pt * 4)))
    if path:
        try:
            return ImageFont.truetype(path, pixels)
        except OSError:
            pass
    return ImageFont.load_default(size=pixels)


def _width_pt(font, text):
    return font.getlength(text) / 4.0


def _runs(paragraph):
    """(text, size_pt, family, bold, italic) of each run of a paragraph."""
    out = []
    base = paragraph.font
    for run in paragraph.runs:
        f = run.font
        size = f.size or base.size
        out.append((
            run.text,
            size.pt if size else DEFAULT_FONT_PT,
            f.name or base.name,
            bool(f.bold if f.bold is not None else base.bold),
            bool(f.italic if f.italic is not None else base.italic),
        ))
    return out


def _paragraph_height_pt(paragraph, width_pt, wrap):
    """Estimated height of a paragraph in points, with the lines it needs."""
    runs = _runs(paragraph)
    if not runs or not any(r[0].strip() for r in runs):
        # An empty paragraph still takes a line of the size in force.
        size = runs[0][1] if runs else (paragraph.font.size.pt if paragraph.font.size else DEFAULT_FONT_PT)
        return size * LINE_HEIGHT, 1, size
    # Words with their own width, in order, each tagged with its run's font.
    words = []
    for text, size, family, bold, italic in runs:
        font = _font(family, size, bold, italic)
        for piece in re.findall(r"\s+|\S+", text.replace("\v", " ").replace("\n", " ")):
            words.append((piece, _width_pt(font, piece), size))
    biggest = max(w[2] for w in words)
    lines, used = 1, 0.0
    for piece, w, _ in words:
        if piece.isspace():
            used += w
            continue
        if wrap and used > 0 and used + w > width_pt:
            lines += 1
            used = w
        else:
            used += w
    spacing = paragraph.line_spacing
    if spacing is None:
        line = biggest * LINE_HEIGHT
    elif isinstance(spacing, float):
        line = biggest * LINE_HEIGHT * spacing
    else:
        line = spacing.pt
    height = line * lines
    if paragraph.space_before is not None:
        height += paragraph.space_before.pt
    if paragraph.space_after is not None:
        height += paragraph.space_after.pt
    return height, lines, biggest


def _label(index, shape):
    return "slide %d, shape %r" % (index, shape.name)


def _inches(emu):
    return emu / EMU_PER_INCH


def _is_line(shape):
    from pptx.enum.shapes import MSO_SHAPE_TYPE

    try:
        if shape.shape_type == MSO_SHAPE_TYPE.LINE:
            return True
    except NotImplementedError:
        pass
    return shape.element.tag.endswith("}cxnSp")


def find_problems(path, min_size_in=DEFAULT_MIN_SIZE_IN):
    """The list of problems of the deck at ``path``. Empty when it is clean."""
    from pptx import Presentation

    prs = Presentation(str(path))
    slide_w, slide_h = prs.slide_width, prs.slide_height
    problems = []
    if not slide_w or not slide_h:
        return ["the deck has no slide size"]
    min_emu = inch(min_size_in)
    tol = inch(OFF_SLIDE_TOLERANCE_IN)
    for index, slide in enumerate(prs.slides, start=1):
        for shape in slide.shapes:
            left, top, width, height = shape.left, shape.top, shape.width, shape.height
            if None in (left, top, width, height):
                continue  # inherits its place from the layout
            label = _label(index, shape)
            if not _is_line(shape) and (width < min_emu or height < min_emu):
                problems.append(
                    "%s: %.3f x %.3f in is smaller than %.2f in. A bare number given as inches is read as EMU "
                    "(914400 per inch): pass inch(value) for every position and size."
                    % (label, _inches(width), _inches(height), min_size_in))
                continue
            if left < -tol or top < -tol or left + width > slide_w + tol or top + height > slide_h + tol:
                problems.append(
                    "%s: spans %.2f..%.2f in across and %.2f..%.2f in down, outside the %.2f x %.2f in slide."
                    % (label, _inches(left), _inches(left + width), _inches(top), _inches(top + height),
                       _inches(slide_w), _inches(slide_h)))
            if shape.has_text_frame and shape.text_frame.text.strip():
                problems.extend(_text_problems(label, shape))
    return problems


def _text_problems(label, shape):
    frame = shape.text_frame
    inner_w = shape.width - (frame.margin_left or 0) - (frame.margin_right or 0)
    inner_h = shape.height - (frame.margin_top or 0) - (frame.margin_bottom or 0)
    if inner_w <= 0 or inner_h <= 0:
        return ["%s: the margins leave no room for the text." % label]
    width_pt = inner_w / EMU_PER_PT
    wrap = frame.word_wrap is not False
    total = 0.0
    widest_unwrapped = 0.0
    for paragraph in frame.paragraphs:
        height, _, _ = _paragraph_height_pt(paragraph, width_pt, wrap)
        total += height
        if not wrap:
            for text, size, family, bold, italic in _runs(paragraph):
                widest_unwrapped = max(widest_unwrapped, _width_pt(_font(family, size, bold, italic), text))
    out = []
    if not wrap and widest_unwrapped > width_pt * FIT_TOLERANCE:
        out.append("%s: a line of text is %.2f in wide in a %.2f in box and the box does not wrap."
                   % (label, widest_unwrapped * EMU_PER_PT / EMU_PER_INCH, inner_w / EMU_PER_INCH))
    if total > inner_h / EMU_PER_PT * FIT_TOLERANCE:
        out.append("%s: the text needs about %.2f in of height and the box has %.2f in. Shorten the text, "
                   "make the font smaller or make the box taller."
                   % (label, total * EMU_PER_PT / EMU_PER_INCH, inner_h / EMU_PER_INCH))
    return out


def check(path, min_size_in=DEFAULT_MIN_SIZE_IN):
    """Raise DeckProblems when the deck at ``path`` has a problem."""
    problems = find_problems(path, min_size_in)
    if problems:
        raise DeckProblems(problems)


def main(argv=None):
    parser = argparse.ArgumentParser(description="Check a .pptx for collapsed, off-slide and overflowing shapes.")
    parser.add_argument("deck")
    parser.add_argument("--min-size", type=float, default=DEFAULT_MIN_SIZE_IN,
                        help="smallest width or height of a shape, in inches (default %(default)s)")
    args = parser.parse_args(argv)
    try:
        problems = find_problems(args.deck, args.min_size)
    except Exception as exc:  # an unreadable file is a usage problem, not a deck problem
        print("pptx_guard: cannot read %s: %s" % (args.deck, exc), file=sys.stderr)
        return 2
    if problems:
        print("pptx_guard: %d problem(s) in %s" % (len(problems), args.deck))
        for line in problems:
            print("  - " + line)
        return 1
    print("pptx_guard: no problems found in %s (this does not prove the deck looks good; render it and look)" % args.deck)
    return 0


if __name__ == "__main__":
    sys.exit(main())
