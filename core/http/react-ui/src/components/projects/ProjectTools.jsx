/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import { chatProjectsApi } from '../../utils/api'
import { useProjectToolSources } from '../../hooks/useProjectToolSources'
import { APPLIED_LISTS, addChip, cleanDefaults, entryState, pickerChoices, unavailableCount } from '../../utils/projects'
import { labelOf } from '../../utils/projectToolSources'

// One entry of a list as a chip. A name that the source of its kind does not
// list says so on the chip and in its tooltip: it does nothing until it exists.
function EntryChip({ listKey, value, options, ready, onRemove, readOnly }) {
  const { t } = useTranslation('projects')
  const state = entryState(options, ready, value)
  const label = labelOf(options, value)
  const flagged = state === 'missing' || state === 'off'
  const reason = options.find(o => o.value === value)?.reason
  const title = state === 'missing' ? t('tools.state.missingTitle', { name: value }) : state === 'off' ? (reason || t('tools.state.offTitle', { name: value })) : label !== value ? value : undefined
  return (
    <li className={`pj-chip${flagged ? ' pj-chip--warn' : ''}`} data-testid="project-default-entry" data-entry={value} data-state={state} title={title}>
      {flagged && <Icon name="alert-circle" className="pj-chip__warn" />}
      <span className="pj-chip__text">{label}</span>
      {flagged && <span className="pj-chip__state">{t(`tools.state.${state}`)}</span>}
      {!readOnly && (
        <button type="button" className="pj-chip__x" aria-label={t('tools.removeChip', { name: label })} onClick={onRemove}>
          <Icon name="close" />
        </button>
      )}
    </li>
  )
}

// One list: its chips, a field that filters what is installed, the installed
// choices still to add, and a freeform add for a name that is not installed
// (it is kept and flagged). It edits a copy; the caller saves it.
function ListPicker({ listKey, value, onChange, source, hintId, readOnly }) {
  const { t } = useTranslation('projects')
  const id = useId()
  const noteId = `${id}-note`
  const [text, setText] = useState('')
  const { list: choices, freeform } = pickerChoices(source.options, value, text)
  const add = (v) => { onChange(addChip(value, v)); setText('') }
  const onKey = (e) => {
    if (e.key !== 'Enter') return
    e.preventDefault()
    if (!text.trim()) return
    const hit = exactChoice(source.options, value, text)
    if (hit) add(hit)
    else if (choices.length === 1) add(choices[0].value)
    else if (freeform) add(freeform)
  }
  const status = !source.ready ? 'loading' : source.options.length === 0 ? 'none' : ''
  return (
    <div className="pj-chiplist" data-testid={`project-defaults-${listKey}`}>
      {value.length > 0 && (
        <ul className="pj-chips" aria-label={t(`tools.${listKey}`)}>
          {value.map(name => (
            <EntryChip key={name} listKey={listKey} value={name} options={source.options} ready={source.ready} readOnly={readOnly} onRemove={() => onChange(value.filter(v => v !== name))} />
          ))}
        </ul>
      )}
      {!readOnly && (
        <>
          <div className="pj-chipadd">
            <input
              id={id}
              className="dk-input"
              value={text}
              placeholder={t('tools.addPlaceholder')}
              aria-label={t('tools.addLabel', { list: t(`tools.${listKey}`) })}
              aria-describedby={`${hintId} ${noteId}`}
              autoComplete="off"
              onChange={e => setText(e.target.value)}
              onKeyDown={onKey}
            />
            <Button size="sm" onClick={() => add(exactChoice(source.options, value, text) || freeform || (choices.length === 1 ? choices[0].value : ''))} disabled={!text.trim() || (!freeform && choices.length === 0)}>{t('tools.add')}</Button>
          </div>
          <p id={noteId} className="pj-note" data-testid={`project-pick-${listKey}-status`}>
            {status === 'loading' ? t('tools.pickerLoading') : status === 'none' ? t('tools.pickerNone') : (choices.length ? t('tools.pickerLead') : '')}
          </p>
          {choices.length > 0 && (
            <ul className="pj-pick" aria-label={t('tools.pickerLabel', { list: t(`tools.${listKey}`) })} data-testid={`project-pick-${listKey}`}>
              {choices.map(o => (
                <li key={o.value}>
                  <button type="button" className="pj-pick__opt" data-testid="project-pick-option" data-value={o.value} title={o.detail || undefined} onClick={() => add(o.value)}>
                    <Icon name="plus" />
                    <span className="pj-pick__label">{o.label}</span>
                    {o.detail && <small className="pj-pick__detail">{o.detail}</small>}
                    {o.usable === false && <small className="pj-pick__detail">{t('tools.state.off')}</small>}
                  </button>
                </li>
              ))}
            </ul>
          )}
          {freeform && (
            <button type="button" className="pj-pick__free" data-testid="project-pick-free" onClick={() => add(freeform)}>
              <Icon name="plus" />{t(source.ready ? 'tools.addNotInstalled' : 'tools.addTyped', { name: freeform })}
            </button>
          )}
        </>
      )}
    </div>
  )
}

