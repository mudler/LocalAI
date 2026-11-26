import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
// eslint-disable-next-line no-unused-vars
import Sheet from '../../../../talk/ui/src/components/talk/Sheet'
import { cropToDataUrl, squareCrop } from '../lib/portrait'

const PREVIEW = 288

// The portraits kept in this browser, the one in use, and Upload. An upload
// opens the crop sheet: the model wants one face near the centre of a square.
export default function PortraitPicker({ portraits, selectedId, onSelect, onAdd, onRemove, disabled }) {
  const { t } = useTranslation('head')
  const inputRef = useRef(null)
  const [file, setFile] = useState(null)

  return (
    <div className="head-portraits" data-testid="head-portraits">
      <div className="head-portraits__row" role="group" aria-label={t('portrait.library')}>
        {portraits.map((p) => (
          <span key={p.id} className="head-thumb" data-selected={p.id === selectedId}>
            <button
              type="button"
              className="head-thumb__pick"
              aria-pressed={p.id === selectedId}
              aria-label={t('portrait.use', { name: p.name })}
              title={p.name}
              disabled={disabled}
              onClick={() => onSelect(p.id)}
            >
              <img src={p.dataUrl} alt="" />
            </button>
            <button
              type="button"
              className="head-thumb__remove"
              aria-label={t('portrait.remove', { name: p.name })}
              title={t('portrait.remove', { name: p.name })}
              disabled={disabled}
              onClick={() => onRemove(p.id)}
            >
              <Icon name="close" />
            </button>
          </span>
        ))}
        <button type="button" className="talk-btn head-upload" onClick={() => inputRef.current?.click()} disabled={disabled} data-testid="head-upload">
          <Icon name="upload" /> {portraits.length ? t('portrait.add') : t('portrait.upload')}
        </button>
        <input
          ref={inputRef}
          type="file"
          accept="image/png,image/jpeg,image/webp"
          className="hidden"
          data-testid="head-upload-input"
          onChange={(e) => { const f = e.target.files?.[0]; e.target.value = ''; if (f) setFile(f) }}
        />
      </div>
      {file && (
        <CropSheet
          file={file}
          onCancel={() => setFile(null)}
          onDone={(dataUrl) => {
            const name = file.name.replace(/\.[^.]+$/, '').slice(0, 60) || t('portrait.untitled')
            onAdd({ id: `p${Date.now().toString(36)}`, name, dataUrl })
            setFile(null)
          }}
        />
      )}
    </div>
  )
}

// Crops an image to a square: drag or use the arrow keys to move the frame,
// the slider to zoom. Save draws the square at 512x512.
function CropSheet({ file, onCancel, onDone }) {
  const { t } = useTranslation('head')
  const canvasRef = useRef(null)
  const [image, setImage] = useState(null)
  const [failed, setFailed] = useState(false)
  const [view, setView] = useState({ zoom: 1, x: 0.5, y: 0.5 })
  const dragRef = useRef(null)

  useEffect(() => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => setImage(img)
    img.onerror = () => setFailed(true)
    img.src = url
    return () => URL.revokeObjectURL(url)
  }, [file])

  const crop = image ? squareCrop(image.naturalWidth, image.naturalHeight, view) : null

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || !image || !crop) return
    const g = canvas.getContext('2d')
    g.clearRect(0, 0, PREVIEW, PREVIEW)
    g.drawImage(image, crop.sx, crop.sy, crop.side, crop.side, 0, 0, PREVIEW, PREVIEW)
  }, [image, crop?.sx, crop?.sy, crop?.side])

  // Moves the frame by dx, dy preview pixels: the picture follows the pointer.
  const move = (dx, dy) => {
    if (!image || !crop) return
    const perPixel = crop.side / PREVIEW
    const spanX = image.naturalWidth - crop.side
    const spanY = image.naturalHeight - crop.side
    setView((v) => ({
      ...v,
      x: spanX > 0 ? Math.min(1, Math.max(0, v.x - (dx * perPixel) / spanX)) : 0.5,
      y: spanY > 0 ? Math.min(1, Math.max(0, v.y - (dy * perPixel) / spanY)) : 0.5,
    }))
  }

  const onKey = (e) => {
    const step = e.shiftKey ? 24 : 6
    const d = { ArrowLeft: [step, 0], ArrowRight: [-step, 0], ArrowUp: [0, step], ArrowDown: [0, -step] }[e.key]
    if (!d) return
    e.preventDefault()
    move(d[0], d[1])
  }

  return (
    <Sheet id="head-crop" title={t('crop.title')} desc={t('crop.desc')} closeLabel={t('crop.cancel')} onClose={onCancel} testId="head-crop">
      <section className="talk-sec">
        {failed ? (
          <p className="talk-help" role="alert">{t('crop.unreadable')}</p>
        ) : (
          <>
            <canvas
              ref={canvasRef}
              width={PREVIEW}
              height={PREVIEW}
              className="head-crop__canvas"
              tabIndex={0}
              role="img"
              aria-label={t('crop.preview')}
              onKeyDown={onKey}
              onPointerDown={(e) => { dragRef.current = { x: e.clientX, y: e.clientY }; e.currentTarget.setPointerCapture?.(e.pointerId) }}
              onPointerMove={(e) => {
                const last = dragRef.current
                if (!last) return
                const rect = e.currentTarget.getBoundingClientRect()
                const k = PREVIEW / (rect.width || PREVIEW)
                move((e.clientX - last.x) * k, (e.clientY - last.y) * k)
                dragRef.current = { x: e.clientX, y: e.clientY }
              }}
              onPointerUp={() => { dragRef.current = null }}
              onPointerCancel={() => { dragRef.current = null }}
            />
            <div className="talk-field">
              <label htmlFor="head-crop-zoom">{t('crop.zoom')}</label>
              <input id="head-crop-zoom" type="range" min="1" max="4" step="0.05" value={view.zoom} onChange={(e) => setView((v) => ({ ...v, zoom: Number(e.target.value) }))} />
            </div>
            <p className="talk-help">{t('crop.help')}</p>
          </>
        )}
        <div className="talk-card__acts">
          <button type="button" className="talk-btn" onClick={onCancel}>{t('crop.cancel')}</button>
          <button
            type="button"
            className="talk-btn head-btn--primary"
            disabled={!image}
            data-testid="head-crop-save"
            onClick={() => onDone(cropToDataUrl(image, crop))}
          >
            <Icon name="check" /> {t('crop.save')}
          </button>
        </div>
      </section>
    </Sheet>
  )
}
