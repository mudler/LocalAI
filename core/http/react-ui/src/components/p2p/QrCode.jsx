import { useMemo } from 'react'
import { qrPath } from './qrPath'

// An invite link drawn as a QR code. The quiet zone of four modules is part
// of the drawing. The tile stays dark on light in both themes: some phone
// cameras do not read a light-on-dark code.
export default function QrCode({ text, label, testId = 'qr-code' }) {
  const { size, d } = useMemo(() => qrPath(text), [text])
  const box = size + 8
  return (
    <svg className="p2p-qr" viewBox={`-4 -4 ${box} ${box}`} role="img" aria-label={label} data-testid={testId} shapeRendering="crispEdges">
      <rect className="p2p-qr__tile" x="-4" y="-4" width={box} height={box} />
      <path className="p2p-qr__modules" d={d} />
    </svg>
  )
}
