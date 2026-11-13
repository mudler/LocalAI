// Draws a presentation slide by slide on a canvas. The renderer (PptxViewJS)
// loads on first use, so it is not in the main bundle. A canvas takes no HTML
// from the file: slides are pixels.

// Opens buffer and draws slide 0 on canvas, sized to the slide's shape at
// width device pixels. Returns { count, show(i), close() }.
export async function openSlides(buffer, canvas, width) {
  // The renderer looks for JSZip as a global and, failing that, loads it from
  // a public CDN. Hand it the bundled copy so it never goes to the network.
  if (!globalThis.JSZip) globalThis.JSZip = (await import('jszip')).default
  const { PPTXViewer } = await import('pptxviewjs')
  canvas.width = width
  canvas.height = Math.round(width * 9 / 16)
  const viewer = new PPTXViewer({ canvas, backgroundColor: 'white' })
  await viewer.loadFile(buffer)
  const count = viewer.getSlideCount()
  const size = viewer.processor?.getSlideDimensions?.()
  const ratio = size && size.cx > 0 && size.cy > 0 ? size.cy / size.cx : 9 / 16
  if (Math.abs(ratio - 9 / 16) > 0.01) {
    canvas.width = width
    canvas.height = Math.round(width * ratio)
  }
  const show = async (i) => { await viewer.goToSlide(i, canvas) }
  await viewer.render(canvas, { slideIndex: 0 })
  return { count, show, close: () => viewer.destroy() }
}
