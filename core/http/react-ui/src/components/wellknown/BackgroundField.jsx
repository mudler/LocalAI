/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { filesApi } from '../../utils/api'
import { backgroundImagePath, homeBackground, homeBackgroundStyle } from '../../utils/wellKnown'
import { sortEntries } from '../../utils/fileBrowser'

const KINDS = ['none', 'color', 'gradient', 'image']
const IMAGE_FILE = /\.(png|jpe?g|webp|gif|avif)$/i
const LAYERS = ['color', 'gradient', 'image']
const FALLBACK = { color: '#1e293b', from: '#0f172a', to: '#3b82f6' }

function kindOf(v) {
  if (v?.image) return 'image'
  if (v?.gradient) return 'gradient'
  if (v?.color) return 'color'
  return 'none'
}

// ImagePicker walks the folders of the user's files and offers the pictures in
// the open one. Its choice is a path without a leading slash, as the file stores it.
function ImagePicker({ onPick, onClose }) {
  const { t } = useTranslation('workspace')
  const [at, setAt] = useState('/')
  const [state, setState] = useState({ status: 'loading', entries: [] })
  useEffect(() => {
    let live = true
    setState(s => ({ ...s, status: 'loading' }))
    filesApi.list(at)
      .then(res => { if (live) setState({ status: 'ready', entries: sortEntries((res.entries || []).filter(e => e.kind === 'dir' || (e.kind === 'file' && IMAGE_FILE.test(e.name)))) }) })
      .catch(() => { if (live) setState({ status: 'error', entries: [] }) })
    return () => { live = false }
  }, [at])
  const parts = at.split('/').filter(Boolean)
  return (
    <div className="wk-bgpick" data-testid="wk-bg-picker">
      <nav className="wk-bgpick__path" aria-label={t('breadcrumb')}>
        <button type="button" className="wk-linkbtn" onClick={() => setAt('/')}><Icon name="home" /> {t('home')}</button>
        {parts.map((p, i) => (
          <button key={p + i} type="button" className="wk-linkbtn" onClick={() => setAt(`/${parts.slice(0, i + 1).join('/')}`)}>/ {p}</button>
        ))}
      </nav>
      <ul className="wk-bgpick__list" aria-busy={state.status === 'loading'}>
        {state.status === 'error' && <li className="wk-bgpick__note" role="alert">{t('wellKnown.bg.pickFailed')}</li>}
        {state.status === 'ready' && state.entries.length === 0 && <li className="wk-bgpick__note">{t('wellKnown.bg.pickEmpty')}</li>}
        {state.entries.map(e => (
          <li key={e.path}>
            {e.kind === 'dir' ? (
              <button type="button" className="wk-bgpick__item" onClick={() => setAt(e.path)}><Icon name="folder" /><span>{e.name}</span></button>
            ) : (
              <button type="button" className="wk-bgpick__item" data-testid={`wk-bg-pick-${e.name}`} onClick={() => onPick(e.path.replace(/^\/+/, ''))}>
                <img src={filesApi.contentUrl(e.path, true)} alt="" loading="lazy" /><span>{e.name}</span>
              </button>
            )}
          </li>
        ))}
      </ul>
      <button type="button" className="wk-linkbtn" onClick={onClose}>{t('wellKnown.bg.pickClose')}</button>
    </div>
  )
}

