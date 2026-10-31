#!/usr/bin/env python3
"""Deterministic PDF package and geometry checker.

The verifier reads PDF geometry through pdfplumber and pypdf. It is not a renderer, so it cannot
prove that a PDF is visually polished. By default it catches only high-confidence defects in the
final rendered artifact: unreadable PDFs, invalid page boxes, extracted objects outside the page,
and missing text layer warnings. Optional strict-layout heuristics report spacing and overlap
conditions for known authored fixed-page PDFs. Optional page rendering writes PNG files.
"""

from __future__ import annotations

import argparse
import json
import math
import statistics
import sys
from dataclasses import dataclass, field
from itertools import combinations
from pathlib import Path
from typing import Any

POINTS_PER_INCH = 72.0
TOL = 0.08
MIN_OUTER_MARGIN = 0.25
PREFERRED_WORKING_MARGIN = 0.45
MIN_ADJACENT_GAP = 0.10
MIN_INTERSECTION_AREA = 0.004
MAX_BODY_SIZE_BUCKETS = 5


@dataclass
class Element:
    name: str
    kind: str
    left: float
    top: float
    width: float
    height: float
    text: str = ""
    font_sizes: list[float] = field(default_factory=list)
    font_names: list[str] = field(default_factory=list)

    @property
    def right(self) -> float:
        return round(self.left + self.width, 3)

    @property
    def bottom(self) -> float:
        return round(self.top + self.height, 3)

    @property
    def area(self) -> float:
        return max(0.0, self.width * self.height)

    @property
    def size_pt(self) -> float | None:
        return median(self.font_sizes)

    def as_dict(self) -> dict[str, float]:
        return {
            "left": self.left,
            "top": self.top,
            "width": self.width,
            "height": self.height,
        }


@dataclass
class PageInfo:
    index: int
    width: float
    height: float
    elements: list[Element] = field(default_factory=list)
    lines: list[Element] = field(default_factory=list)
    notes: list[str] = field(default_factory=list)


@dataclass
class PdfContext:
    pages: list[PageInfo]
    metadata: dict[str, Any]


def median(values: list[float]) -> float | None:
    clean = [v for v in values if isinstance(v, int | float) and math.isfinite(v)]
    if not clean:
        return None
    return float(statistics.median(clean))


def pt_to_in(value: float) -> float:
    return round(float(value) / POINTS_PER_INCH, 3)


def bbox_to_element(
    name: str, kind: str, bbox: tuple[float, float, float, float], text: str = ""
) -> Element:
    x0, top, x1, bottom = bbox
    return Element(
        name=name,
        kind=kind,
        left=pt_to_in(x0),
        top=pt_to_in(top),
        width=pt_to_in(x1 - x0),
        height=pt_to_in(bottom - top),
        text=text,
    )


def problem(
    kind: str,
    page: int,
    message: str,
    *,
    severity: str = "error",
    elements: list[Element] | None = None,
) -> dict[str, Any]:
    elements = elements or []
    return {
        "severity": severity,
        "kind": kind,
        "page": page,
        "message": message,
        "elements": [element.name for element in elements],
        "boxes": [element.as_dict() for element in elements],
    }


def intersection_area(a: Element, b: Element) -> float:
    width = max(0.0, min(a.right, b.right) - max(a.left, b.left))
    height = max(0.0, min(a.bottom, b.bottom) - max(a.top, b.top))
    return width * height


def x_overlap(a: Element, b: Element) -> float:
    return max(0.0, min(a.right, b.right) - max(a.left, b.left))


def y_overlap(a: Element, b: Element) -> float:
    return max(0.0, min(a.bottom, b.bottom) - max(a.top, b.top))


def contains(parent: Element, child: Element, pad: float = TOL) -> bool:
    return (
        child.left >= parent.left - pad
        and child.top >= parent.top - pad
        and child.right <= parent.right + pad
        and child.bottom <= parent.bottom + pad
    )


def cluster(values: list[float], tol: float = TOL) -> list[list[float]]:
    if not values:
        return []
    groups: list[list[float]] = []
    for value in sorted(values):
        if not groups or abs(value - (sum(groups[-1]) / len(groups[-1]))) > tol:
            groups.append([value])
        else:
            groups[-1].append(value)
    return groups


