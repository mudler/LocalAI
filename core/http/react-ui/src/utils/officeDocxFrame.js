// The page a Word document is drawn into: an isolated frame (see OfficeView).

// What the frame may load: nothing from the network, only what the renderer
// embeds in the page itself. The frame has no scripts either (see the sandbox
// of the frame in OfficeView), so a document cannot run code.
export const DOCX_FRAME_CSP = "default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'; font-src data:"

export function docxFrameSource(colors) {
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${DOCX_FRAME_CSP}">`
    + `<style>html,body{margin:0;background:${colors.bg};color:${colors.fg}}`
    + `.docx-wrapper{background:transparent!important;padding:0!important}`
    + `.docx-wrapper>section.docx{margin:0 0 12px!important;box-shadow:0 0 0 1px ${colors.edge}!important}`
    + `a{pointer-events:none}</style></head><body><div id="styles"></div><div id="root"></div></body></html>`
}
