/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { folderLabel } from '../../hooks/useSharedPages'

const initials = (name) => String(name || '?').split(/\s+/).filter(Boolean).slice(0, 2).map(w => w[0].toUpperCase()).join('') || '?'

function kb(n) {
  if (!n) return '0 B'
  return n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KB`
}

// What changed between the version the viewer allowed and this one, from the
// sizes and lines the server kept: index.html and page.json.
function Changes({ src, t }) {
  const c = src.consent || {}
  const rows = [
    { file: 'index.html', before: c.size || 0, after: src.page.size || 0, lines: [c.lines || 0, src.lines || 0] },
    { file: 'page.json', before: c.meta_size || 0, after: src.page.meta_size || 0 },
  ].filter(r => r.before !== r.after || (r.lines && r.lines[0] !== r.lines[1]))
  return (
    <ul className="pg-trust__changes" data-testid="trust-changes">
      {rows.map(r => {
        const d = r.after - r.before
        return (
          <li key={r.file}>
            <code>{r.file}</code>
            <span className="pg-trust__delta">
              {kb(r.before)} → {kb(r.after)}
              <b className={d >= 0 ? 'is-add' : 'is-del'}>{d >= 0 ? '+' : '−'}{kb(Math.abs(d))}</b>
              {r.lines && r.lines[0] !== r.lines[1] && <span>{t('trust.changed.lines', { before: r.lines[0], after: r.lines[1] })}</span>}
            </span>
          </li>
        )
      })}
    </ul>
  )
}

// TrustSheet asks before a page from a shared folder runs (mockups 12, 13 and,
// on a phone, 31). It says who wrote the page, where it lives, what it can and
// cannot do, and that it acts as the viewer. Focus starts on Cancel, Escape
// cancels, Tab stays inside. On a narrow screen it is a bottom sheet.
//
//   src         the answer of GET /api/chats/shared-pages/{folder}/{name}
//   changed     the viewer allowed an earlier version: the sheet says what changed
//   onCancel, onAllowOnce, onAllow, onSource
//   covered     the source sheet is open over it: its keys belong to that sheet
export default function TrustSheet({ src, changed, busy, error, covered, onCancel, onAllowOnce, onAllow, onSource }) {
  const { t, i18n } = useTranslation('pages')
  const ref = useRef(null)
  const cancelRef = useRef(null)
  const closeRef = useRef(onCancel)
  closeRef.current = onCancel
  const coveredRef = useRef(covered)
  coveredRef.current = covered
  const p = src.page
  const author = p.author_name || p.author_id || t('trust.someone')
  const folder = folderLabel(p.folder)
  const pageFolder = `${folder}/pages/${p.name}`
  const when = (ts) => {
    try { return new Date(ts).toLocaleDateString(i18n.language, { day: 'numeric', month: 'long' }) } catch { return '' }
  }
  const changedAt = (() => {
    try { return new Date(p.updated_at).toLocaleString(i18n.language, { day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit' }) } catch { return '' }
  })()

  useEffect(() => {
    const opener = document.activeElement
    cancelRef.current?.focus()
    const dialog = ref.current
    const onKey = (e) => {
      if (coveredRef.current) return
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeRef.current(); return }
      if (e.key !== 'Tab' || !dialog) return
      const focusable = Array.from(dialog.querySelectorAll('button:not([disabled]), a[href]'))
      if (!focusable.length) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() } else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      if (opener && document.contains(opener)) opener.focus?.()
    }
  }, [])

  const title = changed ? t('trust.changed.title', { title: p.title }) : t('trust.title', { title: p.title })
  return createPortal(
    <div className="dk-veil pg-trust-veil" data-state="open" onMouseDown={() => { if (!busy) onCancel() }}>
      <div
        ref={ref} className="dk-dialog pg-trust" role="dialog" aria-modal="true" aria-labelledby="pg-trust-title" aria-describedby="pg-trust-desc"
        data-state="open" data-testid="trust-sheet" data-variant={changed ? 'changed' : 'first'} onMouseDown={e => e.stopPropagation()}
      >
        <span className="pg-trust__grip" aria-hidden="true" />
        <div className="dk-dialog-head">
          <div>
            <h2 className="dk-dialog-title pg-trust__title" id="pg-trust-title">{title}</h2>
            <p className="dk-dialog-desc" id="pg-trust-desc">{changed ? t('trust.changed.desc') : t('trust.desc')}</p>
          </div>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('trust.close')} onClick={onCancel} disabled={busy}>
            <Icon name="close" />
          </button>
        </div>
        <div className="dk-dialog-body pg-trust__body">
          <div className="pg-trust__who">
            <span className="pg-trust__avatar" aria-hidden="true">{initials(author)}</span>
            <div>
              <b>{changed ? t('trust.changed.who', { author, when: changedAt }) : t('trust.who', { author })}</b>
              <span>
                {changed && src.consent?.allowed_at
                  ? t('trust.changed.allowed', { date: when(src.consent.allowed_at) })
                  : <>{t('trust.whereLabel')} <code>{folder}</code>{t('trust.whereWhen', { when: changedAt })}</>}
              </span>
            </div>
          </div>

          {changed && (
            <>
              <h3 className="pg-trust__h">{t('trust.changed.what')}</h3>
              <Changes src={src} t={t} />
              <p className="pg-trust__same"><Icon name="info" /> {t('trust.changed.same')}</p>
            </>
          )}

          <h3 className="pg-trust__h">{changed ? t('trust.canStillTitle') : t('trust.canTitle')}</h3>
          <ul className="pg-trust__list">
            <li><Icon name="check-circle" className="tone-ok" /><span>{t('trust.items.files')}<code>{pageFolder}</code>{!p.writable && <small>{t('trust.items.readOnly')}</small>}</span></li>
            <li><Icon name="check-circle" className="tone-ok" /><span>{t('trust.items.chat')}<small>{t('trust.items.chatNote')}</small></span></li>
          </ul>
          {!changed && (
            <>
              <h3 className="pg-trust__h">{t('trust.cannotTitle')}</h3>
              <ul className="pg-trust__list">
                <li><Icon name="ban" /><span>{t('trust.items.noFiles')}</span></li>
                <li><Icon name="ban" /><span>{t('trust.items.noNet')}<small>{t('trust.items.noNetNote')}</small></span></li>
                <li><Icon name="ban" /><span>{t('trust.items.noTools')}</span></li>
              </ul>
            </>
          )}
          <p className="pg-trust__warn" role="note"><Icon name="warning" /> {changed ? t('trust.changed.warn', { author }) : t('trust.warn', { author })}</p>
          {error && <p className="pg-trust__error" role="alert"><Icon name="alert-circle" /> {error}</p>}
        </div>
        <div className="dk-dialog-foot pg-trust__foot">
          <button type="button" className="pg-trust__source" onClick={onSource} data-testid="trust-source">
            <Icon name="code" /> {changed ? t('trust.changed.view') : t('trust.viewSource')}
          </button>
          <span className="pg-trust__acts">
            <button ref={cancelRef} type="button" className="dk-btn dk-btn--secondary" onClick={onCancel} disabled={busy} data-testid="trust-cancel">{t('trust.cancel')}</button>
            <button type="button" className="dk-btn dk-btn--secondary" onClick={onAllowOnce} disabled={busy} data-testid="trust-allow-once">{t('trust.allowOnce')}</button>
            <button type="button" className="dk-btn dk-btn--primary" onClick={onAllow} disabled={busy} data-testid="trust-allow">{t('trust.allow')}</button>
          </span>
        </div>
      </div>
    </div>,
    document.body,
  )
}

// SourceSheet shows the text of a page's index.html, read-only, beside the
// sheet that asked for it. The text is shown as text, never run.
export function SourceSheet({ src, onClose }) {
  const { t } = useTranslation('pages')
  const ref = useRef(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  useEffect(() => {
    const opener = document.activeElement
    ref.current?.querySelector('button')?.focus()
    const onKey = (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeRef.current() } }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      if (opener && document.contains(opener)) opener.focus?.()
    }
  }, [])
  return createPortal(
    <div className="dk-sheet-veil pg-source-veil" onMouseDown={onClose}>
      <aside ref={ref} className="dk-sheet dk-sheet--wide pg-source" role="dialog" aria-modal="true" aria-labelledby="pg-source-title" data-testid="trust-source-sheet" onMouseDown={e => e.stopPropagation()}>
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-dialog-title" id="pg-source-title">{t('trust.sourceTitle', { title: src.page.title })}</h2>
            <p className="dk-dialog-desc"><code>{folderLabel(src.page.folder)}/pages/{src.page.name}/index.html</code> · {t('trust.sourceLines', { count: src.lines })}</p>
          </div>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('trust.close')} onClick={onClose}><Icon name="close" /></button>
        </div>
        <pre className="pg-source__code" tabIndex={0}>{src.html}</pre>
        <p className="pg-source__foot">{t('trust.sourceNote')}</p>
      </aside>
    </div>,
    document.body,
  )
}
