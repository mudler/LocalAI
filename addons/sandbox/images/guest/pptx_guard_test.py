#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Tests of document-skills/pptx/pptx_guard.py. Needs pptx and Pillow only.

    python3 addons/sandbox/images/guest/pptx_guard_test.py
"""

import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
HELPER_DIR = os.path.join(HERE, "document-skills", "pptx")
sys.path.insert(0, HELPER_DIR)

from pptx import Presentation  # noqa: E402
from pptx.util import Pt  # noqa: E402

import pptx_guard  # noqa: E402
from pptx_guard import inch  # noqa: E402

LOREM = "Quarterly revenue grew in every region and the margin improved for the third quarter in a row. "


def new_deck():
    prs = Presentation()  # 10 x 7.5 in
    return prs, prs.slides.add_slide(prs.slide_layouts[6])


def textbox(slide, x, y, w, h, text, size=18, wrap=True):
    box = slide.shapes.add_textbox(x, y, w, h)
    box.text_frame.word_wrap = wrap
    box.text_frame.text = text
    for run in box.text_frame.paragraphs[0].runs:
        run.font.size = Pt(size)
    return box


class GuardTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory(prefix="pptx-guard-")
        self.addCleanup(self.dir.cleanup)

    def save(self, prs, name="deck.pptx"):
        path = os.path.join(self.dir.name, name)
        prs.save(path)
        return path

    def test_inch_is_emu(self):
        self.assertEqual(inch(1), 914400)
        self.assertEqual(inch(0.5), 457200)
        self.assertIsInstance(inch(2.25), int)

    def test_good_deck_passes(self):
        prs, slide = new_deck()
        textbox(slide, inch(0.5), inch(0.5), inch(9), inch(1), "Results", size=32)
        textbox(slide, inch(0.5), inch(1.8), inch(9), inch(2), LOREM, size=18)
        slide.shapes.add_shape(1, inch(0.5), inch(5), inch(3), inch(1.5))
        path = self.save(prs)
        self.assertEqual(pptx_guard.find_problems(path), [])
        pptx_guard.check(path)

    def test_collapsed_deck_fails_with_a_clear_message(self):
        # The mistake of the unreadable deck: inch values as bare floats, read as EMU.
        prs, slide = new_deck()
        for i in range(3):
            box = slide.shapes.add_textbox(0.5 + i, 1.0, 4.0, 1.0)
            box.text_frame.text = "Quarter %d" % i
        slide.shapes.add_shape(1, 0.5, 3.0, 8.0, 2.0)
        path = self.save(prs, "collapsed.pptx")
        problems = pptx_guard.find_problems(path)
        self.assertEqual(len(problems), 4, problems)
        for line in problems:
            self.assertIn("smaller than 0.20 in", line)
            self.assertIn("inch(", line)
        with self.assertRaises(pptx_guard.DeckProblems) as ctx:
            pptx_guard.check(path)
        self.assertIn("4 problem(s)", str(ctx.exception))

    def test_cli_exit_codes(self):
        prs, slide = new_deck()
        slide.shapes.add_shape(1, 0.5, 3.0, 8.0, 2.0)
        bad = self.save(prs, "bad.pptx")
        prs, slide = new_deck()
        textbox(slide, inch(1), inch(1), inch(6), inch(1), "Fine", size=24)
        good = self.save(prs, "good.pptx")
        run = lambda *a: subprocess.run([sys.executable, os.path.join(HELPER_DIR, "pptx_guard.py"), *a],
                                        capture_output=True, text=True)
        r = run(good)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        r = run(bad)
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("smaller than 0.20 in", r.stdout)
        r = run(os.path.join(self.dir.name, "missing.pptx"))
        self.assertEqual(r.returncode, 2)

    def test_min_size_is_a_setting(self):
        prs, slide = new_deck()
        slide.shapes.add_shape(1, inch(1), inch(1), inch(0.1), inch(3))  # a thin bar
        path = self.save(prs)
        self.assertEqual(len(pptx_guard.find_problems(path)), 1)
        self.assertEqual(pptx_guard.find_problems(path, min_size_in=0.05), [])

    def test_a_line_may_have_no_width(self):
        prs, slide = new_deck()
        slide.shapes.add_connector(1, inch(1), inch(1), inch(1), inch(4))  # vertical
        self.assertEqual(pptx_guard.find_problems(self.save(prs)), [])

    def test_shape_outside_the_slide(self):
        prs, slide = new_deck()
        slide.shapes.add_shape(1, inch(8), inch(1), inch(3), inch(1))  # ends at 11 in
        slide.shapes.add_shape(1, inch(1), inch(-1), inch(2), inch(2))
        problems = pptx_guard.find_problems(self.save(prs))
        self.assertEqual(len(problems), 2, problems)
        self.assertTrue(all("outside" in p for p in problems))

    def test_text_that_does_not_fit(self):
        prs, slide = new_deck()
        textbox(slide, inch(1), inch(1), inch(3), inch(0.6), LOREM * 3, size=20)
        problems = pptx_guard.find_problems(self.save(prs))
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("text needs about", problems[0])

    def test_text_that_fits_in_a_wide_box_but_not_a_narrow_one(self):
        prs, slide = new_deck()
        textbox(slide, inch(0.5), inch(1), inch(9), inch(1.2), LOREM, size=18)
        self.assertEqual(pptx_guard.find_problems(self.save(prs, "wide.pptx")), [])
        prs, slide = new_deck()
        textbox(slide, inch(0.5), inch(1), inch(2), inch(1.2), LOREM, size=18)
        self.assertEqual(len(pptx_guard.find_problems(self.save(prs, "narrow.pptx"))), 1)

    def test_a_line_wider_than_a_box_that_does_not_wrap(self):
        prs, slide = new_deck()
        textbox(slide, inch(1), inch(1), inch(2), inch(1), "A heading that is much too long for the box", size=24, wrap=False)
        problems = pptx_guard.find_problems(self.save(prs))
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("does not wrap", problems[0])

    def test_unknown_font_falls_back(self):
        prs, slide = new_deck()
        box = textbox(slide, inch(1), inch(1), inch(6), inch(1), "Fallback font", size=18)
        box.text_frame.paragraphs[0].runs[0].font.name = "No Such Font 123"
        self.assertEqual(pptx_guard.find_problems(self.save(prs)), [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
