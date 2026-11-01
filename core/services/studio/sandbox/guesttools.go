package sandbox

// GuestTools is what the guest image has for the model to run. The Dockerfile
// of the guest image (addons/sandbox/images/guest) installs it, and its build
// check fails when a program is missing. The Commands block of the prompt and
// the Sandbox documentation name this list, so change it in all three places
// together; the tests keep them equal.
const GuestTools = "python3 (pandas, openpyxl, docx, pptx, pypdf, Pillow), pip, jq, ripgrep (rg), sqlite3, zip, unzip, file, tree, poppler (pdftotext, pdftoppm), git, curl"

// GuestToolsNote is the sentence of the Commands block (the prompt, not the
// 400-character turn note) that tells the model which programs the sandbox
// has. The guest user cannot install packages with apt, so the model must not
// try.
const GuestToolsNote = "The sandbox has: " + GuestTools + ". It has no Node, ImageMagick, ffmpeg or OCR, and apt cannot install anything."
