"""Build-time check of the Python libraries in the guest image.

It fails the image build when a library is missing, when docx, openpyxl or
pptx do not come from the Paper distributions, when an upstream package
that the Paper packages replace is installed next to them, or when matplotlib
cannot draw a chart from a pandas frame and save it as PNG and SVG.
"""
import importlib
import importlib.metadata as md
import os
import sys
import tempfile

# import name -> the only distribution that may provide it
PAPER = {"docx": "paper-docx", "openpyxl": "paper-xlsx", "pptx": "paper-pptx"}
# upstream distributions that must not be installed
UPSTREAM = ("python-docx", "openpyxl", "python-pptx")
OTHERS = ("reportlab", "pypdf", "pdfplumber", "pdf2image", "pypdfium2", "pandas", "numpy", "PIL")

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


def check_chart():
    """Draw a bar chart from a pandas frame and save it as PNG and SVG."""
    # The image sets MPLBACKEND and MPLCONFIGDIR; this check sets them in
    # case it runs without them, and uses a throw-away font cache.
    with tempfile.TemporaryDirectory() as tmp:
        os.environ.setdefault("MPLBACKEND", "Agg")
        os.environ["MPLCONFIGDIR"] = os.path.join(tmp, "mpl")
        import matplotlib
        import matplotlib.pyplot as plt
        import pandas as pd

        if matplotlib.get_backend().lower() != "agg":
            problems.append(f"matplotlib backend is {matplotlib.get_backend()!r}, expected 'agg'")
        df = pd.DataFrame({"month": ["Jan", "Feb", "Mar"], "sales": [3, 5, 4]})
        fig, ax = plt.subplots()
        ax.bar(df["month"], df["sales"])
        ax.set_title("Sales per month")
        ax.set_ylabel("Sales")
        for ext, magic in (("png", b"\x89PNG\r\n\x1a\n"), ("svg", b"<?xml")):
            path = os.path.join(tmp, "chart." + ext)
            fig.savefig(path)
            with open(path, "rb") as f:
                data = f.read()
            if not data.startswith(magic) or len(data) < 500:
                problems.append(f"chart.{ext} is empty or not a {ext.upper()} file ({len(data)} bytes)")
        # Text needs a font file; the default font must resolve to a real one.
        from matplotlib import font_manager

        font = font_manager.findfont(font_manager.FontProperties(), fallback_to_default=False)
        if not os.path.isfile(font):
            problems.append(f"matplotlib default font {font!r} is missing")
        plt.close(fig)


try:
    check_chart()
except Exception as e:  # any error fails the build
    problems.append(f"matplotlib chart: {e}")
if problems:
    sys.exit("guest image Python check failed:\n  " + "\n  ".join(problems))
print("guest image Python check: ok")
