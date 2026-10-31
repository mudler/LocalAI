/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Popover from '../Popover'
import { wellKnownApi } from '../../hooks/useWellKnown'
import './wellKnown.css'

function when(at, locale) {
  const d = new Date(at)
  if (Number.isNaN(d.getTime())) return ''
  const today = new Date()
  const sameDay = d.toDateString() === today.toDateString()
  try {
    return sameDay
      ? d.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })
      : d.toLocaleString(locale, { day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit' })
  } catch {
    return d.toISOString()
  }
}

// Versions is the "Versions" link of a settings card: the last versions of the
// file with who wrote each and where, and Revert on the one picked. A revert
// is a new version, so it can be reverted too.
//
//   name       the well-known file (persona, user, ui, ...)
//   onReverted (doc) after a revert, with the file as written
export default function Versions({ name, onReverted, disabled }) {
  const { t, i18n } = useTranslation('workspace')
  const anchor = useRef(null)
  const [open, setOpen] = useState(false)
  const [list, setList] = useState(null)
  const [error, setError] = useState('')
  const [picked, setPicked] = useState(null)
  const [busy, setBusy] = useState(false)

  const show = async () => {
    const next = !open
    setOpen(next)
    if (!next) return
    setError('')
    setPicked(null)
    try {
      const { versions = [] } = await wellKnownApi.versions(name)
      setList(versions)
    } catch (e) {
      setError(e.message)
      setList([])
    }
  }
  const revert = async () => {
    if (!picked) return
    setBusy(true)
    try {
      const doc = await wellKnownApi.revertTo(name, picked)
      setOpen(false)
      onReverted?.(doc)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }
  const who = (v) => {
    if (v.kind === 'agent') return v.chat_title ? t('discovery.versions.byAssistantIn', { chat: v.chat_title }) : t('discovery.versions.byAssistant')
    if (v.kind === 'undo') return t('discovery.versions.undo')
    if (v.kind === 'upload') return t('discovery.versions.upload')
    return t('discovery.versions.byYou')
  }

  return (
    <>
      <button
        ref={anchor} type="button" className="wk-linkbtn" onClick={show} aria-expanded={open} disabled={disabled}
        data-testid={`wk-versions-${name}`}
      >
        <Icon name="history" /> {t('discovery.versions.label')}
      </button>
      <Popover anchor={anchor} open={open} onClose={() => setOpen(false)} ariaLabel={t('discovery.versions.title')}>
        <div className="wk-versions" data-testid={`wk-versions-list-${name}`}>
          <p className="wk-versions__title">{t('discovery.versions.title')}</p>
          {list === null && <p className="wk-versions__empty" role="status">{t('discovery.versions.loading')}</p>}
          {list && list.length === 0 && !error && <p className="wk-versions__empty">{t('discovery.versions.none')}</p>}
          {list && list.length > 0 && (
            <ul className="wk-versions__list" role="radiogroup" aria-label={t('discovery.versions.title')}>
              {list.map((v, i) => {
                const current = i === 0
                const can = !current && v.valid
                return (
                  <li key={v.rev} className={`wk-versions__row${picked === v.rev ? ' wk-versions__row--picked' : ''}`}>
                    <label className="wk-versions__pick">
                      <input
                        type="radio" name={`wk-versions-${name}`} checked={picked === v.rev || (current && picked === null)}
                        disabled={!can && !current} onChange={() => setPicked(can ? v.rev : null)}
                      />
                      <span className="wk-versions__text">
                        <span className="wk-versions__when">{when(v.at, i18n.language)}</span>
                        <span className="wk-versions__who">
                          {who(v)}
                          {current ? ` · ${t('discovery.versions.current')}` : ''}
                          {!v.valid ? ` · ${t('discovery.versions.invalid')}` : ''}
                        </span>
                      </span>
                    </label>
                    {picked === v.rev && (
                      <button type="button" className="dk-btn dk-btn--sm dk-btn--primary" onClick={revert} disabled={busy} data-testid="wk-versions-revert">
                        {t('discovery.versions.revert')}
                      </button>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
          {error && <p className="wk-note wk-note--error" role="alert">{error}</p>}
          <p className="wk-versions__note">{t('discovery.versions.note')}</p>
        </div>
      </Popover>
    </>
  )
}