def extract_words(page: Any) -> list[dict[str, Any]]:
    try:
        return page.extract_words(extra_attrs=["fontname", "size"], keep_blank_chars=False) or []
    except TypeError:
        return page.extract_words(keep_blank_chars=False) or []


def line_segments_from_words(words: list[dict[str, Any]]) -> list[Element]:
    rows: list[list[dict[str, Any]]] = []
    for word in sorted(words, key=lambda item: (float(item["top"]), float(item["x0"]))):
        center = (float(word["top"]) + float(word["bottom"])) / 2
        target = None
        for row in rows[-4:]:
            row_center = statistics.mean(
                (float(item["top"]) + float(item["bottom"])) / 2 for item in row
            )
            if abs(center - row_center) <= 3.0:
                target = row
                break
        if target is None:
            target = []
            rows.append(target)
        target.append(word)

    elements: list[Element] = []
    for row_index, row in enumerate(rows, start=1):
        segment: list[dict[str, Any]] = []
        last_x1: float | None = None
        segment_index = 1

        def flush() -> None:
            nonlocal segment, segment_index
            if not segment:
                return
            x0 = min(float(item["x0"]) for item in segment)
            x1 = max(float(item["x1"]) for item in segment)
            top = min(float(item["top"]) for item in segment)
            bottom = max(float(item["bottom"]) for item in segment)
            text = " ".join(str(item.get("text", "")).strip() for item in segment).strip()
            element = bbox_to_element(
                f"textline_{row_index:02d}_{segment_index:02d}",
                "textline",
                (x0, top, x1, bottom),
                text,
            )
            element.font_sizes = [
                float(item["size"]) for item in segment if isinstance(item.get("size"), int | float)
            ]
            element.font_names = [str(item["fontname"]) for item in segment if item.get("fontname")]
            elements.append(element)
            segment = []
            segment_index += 1

        for word in sorted(row, key=lambda item: float(item["x0"])):
            gap = float(word["x0"]) - last_x1 if last_x1 is not None else 0.0
            if segment and gap > 28.0:
                flush()
            segment.append(word)
            last_x1 = float(word["x1"])
        flush()
    return elements


def merge_text_blocks(lines: list[Element]) -> list[Element]:
    blocks: list[Element] = []
    for line in sorted(lines, key=lambda item: (item.top, item.left)):
        target = None
        for block in reversed(blocks):
            vertical_gap = line.top - block.bottom
            same_column = (
                abs(line.left - block.left) <= 0.18
                or x_overlap(line, block) >= min(line.width, block.width) * 0.55
            )
            if 0 <= vertical_gap <= 0.16 and same_column:
                target = block
                break
        if target is None:
            block = Element(
                name=f"textblock_{len(blocks) + 1:02d}",
                kind="text",
                left=line.left,
                top=line.top,
                width=line.width,
                height=line.height,
                text=line.text,
                font_sizes=list(line.font_sizes),
                font_names=list(line.font_names),
            )
            blocks.append(block)
            continue
        target.text = "\n".join(part for part in [target.text, line.text] if part)
        right = max(target.right, line.right)
        bottom = max(target.bottom, line.bottom)
        target.left = min(target.left, line.left)
        target.top = min(target.top, line.top)
        target.width = round(right - target.left, 3)
        target.height = round(bottom - target.top, 3)
        target.font_sizes.extend(line.font_sizes)
        target.font_names.extend(line.font_names)
    return blocks


def classify_text_blocks(page: PageInfo) -> None:
    if not page.elements:
        return
    text_blocks = [element for element in page.elements if element.kind == "text"]
    sizes = [element.size_pt for element in text_blocks if element.size_pt]
    max_size = max(sizes) if sizes else None
    title_candidates = [
        element
        for element in text_blocks
        if element.top < 1.15 and (max_size is None or (element.size_pt or 0) >= max_size - 2)
    ]
    title = min(title_candidates, key=lambda item: (item.top, item.left), default=None)
    for element in text_blocks:
        if element is title:
            element.kind = "title"
        elif element.top > page.height - 0.65:
            element.kind = "footer"
        elif element.text.lower().startswith(("source:", "sources:", "note:", "notes:")):
            element.kind = "footer"


