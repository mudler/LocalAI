/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import SearchField from '../ui/SearchField'
import Segmented from '../ui/Segmented'
import { useModels } from '../../hooks/useModels'

// With more models than this the list gets a search box.
const SEARCH_FROM = 8

// "Which models may the other LocalAI use?" in the Share dialog, for a remote
// invite: "All models" (the default, models added later too) or a list of
// chosen models. The names come from the same list the rest of the UI uses.
//
//   scope, onScope(scope)   'all' or 'specific'
//   picked, onPicked(set)   the chosen names (a Set), used when scope is 'specific'
//   reason                  '' or the key of why the invite cannot be made yet
//   reasonId                the id of that text, for the button it explains
export default function ShareModelChoice({ scope, onScope, picked, onPicked, reason, reasonId }) {
  const { t } = useTranslation('p2p')
  const id = useId()
  const specific = scope === 'specific'
  const { models, loading, error, refetch } = useModels(undefined, { enabled: specific })
  const [query, setQuery] = useState('')
  const names = useMemo(
    () => models.filter(m => !m.disabled && !m.alias_error).map(m => m.id).sort((a, b) => a.localeCompare(b)),
    [models],
  )
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return q ? names.filter(n => n.toLowerCase().includes(q)) : names
  }, [names, query])

  const toggle = name => {
    const next = new Set(picked)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    onPicked(next)
  }

  return (
    <div className="dk-field p2p-models" data-testid="share-models">
      <span className="dk-label" id={`${id}-scope`}>{t('share.models.label')}</span>
      <Segmented
        mode="radio" labelledBy={`${id}-scope`} value={scope} onChange={onScope} testId="share-models-scope" itemTestId="share-models-scope-"
        options={[{ id: 'all', label: t('share.models.all') }, { id: 'specific', label: t('share.models.specific') }]}
      />
      <p className="dk-hint">{t(specific ? 'share.models.specificHint' : 'share.models.allHint')}</p>
      {specific && loading && <p className="dk-hint" role="status" data-testid="share-models-loading">{t('share.models.loading')}</p>}
      {specific && error && (
        <Callout tone="error" role="alert" testId="share-models-error"
          actions={<Button size="sm" onClick={refetch}>{t('share.models.retry')}</Button>}>
          {t('share.models.loadFailed')}
        </Callout>
      )}
      {specific && !loading && !error && names.length === 0 && (
        <Callout tone="info" testId="share-models-empty">{t('share.models.empty')}</Callout>
      )}
      {specific && names.length > 0 && (
        <>
          {names.length > SEARCH_FROM && (
            <div>
              <SearchField value={query} onChange={setQuery} label={t('share.models.search')} testId="share-models-search" />
            </div>
          )}
          <ul className="p2p-models__list" aria-labelledby={`${id}-scope`} data-testid="share-models-list">
            {shown.map(name => (
              <li key={name}>
                <label className="p2p-models__row" data-testid={`share-model-${name}`}>
                  <input type="checkbox" className="dk-check" checked={picked.has(name)} onChange={() => toggle(name)} />
                  <span className="p2p-models__name dk-mono">{name}</span>
                </label>
              </li>
            ))}
            {shown.length === 0 && <li className="p2p-models__none">{t('share.models.noMatch')}</li>}
          </ul>
        </>
      )}
      {specific && (reason
        ? <p className="dk-field-error" role="status" id={reasonId} data-testid="share-models-reason"><Icon name="alert-circle" aria-hidden="true" />{t(`share.models.reason.${reason}`)}</p>
        : <p className="dk-hint" role="status" data-testid="share-models-count">{t('share.models.selected', { count: picked.size })}</p>)}
    </div>
  )
}