// BackgroundField edits homeBackground of ui.json: one kind (colour, gradient
// or picture) and the tuning (fit, opacity, blur, veil). Keys it does not
// show stay in the value, so a save keeps them.
export default function BackgroundField({ id, name, value, onChange, disabled, describedBy }) {
  const { t } = useTranslation('workspace')
  const v = useMemo(() => (value && typeof value === 'object' && !Array.isArray(value) ? value : {}), [value])
  const [picked, setPicked] = useState(null)
  const [browsing, setBrowsing] = useState(false)
  const kind = picked && kindOf(v) === 'none' ? picked : kindOf(v)
  const tid = (s) => `wk-bg-${s}`

  const rest = (keep) => Object.fromEntries(Object.entries(v).filter(([k]) => !LAYERS.includes(k) || keep.includes(k)))
  const pickKind = (k) => {
    setPicked(k)
    setBrowsing(k === 'image' && !v.image)
    if (k === 'none') onChange({})
    else if (k === 'color') onChange({ ...rest([]), color: v.color || FALLBACK.color })
    else if (k === 'gradient') onChange({ ...rest([]), gradient: v.gradient || { from: FALLBACK.from, to: FALLBACK.to, angle: 135 } })
    else onChange({ ...rest([]), ...(v.image ? { image: v.image } : {}) })
  }
  const set = (k, val) => onChange({ ...v, [k]: val })
  const setGrad = (k, val) => set('gradient', { ...(v.gradient || { from: FALLBACK.from, to: FALLBACK.to }), [k]: val })
  const clear = () => { setPicked(null); setBrowsing(false); onChange({}) }

  const style = useMemo(() => homeBackgroundStyle(homeBackground({ homeBackground: v }), (p) => filesApi.contentUrl(`/${p}`, true)), [v])
  const imageBad = !!v.image && !backgroundImagePath(v.image)
  const veilDefault = kind === 'image' ? 0.55 : kind === 'gradient' ? 0.35 : 0
  const colorOk = (c) => (/^#[0-9a-fA-F]{6}$/.test(c || '') ? c : FALLBACK.color)

  return (
    <div className="wk-bg" id={id} aria-describedby={describedBy} data-testid={`wk-field-${name}-homeBackground`}>
      <div className="dk-segmented" role="radiogroup" aria-label={t('wellKnown.bg.kind')}>
        {KINDS.map(k => (
          <button key={k} type="button" role="radio" aria-checked={kind === k} className="dk-seg" disabled={disabled} data-testid={tid(`kind-${k}`)} onClick={() => pickKind(k)}>
            {t(`wellKnown.bg.kinds.${k}`)}
          </button>
        ))}
      </div>

      {kind === 'color' && (
        <span className="wk-color">
          <input type="color" className="wk-color__swatch" aria-label={t('wellKnown.bg.color')} value={colorOk(v.color)} disabled={disabled} onChange={e => set('color', e.target.value)} />
          <input type="text" className="dk-input dk-mono" aria-label={t('wellKnown.bg.color')} data-testid={tid('color')} value={v.color || ''} maxLength={7} disabled={disabled} onChange={e => set('color', e.target.value)} />
        </span>
      )}

      {kind === 'gradient' && (
        <div className="wk-bg__row">
          <label className="wk-bg__small">{t('wellKnown.bg.from')}
            <input type="color" className="wk-color__swatch" value={colorOk(v.gradient?.from)} disabled={disabled} data-testid={tid('from')} onChange={e => setGrad('from', e.target.value)} />
          </label>
          <label className="wk-bg__small">{t('wellKnown.bg.to')}
            <input type="color" className="wk-color__swatch" value={colorOk(v.gradient?.to)} disabled={disabled} data-testid={tid('to')} onChange={e => setGrad('to', e.target.value)} />
          </label>
          <label className="wk-bg__small wk-bg__grow">{t('wellKnown.bg.angle', { n: v.gradient?.angle ?? 180 })}
            <input type="range" min="0" max="360" step="5" value={v.gradient?.angle ?? 180} disabled={disabled} data-testid={tid('angle')} onChange={e => setGrad('angle', Number(e.target.value))} />
          </label>
        </div>
      )}

      {kind === 'image' && (
        <div className="wk-bg__stack">
          <div className="wk-bg__row">
            <input type="text" className="dk-input" aria-label={t('wellKnown.bg.imagePath')} data-testid={tid('image')} placeholder="Pictures/sky.jpg" value={v.image || ''} maxLength={400} disabled={disabled} aria-invalid={imageBad ? 'true' : undefined} onChange={e => set('image', e.target.value)} />
            <button type="button" className="dk-btn dk-btn--sm dk-btn--secondary" disabled={disabled} data-testid={tid('browse')} onClick={() => setBrowsing(b => !b)}>
              <Icon name="folder" /> {t('wellKnown.bg.choose')}
            </button>
          </div>
          {imageBad && <p className="dk-field-error" data-testid={tid('image-error')}>{t('wellKnown.bg.imageBad')}</p>}
          {browsing && <ImagePicker onPick={(p) => { set('image', p); setBrowsing(false) }} onClose={() => setBrowsing(false)} />}
          <label className="wk-bg__small">{t('wellKnown.bg.fit')}
            <span className="dk-select-wrap">
              <select className="dk-select" value={v.fit || 'cover'} disabled={disabled} data-testid={tid('fit')} onChange={e => set('fit', e.target.value)}>
                <option value="cover">{t('wellKnown.bg.fits.cover')}</option>
                <option value="contain">{t('wellKnown.bg.fits.contain')}</option>
              </select>
            </span>
          </label>
        </div>
      )}

      {kind !== 'none' && (
        <div className="wk-bg__tune">
          <label className="wk-bg__small">{t('wellKnown.bg.opacity', { n: Math.round((v.opacity ?? 1) * 100) })}
            <input type="range" min="0" max="1" step="0.05" value={v.opacity ?? 1} disabled={disabled} data-testid={tid('opacity')} onChange={e => set('opacity', Number(e.target.value))} />
          </label>
          <label className="wk-bg__small">{t('wellKnown.bg.blur', { n: v.blur ?? 0 })}
            <input type="range" min="0" max="40" step="1" value={v.blur ?? 0} disabled={disabled} data-testid={tid('blur')} onChange={e => set('blur', Number(e.target.value))} />
          </label>
          <label className="wk-bg__small">{t('wellKnown.bg.overlay', { n: Math.round((v.overlay ?? veilDefault) * 100) })}
            <input type="range" min="0" max="1" step="0.05" value={v.overlay ?? veilDefault} disabled={disabled} data-testid={tid('overlay')} onChange={e => set('overlay', Number(e.target.value))} />
          </label>
        </div>
      )}

      {style && (
        <div className="wk-bg__preview" aria-hidden="true" data-testid={tid('preview')}>
          <div className="home-bg__art" style={style.art} />
          <div className="home-bg__scrim" style={style.scrim} />
        </div>
      )}

      {kind !== 'none' && (
        <div className="wk-actions">
          <button type="button" className="wk-linkbtn" disabled={disabled} data-testid={tid('clear')} onClick={clear}>
            <Icon name="close" /> {t('wellKnown.bg.clear')}
          </button>
        </div>
      )}
    </div>
  )
}
