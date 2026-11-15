/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Fragment, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button } from '../ui'
import { PULLED_MAX, composerProjects, withPulled } from '../../utils/projects'
import { composerShared } from '../../utils/sharedProjects'
import './projectchat.css'

// The project a new chat starts in, as a chip of the composer next to the
// model and Tools. The list opens under the chip: a search box, "No project",
// the active projects with their chat counts, and a link to the Projects page.
// Archived projects are not offered. The pick is the draft's: it goes out as
// project_id when the chat is created and is forgotten with the draft.
// When projects are off or cannot be read the chip is not drawn.
//
//   list       the result of useProjectList (the composer holds it, so the
//              note under the composer reads the same projects)
//   value      the picked project id, or ''
//   onChange   (id) => void; '' means no project
//   pulled     the refs of shared projects the new chat pulls in (optional)
//   onPulled   (refs) => void; with pulled, the list also offers the projects
//              shared with the person under "Pull in". They are
//              never a home project: choosing one only pulls it in.
export default function ProjectPicker({ list, value, onChange, pulled, onPulled }) {
  const { t } = useTranslation('projects')
  const { projects, status, reload, shared } = list
  const pullable = Boolean(onPulled)
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const rootRef = useRef(null)
  const chipRef = useRef(null)
  const filterRef = useRef(null)
  const listId = useId()

  const shown = useMemo(() => composerProjects(projects, query), [projects, query])
  const sharedShown = useMemo(() => (pullable ? composerShared(shared, query) : []), [pullable, shared, query])
  // Row 0 is "No project" while the search is empty; then the projects the chat
  // can start in; then the shared ones it can only pull in.
  const rows = useMemo(() => [
    ...(query.trim() ? [] : [{ id: '' }]),
    ...shown,
    ...sharedShown.map(p => ({ id: p.ref, shared: p })),
  ], [shown, sharedShown, query])

  useEffect(() => {
    if (!open) return undefined
    filterRef.current?.focus({ preventScroll: true })
    const onDown = (e) => { if (rootRef.current && !rootRef.current.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  if (status !== 'ready' || !projects) return null

  const picked = projects.find(p => p.id === value)
  const close = (refocus) => { setOpen(false); setQuery(''); setActive(0); if (refocus) chipRef.current?.focus() }
  const choose = (row) => {
    if (row.shared) { onPulled(withPulled(pulled || [], row.id, !(pulled || []).includes(row.id))); return }
    onChange(row.id)
    close(true)
  }
  const pulledCount = (pulled || []).filter(ref => (shared || []).some(p => p.ref === ref)).length
  const openMenu = () => { reload(); setOpen(true) }
  const onKey = (e) => {
    if (e.key === 'Escape') { e.preventDefault(); close(true) }
    else if (e.key === 'ArrowDown') { e.preventDefault(); setActive(i => Math.min(i + 1, rows.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive(i => Math.max(i - 1, 0)) }
    else if (e.key === 'Enter' && rows[active]) { e.preventDefault(); choose(rows[active]) }
  }
  const optionId = (i) => `${listId}-opt-${i}`

  return (
    <div className="pjc-pick" ref={rootRef} data-testid="project-picker">
      <button
        ref={chipRef}
        type="button"
        className="home-chip"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        title={t('composer.title')}
        data-picked={picked || pulledCount > 0 ? 'true' : undefined}
        data-testid="project-picker-button"
        onClick={() => (open ? close(false) : openMenu())}
        onKeyDown={(e) => { if (e.key === 'ArrowDown' && !open) { e.preventDefault(); openMenu() } }}
      >
        <Icon name="layers" />
        <span className="home-chip__text">{picked ? (pulledCount > 0 ? t('composer.plusPulled', { name: picked.name, count: pulledCount }) : picked.name) : (pulledCount > 0 ? t('composer.pulledOnly', { count: pulledCount }) : t('composer.label'))}</span>
        <Icon name="chevron-down" className="home-chip__caret" />
      </button>
      {open && (
        <div className="home-menu pjc-menu" onKeyDown={onKey} data-testid="project-picker-menu">
          <input
            ref={filterRef}
            type="search"
            className="home-menu__filter"
            placeholder={t('composer.search')}
            aria-label={t('composer.search')}
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={rows[active] ? optionId(active) : undefined}
            value={query}
            onChange={(e) => { setQuery(e.target.value); setActive(0) }}
          />
          <ul id={listId} className="home-menu__list" role="listbox" aria-label={t('composer.label')}>
            <li className="home-menu__head" role="presentation">{t('composer.heading')}</li>
            {rows.length === 0 && <li className="home-menu__empty" role="presentation">{t('composer.empty')}</li>}
            {rows.map((row, i) => {
              const none = row.id === ''
              const isShared = Boolean(row.shared)
              const chosen = isShared ? (pulled || []).includes(row.id) : none ? !picked : row.id === value
              const full = isShared && !chosen && (pulled || []).length >= PULLED_MAX
              return (
                <Fragment key={row.id || 'none'}>
                  {isShared && (i === 0 || !rows[i - 1].shared) && <li className="home-menu__head" role="presentation" data-testid="project-picker-shared-head">{t('composer.pullHeading')}</li>}
                  <li
                    id={optionId(i)}
                    role="option"
                    aria-selected={chosen}
                    aria-disabled={full || undefined}
                    data-active={i === active}
                    data-shared={isShared ? 'true' : undefined}
                    className="home-menu__item pjc-item"
                    onMouseMove={() => setActive(i)}
                    onClick={() => { if (!full) choose(row) }}
                  >
                    <Icon name={none ? 'close-circle' : isShared ? 'users' : 'layers'} className="pjc-item__icon" />
                    <span className="pjc-item__text">
                      <b className="pjc-item__title">
                        {none ? t('composer.none') : isShared ? row.shared.name : row.name}
                        {isShared && <span className="pjc-badge">{t('share.badge')}</span>}
                      </b>
                      <small>{none ? t('composer.noneNote') : isShared ? t('share.byAccess', { owner: row.shared.owner_name, access: t(row.shared.mode === 'write' ? 'share.canEdit' : 'share.readOnly') }) : t('row.chats', { count: row.chat_count || 0 })}</small>
                    </span>
                    {chosen && <Icon name="check" className="home-menu__check" />}
                  </li>
                </Fragment>
              )
            })}
            <li className="home-menu__head" role="presentation">{t('composer.archivedHidden')}</li>
          </ul>
          <div className="home-menu__foot pjc-foot">
            <Button size="sm" variant="ghost" iconEnd="arrow-right" to="/app/projects" onClick={() => close(false)}>{t('composer.manage')}</Button>
          </div>
        </div>
      )}
    </div>
  )
}
