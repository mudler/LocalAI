/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { addonsCatalog } from '../../utils/addonsClient'
import { filterSources, sourceState } from '../catalog/catalogState'

const PAGE = { model: '/app/models', backend: '/app/backends' }

// A read-only view of the library sources of one kind. Changes are made in the
// Manage libraries panel of the page that lists that kind.
export default function LibrarySourcesField({ kind, id, adapter = addonsCatalog }) {
  const { t } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', sources: [] })

  useEffect(() => {
    const ctl = new AbortController()
    adapter.listSources(ctl.signal)
      .then(all => setState({ status: 'ready', sources: filterSources(all, kind) }))
      .catch(() => { if (!ctl.signal.aborted) setState({ status: 'error', sources: [] }) })
    return () => ctl.abort()
  }, [adapter, kind])

  const to = `${PAGE[kind]}?libraries=1`
  return (
    <div className="st-sources" id={id} data-testid={`settings-sources-${kind}`}>
      {state.status === 'error' && <p className="st-note" role="alert">{t('settingsView.loadFailed')}</p>}
      {state.status === 'ready' && state.sources.length === 0 && <p className="st-note">{t('settingsView.none')}</p>}
      {state.sources.length > 0 && (
        <ul className="st-sources__list" aria-label={t('settingsView.listLabel')}>
          {state.sources.map(s => {
            const mirrors = s.mirrors || []
            return (
              <li key={s.id} data-source={s.id} data-state={sourceState(s)}>
                <strong>{s.name}</strong> <code className="st-key">{s.id}</code>{' '}
                <span className="dk-mono">{s.url}</span>{' '}
                <span className="st-tag" data-testid="source-state">{t(`libraries.state.${sourceState(s)}`)}</span>
                {s.default && <> <span className="st-tag" data-testid="source-default">{t('libraries.default')}</span></>}
                {mirrors.length > 0 && <div className="st-note">{t('settingsView.mirrors', { list: mirrors.join(', ') })}</div>}
              </li>
            )
          })}
        </ul>
      )}
      <Link className="dk-btn dk-btn--secondary dk-btn--sm" to={to} data-action="manage-libraries">{t('libraries.manage')}</Link>
    </div>
  )
}
