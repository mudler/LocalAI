import qrcode from 'qrcode-generator'

// The modules of a QR code as one SVG path: a unit square for each dark
// module. Error correction M keeps an invite link with two relays near
// version 13, which a phone camera reads at the size the dialog draws.
export function qrPath(text) {
  const qr = qrcode(0, 'M')
  qr.addData(text, 'Byte')
  qr.make()
  const n = qr.getModuleCount()
  let d = ''
  for (let y = 0; y < n; y++) {
    for (let x = 0; x < n; x++) {
      if (qr.isDark(y, x)) d += `M${x} ${y}h1v1h-1z`
    }
  }
  return { size: n, d }
}
