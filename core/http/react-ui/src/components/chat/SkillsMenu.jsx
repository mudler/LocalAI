import { useState, useEffect, useRef, useId } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Switch } from '../ui'
import { SEARCH_MIN, fallbackNotice, filterSkills, skillCounts, skillKey, skillSource } from '../../utils/chatSkills'
import './skillsmenu.css'

// The composer chip that opens the skills this chat may use: one switch per
// skill (name, one line of description, the full text in the tooltip), the
// count, "Select all" and "Disable all", and a search field from nine skills
// on. While a search is on, the two actions work on the skills shown only.
// The server stores the skills that are off, so a skill added later is on.
// Nothing shows when there is no skill.
//
// Each row says where the skill comes from: Built-in, the add-on's title, or
// its layer. A built-in skill that an add-on replaces is listed dimmed, with
// who replaces it and no switch; one line under the title says when a
// built-in skill serves because the add-on's own skill is off or not running.
//
// skills: [{ id, name, description, enabled, layer, addon_title, fallback }]
// replaced: the built-in skills an add-on replaces now [{ name, shadowed_by }]
// onSet(ids: Set, on): switches those skills with one request
// error: the text of a refused change, which the hook has already reverted
export default function SkillsMenu({ skills, replaced = [], onSet, error, testId = 'chat-skills' }) {
  const { t } = useTranslation('chat')
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const ref = useRef(null)
  const buttonRef = useRef(null)
  const noteId = useId()

  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    const onKey = (e) => {
      if (e.key !== 'Escape') return
      setOpen(false)
      buttonRef.current?.focus()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (skills.length === 0) return null
  const { on, total } = skillCounts(skills)
  const searchable = total >= SEARCH_MIN
  const q = searchable ? query : ''
  const shown = filterSkills(skills, q)
  const filtering = q.trim() !== ''
  const shownIds = new Set(shown.map(skillKey))
  const allIds = new Set(skills.map(skillKey))
  const target = filtering ? shownIds : allIds
  const nothingToEnable = !skills.some((s) => target.has(skillKey(s)) && !s.enabled)
  const nothingToDisable = !skills.some((s) => target.has(skillKey(s)) && s.enabled)
  const fallback = fallbackNotice(skills)
  // The title of the add-on that replaces a built-in skill, from the add-on
  // skill of the same name in the list.
  const titleOf = (id, name) => skills.find((s) => s.layer === 'addon' && s.addon === id && s.name === name)?.addon_title || id
  const shownReplaced = filtering ? filterSkills(replaced, q) : replaced

  return (
    <div className="chat-mcp-dropdown chat-addons-dropdown" ref={ref} data-testid={testId}>
      <button
        ref={buttonRef}
        type="button"
        className={`btn btn-sm ${on > 0 ? 'btn-primary' : 'btn-secondary'}`}
        title={t('skills.title')}
        aria-haspopup="true"
        aria-expanded={open}
        data-testid={`${testId}-button`}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name="book" /> {t('skills.label')}
        {on > 0 && <span className="chat-mcp-badge" data-testid={`${testId}-count`}>{on}</span>}
      </button>
      {open && (
        <div className="chat-mcp-dropdown-menu sk-menu" role="group" aria-label={t('skills.title')} data-testid={`${testId}-menu`}>
          <div className="sk-head">
            <div className="sk-head__line">
              <span className="sk-head__title">{t('skills.heading')}</span>
              <span className="sk-head__count" aria-live="polite" data-testid={`${testId}-summary`}>{t('skills.count', { on, total })}</span>
            </div>
            <div className="sk-head__actions">
              <Button size="sm" variant="ghost" disabled={nothingToEnable} onClick={() => onSet(target, true)} data-testid={`${testId}-all`}>
                {t(filtering ? 'skills.selectShown' : 'skills.selectAll')}
              </Button>
              <Button size="sm" variant="ghost" disabled={nothingToDisable} onClick={() => onSet(target, false)} data-testid={`${testId}-none`}>
                {t(filtering ? 'skills.disableShown' : 'skills.disableAll')}
              </Button>
            </div>
            {searchable && (
              <div className="sk-search">
                <Icon name="search" className="sk-search__icon" />
                <input
                  type="search"
                  className="dk-input sk-search__input"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder={t('skills.search')}
                  aria-label={t('skills.search')}
                  data-testid={`${testId}-search`}
                />
              </div>
            )}
            <p className="sk-head__note" id={noteId}>{t('skills.later')}</p>
            {fallback && (
              <p className="sk-head__note sk-head__note--fallback" data-testid={`${testId}-fallback`}>
                {t('skills.fallback', { count: fallback.skills.length, skills: fallback.skills.join(', '), name: fallback.addons.join(', ') })}
              </p>
            )}
          </div>
          <div className="sk-list" data-testid={`${testId}-list`}>
            {shown.map((s) => <SkillRow key={skillKey(s)} skill={s} onSet={onSet} testId={testId} />)}
            {shownReplaced.map((s) => (
              <div key={`replaced:${s.name}`} className="sk-row" data-replaced="true" data-testid={`${testId}-replaced`} title={s.description || s.name}>
                <span className="sk-row__text">
                  <span className="sk-row__name">{s.name}</span>
                  <span className="sk-row__desc">{t('skills.replacedBy', { name: titleOf(s.shadowed_by, s.name) })}</span>
                </span>
              </div>
            ))}
            {shown.length === 0 && <p className="sk-empty">{t('skills.noMatch', { query: query.trim() })}</p>}
          </div>
          <div className="sk-status" role="alert" data-testid={`${testId}-error`}>
            {error && (
              <>
                <Icon name="warning" />
                <span title={error}>{t('skills.failed')}</span>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function SkillRow({ skill, onSet, testId }) {
  const { t } = useTranslation('chat')
  const nameId = useId()
  const descId = useId()
  const switchId = useId()
  const hasDesc = !!(skill.description || '').trim()
  const src = skillSource(skill)
  const source = src.kind === 'addon' ? t('skills.source.addon', { name: src.name }) : src.kind ? t(`skills.source.${src.kind}`, { defaultValue: '' }) : ''
  return (
    <div className="sk-row" data-testid={`${testId}-item`} data-on={skill.enabled ? 'true' : 'false'}>
      <label className="sk-row__text" htmlFor={switchId} title={hasDesc ? `${skill.name}: ${skill.description}` : skill.name}>
        <span className="sk-row__name" id={nameId}>{skill.name}</span>
        {hasDesc && <span className="sk-row__desc" id={descId}>{skill.description}</span>}
        {source && <span className="sk-row__src" data-testid={`${testId}-source`} data-kind={src.kind}>{source}</span>}
      </label>
      <Switch
        id={switchId}
        checked={skill.enabled}
        labelledBy={nameId}
        describedBy={hasDesc ? descId : undefined}
        onChange={(next) => onSet(new Set([skillKey(skill)]), next)}
      />
    </div>
  )
}
