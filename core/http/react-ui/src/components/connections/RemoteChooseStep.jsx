import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import SearchField from '../ui/SearchField'
import { filterModels } from '../../utils/connections'

// How many models make the list long enough to need a search box.
const SEARCH_FROM = 12

// What a remote offers, as checkboxes with a chip for each use, and the
// button that creates the connection. Until the test has passed it says what
// to do first.
export default function RemoteChooseStep({ remote }) {
  const { t } = useTranslation('connections')
  const uid = useId()
  const [query, setQuery] = useState('')
  const { phase, models, ticked, createError } = remote

  if (phase !== 'ok') {
    return <p className="conn-hint" data-testid="remote-choose-empty">{t('choose.waiting')}</p>
  }

  const shown = filterModels(models, query)
  const shownIds = shown.map(m => m.id)
  const usecaseLabel = (u) => t(`usecase.${u}`, { defaultValue: u })
  const errorText = createError
    ? t(`choose.error.${createError.kind}`, { message: createError.message })
    : ''

  return (
    <div className="conn-choose" data-testid="remote-choose-step">
      {models.length >= SEARCH_FROM && (
        <SearchField value={query} onChange={setQuery} label={t('choose.search')} />
      )}
      <div className="conn-choose__bar">
        <span className="conn-count" aria-live="polite" data-testid="remote-count">
          {t('choose.count', { selected: ticked.length, total: models.length })}
        </span>
        <span className="conn-choose__all">
          <Button size="sm" variant="ghost" onClick={() => remote.setMany(shownIds, true)}>{t('choose.selectAll')}</Button>
          <Button size="sm" variant="ghost" onClick={() => remote.setMany(shownIds, false)}>{t('choose.selectNone')}</Button>
        </span>
      </div>
      <ul className="conn-models" role="group" aria-label={t('choose.listLabel')} data-testid="remote-models">
        {shown.map(m => (
          <li key={m.id}>
            <label className="conn-model">
              <input type="checkbox" className="dk-check" checked={ticked.includes(m.id)} onChange={() => remote.toggle(m.id)} />
              <span className="conn-model__id">{m.id}</span>
              <span className="conn-model__uses">
                {(m.usecases || []).map(u => <span key={u} className="dk-badge">{usecaseLabel(u)}</span>)}
              </span>
            </label>
          </li>
        ))}
        {shown.length === 0 && <li className="conn-hint">{t('choose.noMatch')}</li>}
      </ul>

      <div className="dk-field">
        <label className="dk-label" htmlFor={`${uid}-name`}>{t('choose.name')}</label>
        <input
          id={`${uid}-name`}
          className="dk-input"
          autoComplete="off"
          value={remote.nameValue}
          placeholder={remote.name}
          onChange={e => remote.setName(e.target.value)}
          aria-invalid={createError?.kind === 'nameTaken' || undefined}
          data-testid="remote-name"
        />
        <p className="dk-hint">{t('choose.nameHint')}</p>
      </div>

      {createError && <Callout tone="error" role="alert" testId="remote-create-error">{errorText}</Callout>}

      <div className="conn-actions">
        <Button variant="primary" icon="plug" busy={remote.creating} disabled={ticked.length === 0 || !remote.name} onClick={remote.create} data-testid="remote-connect">
          {t('choose.submit')}
        </Button>
        {ticked.length === 0 && <span className="conn-hint">{t('choose.pick')}</span>}
      </div>
    </div>
  )
}
