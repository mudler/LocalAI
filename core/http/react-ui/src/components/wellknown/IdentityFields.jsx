/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '../ui'
import { ImagePicker } from './BackgroundField'
import { AssistantAvatar } from './Identity'
import { avatarPath, templateProblem } from '../../utils/look'

// PlaceholderNote says what is wrong with the placeholders of a text, or
// lists the ones it may use.
function PlaceholderNote({ id, text, allowed, onInsert, disabled }) {
  const { t } = useTranslation('workspace')
  const problem = templateProblem(text, allowed)
  if (problem) {
    const msg = problem.unknown.length
      ? t('wellKnown.placeholders.unknown', { names: problem.unknown.join(', '), count: problem.unknown.length })
      : t('wellKnown.placeholders.brace')
    return <p className="dk-field-error" id={id} data-testid={`${id}-error`}>{msg}</p>
  }
  if (!allowed.length) return null
  return (
    <p className="wk-phs" id={id}>
      {allowed.map(p => (
        <button key={p} type="button" className="wk-ph" disabled={disabled} onClick={() => onInsert?.(p)} title={t('wellKnown.placeholders.insert')}>{p}</button>
      ))}
      <span>{t('wellKnown.placeholders.hint')}</span>
    </p>
  )
}

// TemplateField is a line of text that may use placeholders ({user.name},
// {time_of_day}, {date}). A chip puts a placeholder at the caret; a name that
// is not one of them, or a lone brace, shows at once and blocks the save.
export function TemplateField({ id, name, fieldKey, value, onChange, placeholders = [], maxLength, disabled, describedBy, sample }) {
  const ref = useRef(null)
  const text = typeof value === 'string' ? value : ''
  const insert = (p) => {
    const el = ref.current
    const at = el && typeof el.selectionStart === 'number' ? el.selectionStart : text.length
    onChange(text.slice(0, at) + p + text.slice(at))
    requestAnimationFrame(() => { el?.focus(); el?.setSelectionRange?.(at + p.length, at + p.length) })
  }
  return (
    <span className="wk-template">
      <input
        ref={ref} id={id} type="text" className="dk-input" value={text} maxLength={maxLength} disabled={disabled}
        placeholder={sample} aria-describedby={`${describedBy} ${id}-ph`}
        aria-invalid={templateProblem(text, placeholders) ? 'true' : undefined}
        data-testid={`wk-field-${name}-${fieldKey}`}
        onChange={e => onChange(e.target.value)}
      />
      <PlaceholderNote id={`${id}-ph`} text={text} allowed={placeholders} onInsert={insert} disabled={disabled} />
    </span>
  )
}

// AvatarField picks the picture of the assistant from the user's files, with
// the path rule of the backgrounds: relative, no .., a picture type, no SVG.
export function AvatarField({ id, name, value, onChange, disabled, describedBy, assistantName }) {
  const { t } = useTranslation('workspace')
  const [browsing, setBrowsing] = useState(false)
  const path = typeof value === 'string' ? value : ''
  const bad = !!path && !avatarPath(path)
  return (
    <div className="wk-avatarfield" id={id} aria-describedby={describedBy} data-testid={`wk-field-${name}-avatar`}>
      <div className="wk-avatarfield__row">
        <AssistantAvatar avatar={bad ? '' : path} name={assistantName || '?'} size="lg" testId="wk-avatar-preview" />
        <input
          type="text" className="dk-input" aria-label={t('wellKnown.avatar.path')} placeholder="Pictures/avatars/juniper.png"
          value={path} maxLength={400} disabled={disabled} aria-invalid={bad ? 'true' : undefined}
          data-testid="wk-avatar-path" onChange={e => onChange(e.target.value)}
        />
        <Button size="sm" icon="folder" disabled={disabled} onClick={() => setBrowsing(b => !b)} data-testid="wk-avatar-browse">{t('wellKnown.bg.choose')}</Button>
        {path && <Button size="sm" variant="ghost" icon="close" disabled={disabled} onClick={() => onChange('')} data-testid="wk-avatar-clear">{t('wellKnown.avatar.remove')}</Button>}
      </div>
      {bad && <p className="dk-field-error" data-testid="wk-avatar-error">{t('wellKnown.avatar.bad')}</p>}
      {browsing && <ImagePicker pre="wk-avatar" onPick={(p) => { onChange(p); setBrowsing(false) }} onClose={() => setBrowsing(false)} />}
    </div>
  )
}

// EmptyChatField edits the empty chat of persona.json: its headline, the line
// under it and whether the shortcuts show. Keys it does not show stay.
export function EmptyChatField({ id, name, value, onChange, properties = {}, disabled, describedBy }) {
  const { t } = useTranslation('workspace')
  const v = useMemo(() => (value && typeof value === 'object' && !Array.isArray(value) ? value : {}), [value])
  const set = (k, val) => {
    const next = { ...v, [k]: val }
    if (val === '' || val === undefined) delete next[k]
    onChange(next)
  }
  const ph = (k) => properties[k]?.['x-placeholders'] || []
  return (
    <div className="wk-emptychat" id={id} aria-describedby={describedBy} data-testid={`wk-field-${name}-emptyChat`}>
      <label className="wk-bg__small" htmlFor={`${id}-title`}>{t('wellKnown.emptyChat.title')}</label>
      <TemplateField id={`${id}-title`} name={name} fieldKey="emptyChat-title" value={v.title} placeholders={ph('title')} maxLength={properties.title?.maxLength} disabled={disabled} describedBy={describedBy} sample={t('wellKnown.emptyChat.titleSample')} onChange={(x) => set('title', x)} />
      <label className="wk-bg__small" htmlFor={`${id}-subtitle`}>{t('wellKnown.emptyChat.subtitle')}</label>
      <TemplateField id={`${id}-subtitle`} name={name} fieldKey="emptyChat-subtitle" value={v.subtitle} placeholders={ph('subtitle')} maxLength={properties.subtitle?.maxLength} disabled={disabled} describedBy={describedBy} sample={t('wellKnown.emptyChat.subtitleSample')} onChange={(x) => set('subtitle', x)} />
      <label className="wk-check">
        <input type="checkbox" className="dk-check" checked={v.showShortcuts !== false} disabled={disabled} data-testid="wk-emptychat-shortcuts" onChange={e => set('showShortcuts', e.target.checked ? undefined : false)} />
        <span>{t('wellKnown.emptyChat.showShortcuts')}</span>
      </label>
    </div>
  )
}
