#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Run the image workflow's real publication gate with disposable git history."""

import os
from pathlib import Path
import re
import subprocess
import tempfile
import textwrap
import unittest


WORKFLOW = Path(__file__).resolve().parents[1] / "workflows" / "image.yml"


def gate_script():
    # This deliberately supports only the workflow's block-scalar layout, not
    # arbitrary YAML. Fail closed if the job/step becomes ambiguous or moves.
    lines = WORKFLOW.read_text().splitlines()
    jobs = [i for i, line in enumerate(lines) if re.fullmatch(r" +changes:", line)]
    if len(jobs) != 1:
        raise AssertionError("expected one changes job")
    start = jobs[0]
    indent = len(lines[start]) - len(lines[start].lstrip())
    end = next((i for i in range(start + 1, len(lines))
                if lines[i].strip() and not lines[i].lstrip().startswith("#")
                and len(lines[i]) - len(lines[i].lstrip()) <= indent), len(lines))
    job = lines[start + 1:end]
    steps = [i for i, line in enumerate(job)
             if re.fullmatch(r" +- id: decide", line)]
    if len(steps) != 1:
        raise AssertionError("expected one changes -> decide step")
    start = steps[0]
    indent = len(job[start]) - len(job[start].lstrip())
    end = next((i for i in range(start + 1, len(job))
                if job[i].strip() and not job[i].lstrip().startswith("#")
                and len(job[i]) - len(job[i].lstrip()) <= indent), len(job))
    step = job[start + 1:end]
    runs = [i for i, line in enumerate(step) if re.fullmatch(r" +run: \|", line)]
    if len(runs) != 1:
        raise AssertionError("expected one literal run block in changes -> decide")
    start = runs[0]
    indent = len(step[start]) - len(step[start].lstrip())
    end = next((i for i in range(start + 1, len(step))
                if step[i].strip()
                and len(step[i]) - len(step[i].lstrip()) <= indent), len(step))
    script = textwrap.dedent("\n".join(step[start + 1:end]))
    if not script.strip():
        raise AssertionError("empty changes -> decide run block")
    return script + "\n"


class ImagePublicationGate(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.script = gate_script()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="image-publication-gate-")
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name) / "repo"
        self.repo.mkdir()
        self.git("init", "--quiet")
        self.git("config", "user.name", "Gate Test")
        self.git("config", "user.email", "gate-test@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.git("commit", "--quiet", "--allow-empty", "-m", "base")
        self.before = self.git("rev-parse", "HEAD").strip()
        self.after = self.before

    def git(self, *args):
        return subprocess.run(
            ["git", *args], cwd=self.repo, check=True, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30,
        ).stdout

    def change(self, paths):
        for name in paths:
            path = self.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("fixture\n")
        self.git("add", ".")
        self.git("commit", "--quiet", "-m", "changes")
        self.after = self.git("rev-parse", "HEAD").strip()

    def assert_gate(self, expected, before=None, ref="refs/heads/master"):
        output = Path(self.tmp.name) / "output"
        output.write_text("")
        env = dict(os.environ, BEFORE=self.before if before is None else before,
                   AFTER=self.after, GITHUB_REF=ref, GITHUB_OUTPUT=str(output),
                   LC_ALL="C")
        result = subprocess.run(
            ["bash", "-e", "-o", "pipefail", "-c", self.script],
            cwd=self.repo, env=env, text=True, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(output.read_text(), f"build={str(expected).lower()}\n",
                         result.stdout[-250:] + result.stderr)

    def test_small_code(self):
        self.change(["core/main.go"])
        self.assert_gate(True)

    def test_small_mixed(self):
        self.change(["core/main.go", "docs/guide.txt", "README.md"])
        self.assert_gate(True)

    def test_excluded_classes(self):
        for path in ["docs/guide.txt", "gallery/model.yaml", "examples/demo.py",
                     "README.md", "core/nested/notes.md"]:
            with self.subTest(path=path):
                self.before = self.after
                self.change([path])
                self.assert_gate(False)

    def large_paths(self):
        paths = [f"docs/guide-{i:05d}-{'x' * 55}.txt" for i in range(3500)]
        self.assertGreater(len(paths), 2869)
        self.assertGreater(len(("\n".join(paths) + "\n").encode()), 140000)
        return paths

    def test_large_mixed_early_match_repeated(self):
        # Sorting puts the code match first, with far more than a pipe buffer
        # behind it. Repeat to guard against scheduling-dependent SIGPIPE.
        self.change(["aaa-code.go", *self.large_paths()])
        diff = self.git("diff", "--name-only", self.before, self.after)
        self.assertTrue(diff.startswith("aaa-code.go\n"))
        self.assertGreater(len(diff.encode()), 140000)
        self.assertGreater(len(diff.splitlines()), 2869)
        for attempt in range(8):
            with self.subTest(attempt=attempt):
                self.assert_gate(True)

    def test_large_excluded_only(self):
        self.change(self.large_paths())
        self.assert_gate(False)

    def test_empty_diff(self):
        self.assert_gate(True)

    def test_missing_zero_unresolvable_base(self):
        self.change(["docs/guide.txt"])
        for before in ["", "0" * 40, "f" * 40]:
            with self.subTest(before=before):
                self.assert_gate(True, before=before)

    def test_tag_push(self):
        self.change(["docs/guide.txt"])
        self.assert_gate(True, ref="refs/tags/v-test")
        self.assert_gate(True, before="", ref="refs/tags/v-test")
        self.assert_gate(True, before=self.after, ref="refs/tags/v-test")


if __name__ == "__main__":
    unittest.main(verbosity=2)
