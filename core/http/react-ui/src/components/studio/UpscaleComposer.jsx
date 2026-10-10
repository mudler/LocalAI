// SPDX-License-Identifier: MIT
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { imageApi } from '../../utils/api'
import { imageDimensions, sourceImageFile } from '../../utils/upscale'

export default function UpscaleComposer({ source, upscalers, onSuccess }) {
  const { t } = useTranslation('media')
  const [model, setModel] = useState(upscalers[0]?.id || '')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const active = useRef(true)
  const running = useRef(false)
  useEffect(() => { active.current = true; return () => { active.current = false } }, [])
  const selected = upscalers.find(m => m.id === model) || upscalers[0]

  const submit = async (event) => {
    event.preventDefault()
    if (!selected || running.current) return
    running.current = true
    setLoading(true)
    setError('')
    try {
      const image = await sourceImageFile(source.url)
      const sourceDimensions = await imageDimensions(image)
      if (!active.current) return
      const response = await imageApi.upscale({ model: selected.id, image, scale: selected.upscaleScale })
      const results = (response?.data || []).map(r => ({ url: r.url || (r.b64_json ? `data:image/png;base64,${r.b64_json}` : '') }))
      if (!results.length || results.some(r => !r.url)) throw new Error(t('studio.upscale.noResult'))
      // A result that cannot be decoded is a failed run: never record guessed
      // dimensions or an unusable child. The endpoint's dimensions are ignored.
      const dimensions = await Promise.all(results.map(r => imageDimensions(r.url)))
      if (!active.current) return
      onSuccess({ model: selected.id, params: { scale: selected.upscaleScale, sourceDimensions, outputDimensions: dimensions[0] }, results, parentId: source.id, edge: 'upscale' })
    } catch (err) {
      if (active.current) setError(t('studio.upscale.failed', { message: err.message }))
    } finally {
      running.current = false
      if (active.current) setLoading(false)
    }
  }

  return (
    <form className="studio-dock__body studio-detail" onSubmit={submit} data-testid="upscale-composer" aria-busy={loading}>
      <div className="studio-detail__preview"><img src={source.url} alt={source.title || t('studio.upscale.source')} /></div>
      <div className="studio-detail__info">
        <label className="studio-eyebrow">{t('studio.upscale.model')}
          <select className="dk-input" value={selected?.id || ''} onChange={e => setModel(e.target.value)} disabled={loading}>
            {upscalers.map(m => <option key={m.id} value={m.id}>{m.id}</option>)}
          </select>
        </label>
        <label className="studio-eyebrow">{t('studio.upscale.scale')}
          <input className="dk-input" value={selected ? `${selected.upscaleScale}×` : ''} readOnly />
        </label>
        {error && <p role="alert">{error}</p>}
        <button className="dk-btn dk-btn--primary" type="submit" disabled={loading || !selected}>
          {t(loading ? 'studio.upscale.loading' : 'studio.steps.upscale.title')}
        </button>
      </div>
    </form>
  )
}
