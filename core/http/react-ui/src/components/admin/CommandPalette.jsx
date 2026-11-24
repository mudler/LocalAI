import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { useAuth } from '../../context/AuthContext'
import { adminUsersApi, nodesApi } from '../../utils/api'
import { FIELDS, GROUPS } from '../../utils/settingsSchema'
import { operateHub, resolveGroups } from '../hub/hubConfig'
import { useHubAuth } from '../hub/useHubAuth'
import { useLocation } from 'react-router-dom'
import { OPEN_PALETTE_EVENT } from './adminEvents'
import { KINDS, isApplePlatform, pageEntries, personEntries, rank, settingEntries, viewAsEntries, workerEntries } from './paletteModel'
import { useViewAs } from '../../contexts/ViewAsContext'
import './admin.css'

// Search for the Administration pages, and for the people, workers and
// settings that the client can already list. Ctrl or Cmd K opens it on any
// Administration page; the Search button of the sidebar does the same.
//
// A modal listbox: the input keeps the focus (the options are reached with the
// arrow keys and named by aria-activedescendant), Tab stays in the dialog,
// Escape or a click outside closes it, and the focus goes back to where it was.
export default function CommandPalette() {
  const { t } = useTranslation('nav')
  const navigate = useNavigate()
  const location = useLocation()
  const hubAuth = useHubAuth()
  const { authEnabled, isAdmin, user: viewer } = useAuth()
  const { t: tv } = useTranslation('viewas')
  const viewAs = useViewAs()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const [people, setPeople] = useState([])
  const [workers, setWorkers] = useState([])
  const returnTo = useRef(null)
  const inputRef = useRef(null)
  const listRef = useRef(null)
  const base = useId()

  const { groups } = resolveGroups(operateHub, hubAuth, location)
  const root = t('sections.operate')
  const pages = useMemo(() => pageEntries(groups, t), [groups, t])
  const settings = useMemo(() => settingEntries(FIELDS, GROUPS, root), [root])
  const entries = useMemo(() => [
    ...pages,
    ...settings,
    ...workerEntries(workers, `${root} / ${t('admin.groups.cluster')}`),
    ...(viewAs.canViewAs ? viewAsEntries(people, viewer, {
      kind: tv('palette.kind'),
      title: (name) => tv('palette.title', { name }),
      where: (email) => tv('palette.where', { email }),
    }) : []),
    ...personEntries(people, `${root} / ${t('admin.groups.access')}`),
  ], [pages, settings, workers, people, root, t, tv, viewAs.canViewAs, viewer])
  const results = useMemo(() => rank(entries, query), [entries, query])

  const close = useCallback(() => {
    setOpen(false)
    setQuery('')
    setActive(0)
    const el = returnTo.current
    returnTo.current = null
    if (el && typeof el.focus === 'function') requestAnimationFrame(() => el.focus())
  }, [])

  const show = useCallback(() => {
    returnTo.current = document.activeElement
    setOpen(true)
  }, [])

  // Ctrl or Cmd K, and the sidebar's Search button.
  useEffect(() => {
    const onKey = (e) => {
      if ((e.key === 'k' || e.key === 'K') && (isApplePlatform() ? e.metaKey : e.ctrlKey) && !e.altKey && !e.shiftKey) {
        e.preventDefault()
        if (open) close()
        else show()
      }
    }
    window.addEventListener('keydown', onKey)
    window.addEventListener(OPEN_PALETTE_EVENT, show)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener(OPEN_PALETTE_EVENT, show)
    }
  }, [open, close, show])

  // The lists are read when the palette opens, only where the call exists for
  // this viewer: users with sign-in on, workers in a cluster.
  useEffect(() => {
    if (!open) return undefined
    let live = true
    if (authEnabled && isAdmin) {
      adminUsersApi.list().then(d => { if (live) setPeople(Array.isArray(d) ? d : d?.users || []) }).catch(() => {})
    }
    if (hubAuth.features.distributed === true) {
      nodesApi.list().then(d => { if (live) setWorkers(Array.isArray(d) ? d : d?.nodes || []) }).catch(() => {})
    }
    return () => { live = false }
  }, [open, authEnabled, isAdmin, hubAuth.features.distributed])

  useEffect(() => { if (open) inputRef.current?.focus() }, [open])
  useEffect(() => { setActive(0) }, [query])
  useEffect(() => {
    if (!open) return
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [active, open])
  // A route change (a result was taken, or the back button) closes it.
  useEffect(() => { setOpen(false); setQuery('') }, [location.pathname, location.search])

  if (!open) return null

  const choose = (entry) => {
    if (!entry) return
    setOpen(false)
    setQuery('')
    returnTo.current = null
    // View as starts here and lands on Projects, the page that follows it.
    if (entry.viewAs) { viewAs.enter(entry.viewAs); navigate('/app/projects'); return }
    navigate(entry.to)
  }

  const onKeyDown = (e) => {
    if (e.key === 'Escape') { e.preventDefault(); close() }
    else if (e.key === 'ArrowDown') { e.preventDefault(); setActive(i => (results.length ? (i + 1) % results.length : 0)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive(i => (results.length ? (i + results.length - 1) % results.length : 0)) }
    else if (e.key === 'Home' && e.target !== inputRef.current) { e.preventDefault(); setActive(0) }
    else if (e.key === 'Enter') { e.preventDefault(); choose(results[active]) }
    // The input is the only control: Tab would leave the dialog.
    else if (e.key === 'Tab') { e.preventDefault(); inputRef.current?.focus() }
  }

  const listId = `${base}-list`
  const optionId = (entry) => `${base}-${entry.id.replace(/[^a-z0-9]+/gi, '-')}`
  let index = -1
  return createPortal(
    <div className="palette-backdrop" onMouseDown={(e) => { if (e.target === e.currentTarget) close() }}>
      <div className="palette" role="dialog" aria-modal="true" aria-label={t('admin.palette.label')} onKeyDown={onKeyDown} data-testid="admin-palette">
        <div className="palette__bar">
          <Icon name="search" aria-hidden="true" />
          <input
            ref={inputRef}
            className="palette__input"
            type="text"
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={results[active] ? optionId(results[active]) : undefined}
            aria-label={t('admin.palette.input')}
            placeholder={t('admin.palette.placeholder')}
            autoComplete="off"
            spellCheck={false}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <kbd className="palette__esc">Esc</kbd>
        </div>
        <div className="palette__list" id={listId} role="listbox" aria-label={t('admin.palette.results')} ref={listRef}>
          {results.length === 0 && <p className="palette__empty" role="status">{t('admin.palette.none', { query })}</p>}
          {KINDS.map(kind => {
            const rows = results.filter(r => r.kind === kind)
            if (rows.length === 0) return null
            return (
              <div key={kind} role="group" aria-label={kind === 'viewas' ? tv('palette.kind') : t(`admin.palette.kinds.${kind}`)}>
                <p className="palette__kind" aria-hidden="true">{kind === 'viewas' ? tv('palette.kind') : t(`admin.palette.kinds.${kind}`)}</p>
                {rows.map(entry => {
                  index += 1
                  const selected = index === active
                  const at = index
                  return (
                    <div
                      key={entry.id}
                      id={optionId(entry)}
                      role="option"
                      aria-selected={selected}
                      className="palette__row"
                      data-kind={kind}
                      onMouseMove={() => { if (active !== at) setActive(at) }}
                      onClick={() => choose(entry)}
                    >
                      <Icon name={entry.icon} aria-hidden="true" />
                      <span className="palette__words">
                        <span className="palette__title">{entry.title}</span>
                        <span className="palette__where">{entry.where}</span>
                      </span>
                      <Icon name="chevron-right" className="palette__go" aria-hidden="true" />
                    </div>
                  )
                })}
              </div>
            )
          })}
        </div>
        <p className="palette__foot">
          <kbd>&uarr;</kbd><kbd>&darr;</kbd> {t('admin.palette.move')} <kbd>Enter</kbd> {t('admin.palette.open')}
        </p>
      </div>
    </div>,
    document.body,
  )
}
