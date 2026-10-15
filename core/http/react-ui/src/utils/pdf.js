export function isPdf(file) {
  return file?.type === 'application/pdf' || /\.pdf$/i.test(file?.name || '')
}

// pdf.js and its worker are loaded on first use so the main bundle does not
// pay for them when nobody attaches a PDF.
async function loadPdfjs() {
  const [pdfjs, worker] = await Promise.all([
    import('pdfjs-dist'),
    import('pdfjs-dist/build/pdf.worker.min.mjs?url'),
  ])
  pdfjs.GlobalWorkerOptions.workerSrc = worker.default
  return pdfjs
}

// Returns the text layer of every page, one block per page. Throws when the
// file cannot be parsed or has no text layer (scanned PDFs): sending an empty
// attachment to the model would look like success and silently lose the file.
export async function extractPdfText(file) {
  const pdfjs = await loadPdfjs()
  const data = new Uint8Array(await file.arrayBuffer())
  const doc = await pdfjs.getDocument({ data }).promise
  try {
    const pages = []
    for (let i = 1; i <= doc.numPages; i++) {
      const page = await doc.getPage(i)
      const content = await page.getTextContent()
      let text = ''
      for (const item of content.items) {
        text += item.str
        text += item.hasEOL ? '\n' : ''
      }
      pages.push(text.trim())
    }
    const text = pages.filter(Boolean).join('\n\n')
    if (!text) throw new Error('PDF has no extractable text')
    return text
  } finally {
    await doc.destroy()
  }
}

// Text of an attached non-media file. PDFs go through pdf.js; everything else
// is read as UTF-8.
export async function readAttachmentText(file) {
  if (isPdf(file)) return extractPdfText(file)
  return file.text().catch(() => '')
}