def collect_page(pdf_page: Any, index: int) -> PageInfo:
    page = PageInfo(index=index, width=pt_to_in(pdf_page.width), height=pt_to_in(pdf_page.height))
    words = extract_words(pdf_page)
    page.lines = line_segments_from_words(words)
    page.elements.extend(merge_text_blocks(page.lines))

    for image_index, image in enumerate(getattr(pdf_page, "images", []) or [], start=1):
        try:
            page.elements.append(
                bbox_to_element(
                    f"image_{image_index:02d}",
                    "image",
                    (
                        float(image["x0"]),
                        float(image["top"]),
                        float(image["x1"]),
                        float(image["bottom"]),
                    ),
                )
            )
        except (KeyError, TypeError, ValueError):
            continue

    for rect_index, rect in enumerate(getattr(pdf_page, "rects", []) or [], start=1):
        try:
            width = float(rect["x1"]) - float(rect["x0"])
            height = float(rect["bottom"]) - float(rect["top"])
        except (KeyError, TypeError, ValueError):
            continue
        if width < 18 or height < 12:
            continue
        kind = (
            "background" if width * height > (pdf_page.width * pdf_page.height * 0.70) else "panel"
        )
        page.elements.append(
            bbox_to_element(
                f"{kind}_{rect_index:02d}",
                kind,
                (float(rect["x0"]), float(rect["top"]), float(rect["x1"]), float(rect["bottom"])),
            )
        )

    classify_text_blocks(page)
    return page


def build_context(path: str | Path) -> PdfContext:
    import pdfplumber
    from pypdf import PdfReader

    reader = PdfReader(str(path))
    metadata = {
        "page_count": len(reader.pages),
        "is_encrypted": bool(reader.is_encrypted),
        "metadata": {str(k): str(v) for k, v in (reader.metadata or {}).items()},
    }
    if reader.is_encrypted:
        return PdfContext(pages=[], metadata=metadata)
    with pdfplumber.open(str(path)) as pdf:
        pages = [collect_page(page, i) for i, page in enumerate(pdf.pages, start=1)]
    return PdfContext(pages=pages, metadata=metadata)


def primary_elements(page: PageInfo) -> list[Element]:
    return [
        element
        for element in page.elements
        if element.kind in {"title", "text", "image", "panel"}
        and element.width >= 0.08
        and element.height >= 0.06
    ]


def content_elements(page: PageInfo) -> list[Element]:
    return [
        element
        for element in primary_elements(page)
        if element.kind not in {"title", "footer", "background"} and element.top < page.height - 0.7
    ]


def check_pdf_validity(ctx: PdfContext) -> list[dict[str, Any]]:
    if ctx.metadata.get("is_encrypted"):
        return [
            {
                "severity": "error",
                "kind": "pdf-encrypted",
                "page": 0,
                "message": "PDF is encrypted and cannot be inspected.",
                "elements": [],
                "boxes": [],
            }
        ]
    if not ctx.pages:
        return [
            {
                "severity": "error",
                "kind": "pdf-empty",
                "page": 0,
                "message": "PDF has no inspectable pages.",
                "elements": [],
                "boxes": [],
            }
        ]
    return []


def page_size_summary(ctx: PdfContext) -> dict[str, Any]:
    sizes: dict[str, int] = {}
    for page in ctx.pages:
        key = f"{page.width:g}x{page.height:g}"
        sizes[key] = sizes.get(key, 0) + 1
    ratios = [page.width / page.height for page in ctx.pages if page.width > 0 and page.height > 0]
    median_ratio = median(ratios)
    return {
        "sizes_in": sizes,
        "median_aspect_ratio": round(median_ratio, 3) if median_ratio else None,
        "landscape_page_count": sum(1 for page in ctx.pages if page.width >= page.height),
        "portrait_page_count": sum(1 for page in ctx.pages if page.height > page.width),
    }


