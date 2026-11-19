import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import SearchField from '../ui/SearchField'
import SkippedList from './SkippedList'
import { MAX_ADOPT, filterModels, missingModels, resultParts, usecasesOf } from '../../utils/remotes'

// How many models make the list long enough to need a search box.
const SEARCH_FROM = 12

// What a remote offers, as checkboxes with a chip for each use, and the
// button that creates the remote. Until the test has passed it says what
// to do first. When the remote was saved only in part, or models were left
// out, the result replaces the button: the skipped models with their reasons,
// "Adopt models" for the missing ones and "Continue".
export default function RemoteChooseStep({ remote }) {
  const { t } = useTranslation('remotes')
  const uid = useId()
  const [query, setQuery] = useState('')
  const { phase, models, ticked, createError, outcome } = remote

  if (phase !== 'ok') {
    return <p className="remotes-hint" data-testid="remote-choose-empty">{t('choose.waiting')}</p>
  }

  const shown = filterModels(models, query)
  const shownIds = shown.map(m => m.id)
  const usecaseLabel = (u) => t(`usecase.${u}`, { defaultValue: u })
  const errorText = createError
    ? t(`choose.error.${createError.kind}`, { message: createError.message })
    : ''

  return (
    <div className="remotes-choose" data-testid="remote-choose-step">
      {models.length >= SEARCH_FROM && (
        <SearchField value={query} onChange={setQuery} label={t('choose.search')} />
      )}
      <div className="remotes-choose__bar">
        <span className="remotes-count" aria-live="polite" data-testid="remote-count">
          {t('choose.count', { selected: ticked.length, total: models.length })}
        </span>
        <span className="remotes-choose__all">
          <Button size="sm" variant="ghost" onClick={() => remote.setMany(shownIds, true)}>{t('choose.selectAll')}</Button>
          <Button size="sm" variant="ghost" onClick={() => remote.setMany(shownIds, false)}>{t('choose.selectNone')}</Button>
        </span>
      </div>
      <ul className="remotes-models" role="group" aria-label={t('choose.listLabel')} data-testid="remote-models">
        {shown.map(m => (
          <li key={m.id}>
            <label className="remotes-model">
              <input type="checkbox" className="dk-check" checked={ticked.includes(m.id)} onChange={() => remote.toggle(m.id)} />
              <span className="remotes-model__id">{m.id}</span>
              <span className="remotes-model__uses">
                {usecasesOf(remote.kind, m).map(u => <span key={u} className="dk-badge">{usecaseLabel(u)}</span>)}
              </span>
            </label>
          </li>
        ))}
        {shown.length === 0 && <li className="remotes-hint">{t('choose.noMatch')}</li>}
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

      {outcome && (outcome.warning || resultParts(outcome).skipped.length > 0)
        ? <Outcome remote={remote} />
        : (
          <div className="remotes-actions">
            <Button variant="primary" icon="plug" busy={remote.creating} disabled={ticked.length === 0 || ticked.length > MAX_ADOPT || !remote.name || remote.locked} onClick={remote.create} data-testid="remote-connect">
              {t('choose.submit')}
            </Button>
            {ticked.length === 0 && <span className="remotes-hint">{t('choose.pick')}</span>}
            {ticked.length > MAX_ADOPT && <span className="remotes-hint" data-testid="remote-too-many">{t('choose.tooMany', { max: MAX_ADOPT })}</span>}
          </div>
        )}
    </div>
  )
}

// What a saved remote came to: the models added, the warning of a partial
// result and the skipped models. The warning is the server's fixed English
// text, so the page says the same in its own words and does not print it. "Adopt models" adds the ones still missing; it
// never creates the remote again.
function Outcome({ remote }) {
  const { t } = useTranslation('remotes')
  const { outcome, adopting } = remote
  const { created, skipped, warning } = resultParts(outcome)
  const missing = missingModels(remote.chosen, outcome).length
  return (
    <div className="remotes-outcome" data-testid="remote-outcome">
      <Callout tone="warn" role="alert" title={t(warning ? 'result.partialTitle' : 'result.skippedTitle')} testId="remote-warning">
        {t('result.added', { count: created.length })}
        {warning && missing > 0 && <span className="remotes-detail"> {t('result.adoptHint')}</span>}
      </Callout>
      <SkippedList skipped={skipped} />
      <div className="remotes-actions">
        {missing > 0 && (
          <Button variant="primary" icon="plus" busy={adopting} onClick={remote.adopt} data-testid="remote-adopt">{t('result.adopt')}</Button>
        )}
        <Button variant={missing > 0 ? 'ghost' : 'primary'} onClick={remote.finish} data-testid="remote-continue">{t('result.continue')}</Button>
      </div>
    </div>
  )
}
