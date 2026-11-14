//go:build darwin && arm64

package vz

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The checks of the Python stack and of the document tools run in a real guest.
// They need SANDBOX_TEST_IMAGE like the other integration specs. The checks
// of the media profile run when SANDBOX_TEST_PROFILE is "media", with the image
// of that profile in SANDBOX_TEST_IMAGE.
var _ = Describe("Python and the document tools in the guest", func() {
	It("imports the libraries and computes with pandas and numpy", func() {
		sb, _ := realFixture(testRealBackend(), "")
		Expect(strings.TrimSpace(sh(sb, `python3 -c "import pandas, numpy, pypdf, docx, openpyxl, pptx, matplotlib, PIL; print('ok')"`))).To(Equal("ok"))
		got := sh(sb, `python3 - <<'PY'
import numpy as np, pandas as pd
df = pd.DataFrame({"g": ["a", "b", "a", "b"], "v": np.arange(4)})
print(df.groupby("g")["v"].sum().to_dict(), float(np.linalg.norm(np.array([3.0, 4.0]))))
PY`)
		Expect(strings.TrimSpace(got)).To(Equal("{'a': 2, 'b': 4} 5.0"))
	})

	It("writes and reads back an xlsx and a docx in the workspace", func() {
		sb, _ := realFixture(testRealBackend(), "")
		got := sh(sb, `cd proj && python3 - <<'PY'
import openpyxl, docx
wb = openpyxl.Workbook(); ws = wb.active
ws["A1"] = "total"; ws["B1"] = 42; ws["B2"] = "=B1*2"
wb.save("t.xlsx")
d = docx.Document(); d.add_heading("Title", 1); d.add_paragraph("hello docx"); d.save("t.docx")
w = openpyxl.load_workbook("t.xlsx")["Sheet"]
print(w["A1"].value, w["B1"].value, w["B2"].value)
print([p.text for p in docx.Document("t.docx").paragraphs])
PY`)
		Expect(got).To(Equal("total 42 =B1*2\n['Title', 'hello docx']\n"))
	})

	It("makes a PDF with matplotlib and extracts its text with pdftotext", func() {
		sb, _ := realFixture(testRealBackend(), "")
		sh(sb, `cd proj && python3 - <<'PY'
import matplotlib.pyplot as plt
fig = plt.figure(); fig.text(0.1, 0.5, "vz pdf marker"); fig.savefig("t.pdf")
PY`)
		Expect(sh(sb, `cd proj && pdftotext t.pdf - `)).To(ContainSubstring("vz pdf marker"))
	})

	It("passes the runtime checks of the tools of the profile", func() {
		sb, _ := realFixture(testRealBackend(), "")
		sh(sb, `python3 /usr/local/lib/localai/document-skills/check-document-skills.py`)
		profile := "base"
		if p := testProfile(); p != "" {
			profile = p
		}
		sh(sb, `sh /usr/local/lib/localai/check-tools.sh `+profile+` /usr/local/lib/localai/manifest.json`)
	})

	It("runs the media tools", func() {
		if testProfile() != "media" {
			Skip("set SANDBOX_TEST_PROFILE=media with the media image")
		}
		sb, _ := realFixture(testRealBackend(), "")
		got := sh(sb, `cd proj
ffmpeg -loglevel error -f lavfi -i testsrc=duration=1:size=64x64:rate=10 -f lavfi -i sine=frequency=440:duration=1 -shortest -pix_fmt yuv420p t.mp4
ffprobe -v error -show_entries stream=codec_type -of csv=p=0 t.mp4 | sort | tr '\n' ' '
echo
sox -n t.wav synth 0.5 sine 440 && soxi -D t.wav
printf '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="10"><rect width="20" height="10" fill="red"/></svg>' > t.svg
rsvg-convert t.svg -o t.png && magick identify -format '%wx%h\n' t.png
magick t.png -resize 40x20 t2.png && identify -format '%wx%h\n' t2.png
ffmpeg -loglevel error -i t.mp4 -frames:v 1 f.png && magick identify -format '%wx%h\n' f.png`)
		Expect(got).To(Equal("audio video \n0.500000\n20x10\n40x20\n64x64\n"))
	})
})