function exactChoice(options, chosen, text) {
  const q = (text || '').trim().toLowerCase()
  return q ? options.find(o => !chosen.includes(o.value) && (o.value.toLowerCase() === q || o.label.toLowerCase() === q))?.value : ''
}

// The note under each list. Every list says what a non-empty list does.
const HINT_KEY = (key) => `tools.${key}Hint`

// The five default lists as collapsed sections in one group, with one
// legend. Each list's hint is tied to its input with aria-describedby. Used by
// the project sheet and the Tool defaults tab, so both say the same words.
export function DefaultsEditor({ value, onChange, sources, readOnly = false, showLead = true }) {
  const { t } = useTranslation('projects')
  const uid = useId()
  return (
    <fieldset className="pj-defaults pj-fieldset" data-testid="project-defaults">
      <legend className="pj-eyebrow">{t('tools.title')}</legend>
      {showLead && <p className="pj-note" id={`${uid}-lead`} data-testid="project-defaults-lead">{t('tools.lead')}</p>}
      {APPLIED_LISTS.map(key => {
        const list = value[key] || []
        const src = sources[key]
        const missing = unavailableCount(src.options, src.ready, list)
        const hintId = `${uid}-${key}-hint`
        return (
          <details key={key} className="pj-disclosure" data-testid={`project-defaults-section-${key}`}>
            <summary className="pj-disclosure__head">
              <Icon name="chevron-right" className="pj-disclosure__chev" />
              <span className="pj-disclosure__name">{t(`tools.${key}`)}</span>
              {missing > 0 && <span className="pj-disclosure__warn" data-testid={`project-defaults-warn-${key}`}><Icon name="alert-circle" />{t('tools.countMissing', { n: missing })}</span>}
              <span className="pj-disclosure__state">{list.length ? t('sheet.count', { count: list.length }) : t('tools.none')}</span>
            </summary>
            <div className="pj-disclosure__body">
              <p className="pj-note" id={hintId}>{t(HINT_KEY(key))}</p>
              <ListPicker listKey={key} value={list} source={src} hintId={hintId} readOnly={readOnly} onChange={next => onChange({ ...value, [key]: next })} />
            </div>
          </details>
        )
      })}
    </fieldset>
  )
}

// The Tool defaults tab: the same editor as the project sheet, with its own Save. A
// project that is archived is read-only here, as Edit is.
export default function ProjectTools({ project, archived = false, addToast, onSaved }) {
  const { t } = useTranslation('projects')
  const sources = useProjectToolSources()
  const saved = useMemo(() => cleanDefaults(project.defaults), [project.defaults])
  const [draft, setDraft] = useState(() => ({ ...(project.defaults || {}) }))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dirty = JSON.stringify(cleanDefaults(draft)) !== JSON.stringify(saved)

  const save = async () => {
    if (busy || !dirty) return
    setBusy(true)
    setError('')
    try {
      const p = await chatProjectsApi.update(project.id, { defaults: cleanDefaults(draft) })
      onSaved?.(p)
      addToast?.(t('toasts.saved', { name: project.name }), 'success')
    } catch (err) {
      setError(err.message || String(err))
    } finally {
      setBusy(false)
    }
  }
  const discard = () => { setDraft({ ...(project.defaults || {}) }); setError('') }

  return (
    <div className="pj-stack" data-testid="project-tools">
      <section className="pj-section">
        {archived && <Callout tone="info" testId="project-tools-archived">{t('tools.archivedNote')}</Callout>}
        <DefaultsEditor value={draft} onChange={setDraft} sources={sources} readOnly={archived} />
        {error && <p className="dk-field-error" role="alert" data-testid="project-tools-error">{error}</p>}
        {!archived && (
          <div className="pj-savebar">
            <Button variant="primary" busy={busy} disabled={!dirty} onClick={save} data-testid="project-tools-save">{t('tools.save')}</Button>
            <Button variant="ghost" disabled={!dirty || busy} onClick={discard} data-testid="project-tools-discard">{t('tools.discard')}</Button>
          </div>
        )}
      </section>
    </div>
  )
}
