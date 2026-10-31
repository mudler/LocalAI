/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { Button, Page, PageHeader } from '../components/ui'
import { useAuth } from '../context/AuthContext'
import { addonsApi, addonsCatalog } from '../utils/addonsClient'
import AppsView from '../components/addons/AppsView'
import BrowseView from '../components/addons/BrowseView'
import LibrariesView from '../components/addons/LibrariesView'
import { resolveManage, resolveTab, visibleTabs } from '../components/addons/addonState'
import '../components/addons/addons.css'

// Add-ons. Everyone sees the add-ons they may use. Administrators also find
// add-ons and edit sources; what is installed, and how it runs, is in the Details
// of each add-on. Hiding a tab is a convenience: the owner checks administration
// again on every call.
export default function Addons() {
  const { t } = useTranslation('addons')
  const { isAdmin, hasFeature } = useAuth()
  // The installed count for the tab and whether the list is a first run (the
  // empty state then carries the way to Find add-ons, so the header does not).
  const [summary, setSummary] = useState(null)
  const [params, setParams] = useSearchParams()
  const manage = resolveManage(params, isAdmin)
  const tab = manage ? 'apps' : resolveTab(params.get('tab'), isAdmin)
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

  // Add-ons and Pages are one family: installed software here, pages a person
  // or an agent made on the Pages screen. Say where the other kind lives.
  const pagesNote = hasFeature('chats') && hasFeature('pages')
    ? <span className="ad-family"><Trans t={t} i18nKey="family.pages" components={{ go: <Link to="/app/pages" /> }} /></span>
    : null
  const showFind = isAdmin && tab === 'apps' && Boolean(summary) && !summary.firstRun
  return (
    <Page width="full" className="ad-page" data-testid="addons">
      <PageHeader
        title={t('title')}
        supporting={<>{t('supporting')}{pagesNote && <> {pagesNote}</>}</>}
        actions={showFind ? <Button variant="primary" icon="store" to="/app/addons?tab=browse" data-testid="addons-add">{t('drawer.add')}</Button> : null}
      />

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
              {t(`tabs.${id}`)}
              {id === 'apps' && summary && summary.count > 0 && <span className="ad-tab__count">{summary.count}</span>}
            </button>
          ))}
        </div>
      )}

      <div role="tabpanel" id={`ad-panel-${tab}`} aria-labelledby={tabs.length > 1 ? `ad-tab-${tab}` : undefined} aria-label={tabs.length > 1 ? undefined : t('tabs.apps')} className="ad-panel">
        {tab === 'apps' && <AppsView api={addonsApi} adapter={addonsCatalog} onSummary={setSummary} />}
        {tab === 'browse' && <BrowseView api={addonsApi} adapter={addonsCatalog} />}
        {tab === 'libraries' && <LibrariesView adapter={addonsCatalog} />}
      </div>
    </Page>
  )
}