def check_page_size(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    if not ctx.pages:
        return out

    size_shapes = {(round(page.width, 1), round(page.height, 1)) for page in ctx.pages}
    if len(size_shapes) > 3:
        out.append(
            problem(
                "page-size-mixed",
                1,
                f"PDF uses {len(size_shapes)} distinct page sizes",
                severity="warning",
            )
        )

    for page in ctx.pages:
        if page.width <= 0 or page.height <= 0:
            out.append(
                problem(
                    "page-size-invalid",
                    page.index,
                    f"page has invalid dimensions ({page.width} x {page.height} in)",
                )
            )
            continue

        if min(page.width, page.height) < 3.0 or max(page.width, page.height) > 30.0:
            out.append(
                problem(
                    "page-size-unusual",
                    page.index,
                    f"page has unusual dimensions ({page.width} x {page.height} in)",
                    severity="warning",
                )
            )
    return out


def check_text_layer_presence(ctx: PdfContext) -> list[dict[str, Any]]:
    if not ctx.pages:
        return []
    line_count = sum(len(page.lines) for page in ctx.pages)
    if line_count > 0:
        return []
    return [
        problem(
            "pdf-no-extractable-text",
            1,
            "PDF has no extractable text layer.",
            severity="warning",
        )
    ]


def check_boundaries(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for page in ctx.pages:
        for element in primary_elements(page):
            if (
                element.left < -TOL
                or element.top < -TOL
                or element.right > page.width + TOL
                or element.bottom > page.height + TOL
            ):
                out.append(
                    problem(
                        "layout-boundary-violation",
                        page.index,
                        f"{element.name!r} extends outside the page boundary",
                        elements=[element],
                    )
                )
    return out


def check_outer_margin(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for page in ctx.pages:
        for element in primary_elements(page):
            if element.kind in {"background", "footer"}:
                continue
            hard = (
                element.left < MIN_OUTER_MARGIN
                or element.top < MIN_OUTER_MARGIN
                or page.width - element.right < MIN_OUTER_MARGIN
                or page.height - element.bottom < MIN_OUTER_MARGIN
            )
            if hard:
                out.append(
                    problem(
                        "layout-outer-margin",
                        page.index,
                        f"{element.name!r} intrudes into the {MIN_OUTER_MARGIN} in hard margin",
                        elements=[element],
                    )
                )
                continue
            working = (
                element.left < PREFERRED_WORKING_MARGIN
                or page.width - element.right < PREFERRED_WORKING_MARGIN
            )
            if working and element.kind != "title":
                out.append(
                    problem(
                        "layout-working-margin",
                        page.index,
                        f"{element.name!r} is inside the preferred {PREFERRED_WORKING_MARGIN} in working margin",
                        severity="warning",
                        elements=[element],
                    )
                )
    return out


def is_allowed_overlap(a: Element, b: Element) -> bool:
    if a.kind == "background" or b.kind == "background":
        return True
    if a.kind == "panel" and contains(a, b):
        return True
    if b.kind == "panel" and contains(b, a):
        return True
    return False


def check_no_overlap(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for page in ctx.pages:
        elements = primary_elements(page)
        for a, b in combinations(elements, 2):
            area = intersection_area(a, b)
            if area <= MIN_INTERSECTION_AREA or is_allowed_overlap(a, b):
                continue
            out.append(
                problem(
                    "layout-no-overlap",
                    page.index,
                    f"{a.name!r} and {b.name!r} overlap by {round(area, 3)} in^2",
                    elements=[a, b],
                )
            )
    return out


def check_near_miss_spacing(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for page in ctx.pages:
        elements = [element for element in content_elements(page) if element.kind != "panel"]
        for a, b in combinations(elements, 2):
            if intersection_area(a, b) > 0:
                continue
            horiz_gap = max(a.left, b.left) - min(a.right, b.right)
            vert_gap = max(a.top, b.top) - min(a.bottom, b.bottom)
            if (
                y_overlap(a, b) > min(a.height, b.height) * 0.25
                and 0 <= horiz_gap < MIN_ADJACENT_GAP
            ):
                out.append(
                    problem(
                        "layout-minimum-horizontal-gap",
                        page.index,
                        f"{a.name!r} and {b.name!r} are only {round(horiz_gap, 3)} in apart horizontally",
                        severity="warning",
                        elements=[a, b],
                    )
                )
            if x_overlap(a, b) > min(a.width, b.width) * 0.25 and 0 <= vert_gap < MIN_ADJACENT_GAP:
                out.append(
                    problem(
                        "layout-minimum-vertical-gap",
                        page.index,
                        f"{a.name!r} and {b.name!r} are only {round(vert_gap, 3)} in apart vertically",
                        severity="warning",
                        elements=[a, b],
                    )
                )
    return out


def row_groups(elements: list[Element]) -> list[list[Element]]:
    groups: list[list[Element]] = []
    for element in sorted(elements, key=lambda item: (item.top, item.left)):
        target = None
        for group in groups:
            group_top = statistics.mean(item.top for item in group)
            if abs(element.top - group_top) <= 0.18:
                target = group
                break
        if target is None:
            target = []
            groups.append(target)
        target.append(element)
    return [group for group in groups if len(group) >= 2]


def column_groups(elements: list[Element]) -> list[list[Element]]:
    groups: list[list[Element]] = []
    for element in sorted(elements, key=lambda item: (item.left, item.top)):
        target = None
        for group in groups:
            group_left = statistics.mean(item.left for item in group)
            if abs(element.left - group_left) <= 0.18:
                target = group
                break
        if target is None:
            target = []
            groups.append(target)
        target.append(element)
    return [group for group in groups if len(group) >= 2]


def check_left_margin_consistency(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    page_lefts: dict[int, float] = {}
    for page in ctx.pages:
        lefts = [element.left for element in content_elements(page) if element.kind != "panel"]
        groups = cluster(lefts)
        if len(groups) > 3:
            out.append(
                problem(
                    "layout-left-edge-discipline",
                    page.index,
                    f"page uses {len(groups)} distinct content left edges",
                    severity="warning",
                )
            )
        if groups:
            dominant = max(groups, key=len)
            page_lefts[page.index] = sum(dominant) / len(dominant)

    if len(page_lefts) >= 3:
        page_groups = cluster(list(page_lefts.values()))
        dominant = max(page_groups, key=len)
        dominant_left = sum(dominant) / len(dominant)
        deviating = [idx for idx, value in page_lefts.items() if abs(value - dominant_left) > TOL]
        if len(deviating) >= 2:
            out.append(
                problem(
                    "layout-page-left-edge-rhythm",
                    min(deviating),
                    f"{len(deviating)} pages deviate from dominant left edge {round(dominant_left, 3)} in",
                    severity="warning",
                )
            )
    return out


def check_grid_alignment(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for page in ctx.pages:
        elements = [element for element in content_elements(page) if element.height >= 0.18]
        for row in row_groups(elements):
            ordered = sorted(row, key=lambda item: item.left)
            tops = [element.top for element in ordered]
            if max(tops) - min(tops) > TOL:
                out.append(
                    problem(
                        "layout-row-top-alignment",
                        page.index,
                        f"row peers are not top-aligned ({round(min(tops), 3)}..{round(max(tops), 3)} in)",
                        severity="warning",
                        elements=ordered,
                    )
                )
            widths = [element.width for element in ordered if element.kind in {"image", "panel"}]
            if len(widths) >= 2 and max(widths) - min(widths) > 0.20:
                out.append(
                    problem(
                        "layout-row-peer-widths",
                        page.index,
                        f"row peers have unequal widths ({round(min(widths), 3)}..{round(max(widths), 3)} in)",
                        severity="warning",
                        elements=ordered,
                    )
                )
        for col in column_groups(elements):
            ordered = sorted(col, key=lambda item: item.top)
            lefts = [element.left for element in ordered]
            if max(lefts) - min(lefts) > TOL:
                out.append(
                    problem(
                        "layout-column-rail-alignment",
                        page.index,
                        "stacked column peers do not share the same left rail",
                        severity="warning",
                        elements=ordered,
                    )
                )
    return out


def check_even_gutters(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for page in ctx.pages:
        elements = [element for element in content_elements(page) if element.height >= 0.18]
        for row in row_groups(elements):
            ordered = sorted(row, key=lambda item: item.left)
            if len(ordered) < 3:
                continue
            gaps = [ordered[i + 1].left - ordered[i].right for i in range(len(ordered) - 1)]
            gaps = [gap for gap in gaps if gap >= 0]
            if gaps and max(gaps) - min(gaps) > TOL:
                out.append(
                    problem(
                        "layout-even-horizontal-gutters",
                        page.index,
                        f"horizontal gutters vary ({round(min(gaps), 3)}..{round(max(gaps), 3)} in)",
                        severity="warning",
                        elements=ordered,
                    )
                )
        for col in column_groups(elements):
            ordered = sorted(col, key=lambda item: item.top)
            if len(ordered) < 3:
                continue
            gaps = [ordered[i + 1].top - ordered[i].bottom for i in range(len(ordered) - 1)]
            gaps = [gap for gap in gaps if gap >= 0]
            if gaps and max(gaps) - min(gaps) > TOL:
                out.append(
                    problem(
                        "layout-even-vertical-gutters",
                        page.index,
                        f"vertical gutters vary ({round(min(gaps), 3)}..{round(max(gaps), 3)} in)",
                        severity="warning",
                        elements=ordered,
                    )
                )
    return out


def check_typography_hierarchy(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    body_sizes: list[float] = []
    for page in ctx.pages:
        titles = [element for element in page.elements if element.kind == "title"]
        body = [element for element in page.elements if element.kind == "text"]
        page_body_sizes = [element.size_pt for element in body if element.size_pt]
        body_sizes.extend(size for size in page_body_sizes if size is not None)
        if titles and page_body_sizes:
            title_size = max((element.size_pt or 0) for element in titles)
            body_max = max(page_body_sizes)
            if title_size < 18:
                out.append(
                    problem(
                        "typography-title-too-small",
                        page.index,
                        f"title size is only {round(title_size, 1)} pt",
                        severity="warning",
                        elements=titles,
                    )
                )
            if title_size - body_max < 4:
                out.append(
                    problem(
                        "typography-weak-hierarchy",
                        page.index,
                        f"title/body hierarchy gap is only {round(title_size - body_max, 1)} pt",
                        severity="warning",
                        elements=titles,
                    )
                )
        for element in body:
            if element.size_pt and element.size_pt < 7:
                out.append(
                    problem(
                        "typography-body-too-small",
                        page.index,
                        f"{element.name!r} body text is {round(element.size_pt, 1)} pt",
                        severity="warning",
                        elements=[element],
                    )
                )
    buckets = {round(size * 2) / 2 for size in body_sizes}
    if len(buckets) > MAX_BODY_SIZE_BUCKETS:
        out.append(
            problem(
                "typography-too-many-body-sizes",
                1,
                f"PDF uses {len(buckets)} body text size buckets",
                severity="warning",
            )
        )
    return out


def check_whitespace_balance(ctx: PdfContext) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for page in ctx.pages:
        elements = [element for element in content_elements(page) if element.kind != "panel"]
        if not elements:
            out.append(
                problem(
                    "layout-empty-content-region",
                    page.index,
                    "page has no detected body content below the title",
                    severity="warning",
                )
            )
            continue
        left = min(element.left for element in elements)
        right = max(element.right for element in elements)
        side_delta = abs(left - (page.width - right))
        if side_delta > 0.75:
            out.append(
                problem(
                    "layout-whitespace-side-imbalance",
                    page.index,
                    f"left/right whitespace differs by {round(side_delta, 2)} in",
                    severity="warning",
                    elements=elements[:6],
                )
            )
        content_area = max(0.1, (page.width - 2 * PREFERRED_WORKING_MARGIN) * (page.height - 1.6))
        occupied = sum(min(element.area, content_area) for element in elements)
        density = occupied / content_area
        if density < 0.025:
            out.append(
                problem(
                    "layout-too-sparse",
                    page.index,
                    f"detected content density is only {round(density, 3)}",
                    severity="warning",
                    elements=elements[:6],
                )
            )
        if density > 0.65:
            out.append(
                problem(
                    "layout-too-dense",
                    page.index,
                    f"detected content density is {round(density, 3)}",
                    severity="warning",
                    elements=elements[:6],
                )
            )
    return out


def render_pages(pdf_path: Path, render_dir: Path) -> dict[str, Any]:
    render_dir.mkdir(parents=True, exist_ok=True)
    try:
        from pdf2image import convert_from_path

        images = convert_from_path(str(pdf_path), dpi=144)
        paths = []
        for index, image in enumerate(images, start=1):
            out_path = render_dir / f"page_{index:02d}.png"
            image.save(out_path)
            paths.append(str(out_path))
        return {"ok": True, "paths": paths}
    except Exception as exc:  # noqa: BLE001 - rendering is optional review aid
        return {"ok": False, "error": repr(exc), "paths": []}


def verify_pdf(
    path: str | Path,
    *,
    render_dir: Path | None = None,
    strict_layout: bool = False,
) -> dict[str, Any]:
    pdf_path = Path(path)
    ctx = build_context(pdf_path)
    problems = collect_pdf_problems(ctx, strict_layout=strict_layout)

    rendered = render_pages(pdf_path, render_dir) if render_dir else None
    if rendered and not rendered.get("ok"):
        problems.append(
            {
                "severity": "warning",
                "kind": "render-pages-failed",
                "page": 0,
                "message": "Optional PNG rendering failed.",
                "elements": [],
                "boxes": [],
                "error": rendered.get("error"),
            }
        )

    errors = [p for p in problems if p.get("severity", "error") == "error"]
    warnings = [p for p in problems if p.get("severity") == "warning"]
    pages_summary = [
        {
            "index": page.index,
            "width": page.width,
            "height": page.height,
            "element_count": len(page.elements),
            "text_line_count": len(page.lines),
            "elements": [
                {
                    "name": element.name,
                    "kind": element.kind,
                    "box": element.as_dict(),
                    "text": element.text[:160],
                    "size_pt": element.size_pt,
                }
                for element in page.elements[:80]
            ],
        }
        for page in ctx.pages
    ]
    return {
        "ok": not errors,
        "page_count": len(ctx.pages),
        "error_count": len(errors),
        "warning_count": len(warnings),
        "problems": problems,
        "pages": pages_summary,
        "metadata": {
            **ctx.metadata,
            "verification": {
                "strict_layout": strict_layout,
                "page_size": page_size_summary(ctx),
            },
        },
        "rendered_pages": rendered,
    }


def collect_pdf_problems(ctx: PdfContext, *, strict_layout: bool = False) -> list[dict[str, Any]]:
    problems: list[dict[str, Any]] = []
    for check in [
        check_pdf_validity,
        check_page_size,
        check_boundaries,
        check_text_layer_presence,
    ]:
        problems.extend(check(ctx))

    if strict_layout:
        for check in [
            check_outer_margin,
            check_no_overlap,
            check_near_miss_spacing,
            check_typography_hierarchy,
        ]:
            problems.extend(check(ctx))
    return problems


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description=(
            "Verify PDF openability, page boxes, extractable text, and optional fixed-page "
            "layout heuristics."
        )
    )
    parser.add_argument("pdf", type=Path, help="Path to the PDF artifact to verify")
    parser.add_argument("--render-dir", type=Path, default=None, help="Optional PNG output dir")
    parser.add_argument(
        "--strict-layout",
        action="store_true",
        help=(
            "Run heuristic margin, overlap, and spacing checks intended for generated fixed-page "
            "PDFs with meaningful extracted geometry."
        ),
    )
    parser.add_argument("--json", action="store_true", help="Print JSON")
    args = parser.parse_args(argv)

    result = verify_pdf(args.pdf, render_dir=args.render_dir, strict_layout=args.strict_layout)
    if args.json:
        print(json.dumps(result, indent=2))
    else:
        print(
            f"ok={result['ok']} pages={result['page_count']} "
            f"errors={result['error_count']} warnings={result['warning_count']}"
        )
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
