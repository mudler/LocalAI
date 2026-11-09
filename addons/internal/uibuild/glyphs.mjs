// The line glyphs the icon generator draws on a tile. One set, one style: a
// white stroke about two units wide with round ends, on a 24 by 24 grid, the
// same look as the committed icons of the first-party add-ons. A glyph is a
// list of shapes: { line: [[x, y], ...] } is an open path, { loop: [...] } a
// closed one, and { dot: [x, y] } a filled dot. The helpers below turn
// rounded rectangles, ellipses and curves into points.

const rad = (a) => (a * Math.PI) / 180

// A rounded rectangle as a closed path.
function box(x, y, w, h, r) {
  const pts = []
  const corner = (cx, cy, from) => { for (let i = 0; i <= 6; i++) pts.push([cx + r * Math.cos(rad(from + i * 15)), cy + r * Math.sin(rad(from + i * 15))]) }
  corner(x + w - r, y + r, -90)
  corner(x + w - r, y + h - r, 0)
  corner(x + r, y + h - r, 90)
  corner(x + r, y + r, 180)
  return { loop: pts }
}

function ellipse(cx, cy, rx, ry) {
  const pts = []
  for (let i = 0; i < 48; i++) pts.push([cx + rx * Math.cos(rad(i * 7.5)), cy + ry * Math.sin(rad(i * 7.5))])
  return { loop: pts }
}

// A quadratic curve from a to b with control point c, as an open path.
function curve(a, c, b) {
  const pts = []
  for (let i = 0; i <= 16; i++) {
    const t = i / 16
    pts.push([(1 - t) ** 2 * a[0] + 2 * (1 - t) * t * c[0] + t * t * b[0], (1 - t) ** 2 * a[1] + 2 * (1 - t) * t * c[1] + t * t * b[1]])
  }
  return { line: pts }
}

export const GLYPHS = {
  // A closed envelope.
  mail: [box(3, 5.5, 18, 13, 2.5), { line: [[3.8, 7], [12, 13.2], [20.2, 7]] }],
  // A notebook with its binding rings.
  memory: [box(5.5, 3.5, 14, 17, 2.5), { line: [[9.5, 8.5], [15.5, 8.5]] }, { line: [[9.5, 12], [15.5, 12]] }, { line: [[9.5, 15.5], [13, 15.5]] }, { line: [[3, 7], [8, 7]] }, { line: [[3, 12], [8, 12]] }, { line: [[3, 17], [8, 17]] }],
  // A month page with two rings and a few days.
  calendar: [box(3.5, 5, 17, 15.5, 2.5), { line: [[3.5, 10], [20.5, 10]] }, { line: [[8, 3], [8, 6.8]] }, { line: [[16, 3], [16, 6.8]] }, { dot: [8, 14.2] }, { dot: [12, 14.2] }, { dot: [16, 14.2] }, { dot: [8, 17.4] }, { dot: [12, 17.4] }],
  // An address book: a person on a page with index tabs.
  contacts: [box(4.5, 3.5, 14, 17, 2.5), { loop: ellipse(11.5, 10, 2.6, 2.6).loop }, curve([7, 17], [11.5, 11.8], [16, 17]), { line: [[18.5, 7.5], [21, 7.5]] }, { line: [[18.5, 12], [21, 12]] }, { line: [[18.5, 16.5], [21, 16.5]] }],
  // A globe.
  web: [ellipse(12, 12, 9, 9), ellipse(12, 12, 4, 9), { line: [[3, 12], [21, 12]] }],
  // A hash sign, for channels.
  hash: [{ line: [[9.5, 4], [7.5, 20]] }, { line: [[16.5, 4], [14.5, 20]] }, { line: [[4.5, 9], [20.5, 9]] }, { line: [[3.5, 15], [19.5, 15]] }],
  // A paper plane, for messages sent out.
  send: [{ loop: [[21, 3], [3, 10.5], [9.5, 13.5], [12.5, 21]] }, { line: [[9.5, 13.5], [21, 3]] }],
  // A speech bubble with two dots.
  chat: [{ loop: [[6, 4], [18, 4], [21, 7], [21, 13.5], [18, 16.5], [12.5, 16.5], [8, 20.5], [8, 16.5], [6, 16.5], [3, 13.5], [3, 7]] }, { dot: [9, 10.2] }, { dot: [15, 10.2] }],
  // A branch: two commits joined by a line, one merging in.
  branch: [{ loop: ellipse(6.5, 5.5, 2.2, 2.2).loop }, { loop: ellipse(6.5, 18.5, 2.2, 2.2).loop }, { loop: ellipse(17.5, 8.5, 2.2, 2.2).loop }, { line: [[6.5, 7.7], [6.5, 16.3]] }, curve([17.5, 10.7], [17.5, 14.5], [8.5, 15.2])],
  // A house.
  home: [{ line: [[3, 11.2], [12, 3.5], [21, 11.2]] }, { line: [[5.5, 9.5], [5.5, 20.5], [18.5, 20.5], [18.5, 9.5]] }, { line: [[10, 20.5], [10, 15], [14, 15], [14, 20.5]] }],
  // A screen with a play button, for media.
  play: [box(3, 4.5, 18, 15, 2.5), { loop: [[10, 9], [15.5, 12], [10, 15]] }],
  // A terminal window with a prompt, for a remote shell.
  terminal: [box(3, 4, 18, 16, 2.5), { line: [[7, 9.5], [10.5, 12.5], [7, 15.5]] }, { line: [[13, 15.5], [17, 15.5]] }],
  // A screen on a stand, for a remote machine.
  screen: [box(3, 4, 18, 12, 2.5), { line: [[12, 16], [12, 20]] }, { line: [[8, 20], [16, 20]] }],
  // The fallback: a rounded square with a plus, for an add-on that names no glyph.
  app: [box(4, 4, 16, 16, 4), { line: [[12, 8.5], [12, 15.5]] }, { line: [[8.5, 12], [15.5, 12]] }],
}
