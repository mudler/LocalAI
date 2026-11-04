"""Build-time check of the Python libraries in the guest image.

It fails the image build when a library is missing, when docx, openpyxl or
pptx do not come from the Paper distributions, or when an upstream package
that the Paper packages replace is installed next to them.
"""
import importlib
import importlib.metadata as md
import sys

# import name -> the only distribution that may provide it
PAPER = {"docx": "paper-docx", "openpyxl": "paper-xlsx", "pptx": "paper-pptx"}
# upstream distributions that must not be installed
UPSTREAM = ("python-docx", "openpyxl", "python-pptx")
OTHERS = ("reportlab", "pypdf", "pdfplumber", "pdf2image", "pypdfium2", "pandas", "PIL")

problems = []
owners = md.packages_distributions()
for mod, want in PAPER.items():
    try:
        importlib.import_module(mod)
    except Exception as e:  # any import error fails the build
        problems.append(f"import {mod}: {e}")
        continue
    got = sorted(owners.get(mod, []))
    if got != [want]:
        problems.append(f"{mod} comes from {got}, expected only [{want!r}]")
for dist in UPSTREAM:
    try:
        md.distribution(dist)
    except md.PackageNotFoundError:
        continue
    problems.append(f"upstream package {dist} is installed")
for mod in OTHERS:
    try:
        importlib.import_module(mod)
    except Exception as e:
        problems.append(f"import {mod}: {e}")
if problems:
    sys.exit("guest image Python check failed:\n  " + "\n  ".join(problems))
print("guest image Python check: ok")
