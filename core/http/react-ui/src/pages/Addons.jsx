/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useRef } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import Icon from '../components/Icon'
import { useAuth } from '../context/AuthContext'
import { addonsApi, addonsCatalog } from '../utils/addonsClient'
import AppsView from '../components/addons/AppsView'
import BrowseView from '../components/addons/BrowseView'
import InstalledView from '../components/addons/InstalledView'
import LibrariesView from '../components/addons/LibrariesView'
import { resolveManage, resolveTab, visibleTabs } from '../components/addons/addonState'
import '../components/addons/addons.css'

const TAB_ICONS = { apps: 'puzzle', browse: 'store', installed: 'boxes', libraries: 'book' }

// Add-ons. Everyone sees the apps they may use. Administrators also browse
// libraries, manage what is installed and edit libraries. Hiding a tab is a
// convenience: the owner checks administration again on every call.
export default function Addons() {
  const { t } = useTranslation('addons')
  const { isAdmin } = useAuth()
  const [params, setParams] = useSearchParams()
  const manage = resolveManage(params, isAdmin)
  const tab = manage ? 'installed' : resolveTab(params.get('tab'), isAdmin)
  const tabs = visibleTabs(isAdmin)
  const refs = useRef({})

  const select = useCallback((next) => {
    setParams(prev => {
      const out = new URLSearchParams()
      if (next !== 'apps') out.set('tab', next)
      // Searches belong to a tab; do not carry them over.
      if (next === 'browse' && prev.get('tab') === 'browse') for (const k of ['q', 'source', 'page']) if (prev.get(k)) out.set(k, prev.get(k))
      return out
    })
  }, [setParams])

  const onManage = useCallback((id) => {
    setParams(prev => {
      const out = new URLSearchParams(prev)
      out.set('tab', 'installed')
      if (id) out.set('manage', id)
      else out.delete('manage')
      return out
    }, { replace: true })
  }, [setParams])

  const onKeyDown = (e) => {
    const index = tabs.indexOf(tab)
    let next = index
    if (e.key === 'ArrowRight') next = (index + 1) % tabs.length
    else if (e.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = tabs.length - 1
    else return
    e.preventDefault()
    select(tabs[next])
    refs.current[tabs[next]]?.focus()
  }

  return (
    <div className="page page--wide ad-page" data-testid="addons">
      <PageHeader title={t('title')} supporting={t('supporting')} />

      {tabs.length > 1 && (
        <div className="ad-tabs" role="tablist" aria-label={t('tabs.label')} onKeyDown={onKeyDown}>
          {tabs.map(id => (
            <button
              key={id}
              ref={el => { refs.current[id] = el }}
              type="button"
              role="tab"
              id={`ad-tab-${id}`}
              className="ad-tab"
              aria-selected={tab === id}
              aria-controls={tab === id ? `ad-panel-${id}` : undefined}
              tabIndex={tab === id ? 0 : -1}
              onClick={() => select(id)}
            >
              <Icon name={TAB_ICONS[id]} aria-hidden="true" /> {t(`tabs.${id}`)}
            </button>
          ))}
        </div>
      )}

      <div role="tabpanel" id={`ad-panel-${tab}`} aria-labelledby={tabs.length > 1 ? `ad-tab-${tab}` : undefined} aria-label={tabs.length > 1 ? undefined : t('tabs.apps')} className="ad-panel">
        {tab === 'apps' && <AppsView api={addonsApi} />}
        {tab === 'browse' && <BrowseView api={addonsApi} adapter={addonsCatalog} />}
        {tab === 'installed' && <InstalledView api={addonsApi} adapter={addonsCatalog} manage={manage} onManage={onManage} />}
        {tab === 'libraries' && <LibrariesView adapter={addonsCatalog} />}
      </div>
    </div>
  )
}
