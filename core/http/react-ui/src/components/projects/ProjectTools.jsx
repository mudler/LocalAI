/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button } from '../ui'
import { APPLIED_LISTS, addChip } from '../../utils/projects'

// One list of names as chips with an input to add more. It edits a copy: the
// caller saves it with the rest of the form.
function ChipList({ listKey, value, onChange }) {
  const { t } = useTranslation('projects')
  const id = useId()
  const [text, setText] = useState('')
  const add = () => {
    onChange(addChip(value, text))
    setText('')
  }
  return (
    <div className="pj-chiplist" data-testid={`project-defaults-${listKey}`}>
      {value.length > 0 && (
        <ul className="pj-chips">
          {value.map(name => (
            <li key={name} className="pj-chip">
              <span className="pj-chip__text">{name}</span>
              <button type="button" className="pj-chip__x" aria-label={t('tools.removeChip', { name })} onClick={() => onChange(value.filter(v => v !== name))}>
                <Icon name="close" />
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="pj-chipadd">
        <input
          id={id}
          className="dk-input"
          value={text}
          placeholder={t('tools.addPlaceholder')}
          aria-label={t('tools.addLabel', { list: t(`tools.${listKey}`) })}
          onChange={e => setText(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add() } }}
        />
        <Button size="sm" onClick={add} disabled={!text.trim()}>{t('tools.add')}</Button>
      </div>
    </div>
  )
}

// The note under a list that restricts what a new chat gets, by list.
const HINTS = { connectors: 'tools.connectorsHint', collections: 'tools.collectionsHint' }

// The five default lists as collapsed sections, for the project sheet.
// Connectors and collections say what a non-empty list does.
export function DefaultsEditor({ value, onChange }) {
  const { t } = useTranslation('projects')
  return (
    <div className="pj-defaults">
      {APPLIED_LISTS.map(key => {
        const list = value[key] || []
        return (
          <details key={key} className="pj-disclosure" data-testid={`project-defaults-section-${key}`}>
            <summary className="pj-disclosure__head">
              <Icon name="chevron-right" className="pj-disclosure__chev" />
              <span className="pj-disclosure__name">{t(`tools.${key}`)}</span>
              <span className="pj-disclosure__state">{list.length ? t('sheet.count', { count: list.length }) : t('tools.none')}</span>
            </summary>
            <div className="pj-disclosure__body">
              {HINTS[key] && <p className="pj-note">{t(HINTS[key])}</p>}
              <ChipList listKey={key} value={list} onChange={next => onChange({ ...value, [key]: next })} />
            </div>
          </details>
        )
      })}
    </div>
  )
}

// The Tools tab: what a new chat in the project starts with.
export default function ProjectTools({ project }) {
  const { t } = useTranslation('projects')
  const d = project.defaults || {}
  return (
    <div className="pj-stack" data-testid="project-tools">
      <section className="pj-section">
        <h2 className="pj-h2">{t('tools.title')}</h2>
        <p className="pj-note">{t('tools.note')}</p>
        <dl className="pj-facts">
          {APPLIED_LISTS.map(key => (
            <div key={key} className="pj-fact" data-testid={`project-tools-${key}`}>
              <dt>{t(`tools.${key}`)}</dt>
              <dd>
                {HINTS[key] && <p className="pj-note pj-fact__hint">{t(HINTS[key])}</p>}
                {(d[key] || []).length === 0
                  ? <span className="pj-muted">{t('tools.none')}</span>
                  : <ul className="pj-chips">{d[key].map(n => <li key={n} className="pj-chip"><span className="pj-chip__text">{n}</span></li>)}</ul>}
              </dd>
            </div>
          ))}
        </dl>
      </section>
    </div>
  )
}
