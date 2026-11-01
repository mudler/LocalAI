/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { filesApi } from '../../utils/api'
import { wellKnownApi } from '../../hooks/useWellKnown'
import { changedKeys, newLabels } from '../../utils/changeNotes'
import { currentPages, refreshPages } from '../../hooks/usePages'
import { showChange, SHORTCUT_ADDED_EVENT } from './changeNote'
import './wellKnown.css'

// The toasts already shown, by change set, for the life of the tab: a note
// that mounts again (a re-render, a reopened chat) never toasts twice.
const toasted = new Set()

// refine reads the versions of the files whose note depends on what changed:
// user.json (only the hint record changed: no note, unless suggestions were
// switched off), shortcuts.json (the new label) and ui.json (the theme).
async function refine(notes, revs) {
  const out = []
  for (const n of notes) {
    const name = n.key === 'profile' ? 'user' : n.key === 'shortcuts' ? 'shortcuts' : n.key === 'ui' ? 'ui' : ''
    if (!name) { out.push(n); continue }
    let pair = null
    try {
      const { versions = [] } = await wellKnownApi.versions(name)
      const i = versions.findIndex(v => revs.includes(v.rev))
      if (i >= 0) pair = { after: versions[i].data, before: versions[i + 1]?.data || null }
    } catch { /* the plain note stays */ }
    if (!pair) { out.push(n); continue }
    if (name === 'user') {
      const keys = changedKeys(pair.before, pair.after)
      if (keys.length === 1 && keys[0] === 'seen') {
        if (pair.after?.seen?.offers_off === true && pair.before?.seen?.offers_off !== true) out.push({ key: 'offersOff', action: 'turnBackOn' })
        continue
      }
      out.push(n)
    } else if (name === 'shortcuts') {
      const labels = newLabels(pair.before, pair.after)
      out.push({ ...n, label: labels[labels.length - 1] || '', toast: labels.length ? 'shortcut' : '' })
    } else {
      out.push({ ...n, toast: changedKeys(pair.before, pair.after).includes('theme') ? 'ui' : '' })
    }
  }
  return out
}

const ACTION_TO = {
  account: '/app/account?tab=about',
  turnBackOn: '/app/account?tab=about#help',
  appearance: '/app/account?tab=appearance',
  home: '/app',
}

// WorkspaceChangeNote is the line under an answer that says what the turn
// changed in the user's well-known files or pages, with Undo (the change sets
// of the turn, newest first) and where to see it. live is true for a turn that
// ended while this tab watched: only then does a change that shows outside
// the transcript (the composer, the sidebar, Home, the look) raise a toast,
// whose action is the same as the one on this line.
export default function WorkspaceChangeNote({ desc, live, addToast, pageTitle }) {
  const { t } = useTranslation('workspace')
  const navigate = useNavigate()
  const [notes, setNotes] = useState(null)
  const [undo, setUndo] = useState({ state: 'idle', message: '' })
  const revsKey = desc.revs.join(',')
  const undoRef = useRef(null)

  useEffect(() => {
    let alive = true
    refine(desc.notes, desc.revs).then((n) => { if (alive) setNotes(n) })
    return () => { alive = false }
  // revsKey names the change sets: the notes of the same changes are the same.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [revsKey])

  const runUndo = useCallback(async () => {
    setUndo({ state: 'busy', message: '' })
    try {
      for (const rev of desc.revs) await filesApi.undo(rev)
      setUndo({ state: 'done', message: '' })
    } catch (e) {
      setUndo({ state: 'failed', message: e?.message || '' })
    }
  }, [desc.revs])
  undoRef.current = runUndo

  useEffect(() => {
    if (!live || !notes || toasted.has(revsKey)) return
    toasted.add(revsKey)
    for (const n of notes) {
      if (n.toast === 'shortcut' && n.label) {
        window.dispatchEvent(new CustomEvent(SHORTCUT_ADDED_EVENT, { detail: { label: n.label } }))
        showChange(addToast, {
          message: t('discovery.toast.shortcutAdded', { label: n.label }), testId: 'change-toast-shortcut',
          action: { label: t('discovery.action.undo'), onClick: () => undoRef.current?.() },
        })
      } else if (n.toast === 'page') {
        // The sidebar learns of the page from the same read, so the toast and
        // the Pages entry show together, with the page's own title.
        refreshPages().catch(() => {}).then(() => {
          const title = currentPages().find(p => p.name === n.page)?.title || pageTitle(n.page)
          showChange(addToast, {
            message: t('discovery.toast.pageCreated', { title }), testId: 'change-toast-page',
            action: { label: t('discovery.toast.open'), onClick: () => navigate(`/app/pages/${encodeURIComponent(n.page)}`) },
          })
        })
      } else if (n.toast === 'home') {
        showChange(addToast, {
          message: t('discovery.toast.homeChanged'), testId: 'change-toast-home',
          action: { label: t('discovery.action.openHome'), onClick: () => navigate('/app') },
        })
      } else if (n.toast === 'ui') {
        showChange(addToast, {
          message: t('discovery.toast.themeChanged'), testId: 'change-toast-ui',
          action: { label: t('discovery.action.undo'), onClick: () => undoRef.current?.() },
        })
      }
    }
  }, [live, notes, revsKey, addToast, t, navigate, pageTitle])

  if (!notes || notes.length === 0) return null
  const code = <code />
  return (
    <div className="cx-change wk-change" data-testid="workspace-change" role="note">
      <Icon name="check-circle" className="wk-change__icon" />
      <span className="wk-change__text">
        {notes.map((n, i) => (
          <span key={`${n.key}-${n.page || i}`} data-note={n.key}>
            {i > 0 ? ' ' : ''}
            <Trans
              t={t} i18nKey={`discovery.change.${n.key}`} components={{ code }}
              values={{ title: n.page ? pageTitle(n.page) : '', folder: n.page ? `pages/${n.page}/` : '', file: n.file || '' }}
            />
          </span>
        ))}
      </span>
      <span className="wk-change__acts">
        {undo.state === 'idle' && (
          <button type="button" className="cx-link" onClick={runUndo} data-testid="workspace-change-undo">{t('discovery.action.undo')}</button>
        )}
        {undo.state === 'busy' && <span role="status">{t('discovery.change.undoing')}</span>}
        {undo.state === 'done' && <span role="status" data-testid="workspace-change-undone">{t('discovery.change.undone')}</span>}
        {undo.state === 'failed' && <span role="status" data-testid="workspace-change-undone">{t('discovery.change.undoFailed', { message: undo.message })}</span>}
        {notes.map((n, i) => {
          if (n.action === 'page') {
            return (
              <button key={`a-${i}`} type="button" className="cx-link" onClick={() => navigate(`/app/pages/${encodeURIComponent(n.page)}`)} data-testid="workspace-change-open">
                {t('discovery.action.openPage')}
              </button>
            )
          }
          if (!ACTION_TO[n.action]) return null
          const label = n.action === 'home' ? 'openHome' : n.action === 'turnBackOn' ? 'turnBackOn' : 'openAccount'
          return (
            <button key={`a-${i}`} type="button" className="cx-link" onClick={() => navigate(ACTION_TO[n.action])} data-testid="workspace-change-open">
              {t(`discovery.action.${label}`)}
            </button>
          )
        }).filter(Boolean).slice(0, 2)}
      </span>
    </div>
  )
}
