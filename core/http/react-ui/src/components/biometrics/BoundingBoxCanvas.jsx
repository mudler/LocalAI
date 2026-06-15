import { useEffect, useRef, useState } from 'react'

// BoundingBoxCanvas — overlay face-detection rectangles on the user-supplied image.
// boxes: [{ x, y, w, h, label?, sublabel?, tone? }]
// tone: 'default' | 'success' | 'warning' | 'error' | 'accent'
export default function BoundingBoxCanvas({ src, boxes = [], alt = '' }) {
  const wrapRef = useRef(null)
  const imgRef = useRef(null)
  const [dims, setDims] = useState({ w: 0, h: 0, natW: 0, natH: 0 })

  useEffect(() => {
    const update = () => {
      if (!wrapRef.current || !imgRef.current) return
      const rect = imgRef.current.getBoundingClientRect()
      setDims({
        w: rect.width,
        h: rect.height,
        natW: imgRef.current.naturalWidth || 1,
        natH: imgRef.current.naturalHeight || 1,
      })
    }
    update()
    const ro = new ResizeObserver(update)
    if (imgRef.current) ro.observe(imgRef.current)
    window.addEventListener('resize', update)
    return () => {
      ro.disconnect()
      window.removeEventListener('resize', update)
    }
  }, [src])

  const sx = dims.natW ? dims.w / dims.natW : 1
  const sy = dims.natH ? dims.h / dims.natH : 1

  return (
    <div ref={wrapRef} className="biometrics-bbox">
      {src && <img ref={imgRef} src={src} alt={alt} onLoad={(e) => {
        setDims({
          w: e.target.getBoundingClientRect().width,
          h: e.target.getBoundingClientRect().height,
          natW: e.target.naturalWidth,
          natH: e.target.naturalHeight,
        })
      }} />}
      {boxes.map((b, i) => (
        <div key={i} className={`biometrics-bbox__box tone-${b.tone || 'accent'}`}
          style={{
            left: `${b.x * sx}px`,
            top: `${b.y * sy}px`,
            width: `${b.w * sx}px`,
            height: `${b.h * sy}px`,
          }}>
          {(b.label || b.sublabel) && (
            <div className="biometrics-bbox__tag">
              {b.label && <strong>{b.label}</strong>}
              {b.sublabel && <span>{b.sublabel}</span>}
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
