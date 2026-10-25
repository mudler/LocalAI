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

// Thrown when a PDF parses but carries no text layer (a scan). Callers can
// fall back to renderPdfPages and send the pages as images instead.
export class PdfNoTextError extends Error {
  constructor() {
    super('PDF has no extractable text')
    this.name = 'PdfNoTextError'
  }
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
    if (!text) throw new PdfNoTextError()
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

// Pages of a scanned PDF as PNG attachments ({ name, type, base64 }), so a
// vision model can read them. Renders at most maxPages pages; `total` tells the
// caller how many the document has. Scale 1.5 keeps small print legible at a
// size the vision encoders take.
export const SCANNED_PDF_MAX_PAGES = 10
export async function renderPdfPages(file, { maxPages = SCANNED_PDF_MAX_PAGES, scale = 1.5 } = {}) {
  const pdfjs = await loadPdfjs()
  const data = new Uint8Array(await file.arrayBuffer())
  const doc = await pdfjs.getDocument({ data }).promise
  try {
    const stem = (file.name || 'document.pdf').replace(/\.pdf$/i, '')
    const pages = []
    const n = Math.min(doc.numPages, maxPages)
    for (let i = 1; i <= n; i++) {
      const page = await doc.getPage(i)
      const viewport = page.getViewport({ scale })
      const canvas = document.createElement('canvas')
      canvas.width = Math.ceil(viewport.width)
      canvas.height = Math.ceil(viewport.height)
      await page.render({ canvasContext: canvas.getContext('2d'), viewport }).promise
      const url = canvas.toDataURL('image/png')
      pages.push({ name: `${stem}-page-${i}.png`, type: 'image/png', base64: url.split(',')[1] })
    }
    return { pages, total: doc.numPages }
  } finally {
    await doc.destroy()
  }
}
