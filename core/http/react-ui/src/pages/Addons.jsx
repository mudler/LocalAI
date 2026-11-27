/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { Button, Page, PageHeader, Tabs } from '../components/ui'
import { useAuth } from '../context/AuthContext'
import { addonsApi, addonsCatalog } from '../utils/addonsClient'
import AppsView from '../components/addons/AppsView'
import BrowseView from '../components/addons/BrowseView'
import LibrariesView from '../components/addons/LibrariesView'
import HostAccessView from '../components/addons/HostAccessView'
import PluginsView from '../components/plugins/PluginsView'
import InstallFromFile, { FileInstallBanner } from '../components/addons/InstallFromFile'
import { useFileInstall } from '../components/addons/useFileInstall'
import InstallDialog from '../components/addons/InstallDialog'
import AddonQueue from '../components/addons/AddonQueue'
import { useInstallQueue } from '../components/addons/useInstallQueue'
import { useHostGrants } from '../components/addons/useHostGrants'
import { useAddonUpgrades } from '../hooks/useAddonUpgrades'
import { awaitingApproval } from '../components/addons/hostGrantsState'
import { resolveManage, resolveTab, visibleTabs } from '../components/addons/addonState'
import '../components/addons/addons.css'

// Add-ons. Everyone sees the add-ons they may use. Administrators also find
// add-ons and edit sources; what is installed, and how it runs, is in the Details
// of each add-on. Hiding a tab is a convenience: the owner checks administration
// again on every call.
export default function Addons() {
  // The details pane and the install flow show words of the upgrade
  // namespace. Load it with this page: a namespace that loads later suspends
  // the page, which hides it and drops the focus of the person using it.
  const { t } = useTranslation(['addons', 'upgrade'])
  const { isAdmin, hasFeature } = useAuth()
  // The installed count for the tab and whether the list is a first run (the
  // empty state then carries the way to Find add-ons, so the header does not).
  const [summary, setSummary] = useState(null)
  // A file install changes the list; the list is read again.
  const [listKey, setListKey] = useState(0)
  const [params, setParams] = useSearchParams()
  const manage = resolveManage(params, isAdmin)
  const plugins = hasFeature('plugins')
  const tab = manage ? 'apps' : resolveTab(params.get('tab'), isAdmin, { plugins })
  const tabs = visibleTabs(isAdmin, { plugins })
  // Host access is read once here: the tab shows how many approvals wait, and
  // the list of add-ons shows the same banners.
  const grants = useHostGrants({ enabled: Boolean(isAdmin) })
  // An approval whose upgrade is queued or running no longer waits for anyone.
  const upgrades = useAddonUpgrades()
  const waiting = awaitingApproval(grants.data, (id) => upgrades.live.find(op => op.addonId === id) || null).length
  // The installs of the background queue and the dialog that reviews them.
  // They live on the page, not on a tab: the rows stay in view while the
  // person moves between the tabs.
  const queue = useInstallQueue({ adapter: addonsCatalog })
  // The install from a file lives here, above the tabs, so a dialog the person
  // closed still tells how its install goes from whatever tab is open.
  const fileInstall = useFileInstall({ onInstalled: () => setListKey(n => n + 1) })
  const [installed, setInstalled] = useState({})
  useEffect(() => {
    if (!queue.current) return undefined
    const ctl = new AbortController()
    addonsApi.installed(ctl.signal)
      .then(list => setInstalled(Object.fromEntries(list.map(a => [a.id, a]))))
      .catch(() => {})
    return () => ctl.abort()
  }, [queue.current, queue.tick]) // eslint-disable-line react-hooks/exhaustive-deps
  // A link to review one add-on again (from the Activity page): open its
  // review and drop the parameter so a reload does not ask twice.
  const reviewId = params.get('review')
  const reviewing = useRef('')
  useEffect(() => {
    if (!isAdmin || !reviewId || reviewing.current === reviewId) return
    reviewing.current = reviewId
    setParams(prev => { const out = new URLSearchParams(prev); out.delete('review'); return out }, { replace: true })
    queue.reviewId(reviewId)
  }, [isAdmin, reviewId, queue, setParams])

  const select = useCallback((next) => {
    setParams(prev => {
      const out = new URLSearchParams()
      if (next !== 'apps') out.set('tab', next)
      // Searches belong to a tab; do not carry them over.
      if (next === 'browse' && prev.get('tab') === 'browse') for (const k of ['q', 'source', 'page']) if (prev.get(k)) out.set(k, prev.get(k))
      return out
    })
  }, [setParams])

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
        actions={isAdmin && tab === 'apps' ? (
          <>
            {showFind && <Button variant="primary" icon="store" to="/app/addons?tab=browse" data-testid="addons-add">{t('drawer.add')}</Button>}
            <InstallFromFile install={fileInstall} />
          </>
        ) : null}
      />

      {tabs.length > 1 && (
        <Tabs
          label={t('tabs.label')}
          idPrefix="ad"
          value={tab}
          onChange={select}
          items={tabs.map(id => ({
            id,
            label: t(`tabs.${id}`),
            ...(id === 'apps' && summary && summary.count > 0 ? { count: summary.count } : {}),
            ...(id === 'access' && waiting > 0 ? { count: waiting, countTone: 'warn', countTestId: 'access-waiting', countLabel: t('hostAccess.tabWaiting', { count: waiting }) } : {}),
          }))}
        />
      )}

      {isAdmin && <AddonQueue queue={queue} />}
      {isAdmin && <FileInstallBanner install={fileInstall} onRetry={() => select('apps')} />}

      <div role="tabpanel" id={`ad-panel-${tab}`} aria-labelledby={tabs.length > 1 ? `ad-tab-${tab}` : undefined} aria-label={tabs.length > 1 ? undefined : t('tabs.apps')} className="ad-panel">
        {tab === 'apps' && <AppsView key={`${listKey}:${queue.tick}`} api={addonsApi} adapter={addonsCatalog} onSummary={setSummary} grants={grants} queue={queue} fileOp={fileInstall.activity} />}
        {tab === 'browse' && <BrowseView api={addonsApi} adapter={addonsCatalog} queue={queue} />}
        {tab === 'libraries' && <LibrariesView adapter={addonsCatalog} />}
        {tab === 'access' && <HostAccessView grants={grants} />}
        {tab === 'plugins' && <PluginsView />}
      </div>
      {isAdmin && (
        <InstallDialog
          entry={queue.current}
          installed={installed}
          batch={queue.batch}
          review={queue.review}
          sending={queue.sending}
          onAccept={queue.accept}
          onSkip={queue.skip}
          onClose={queue.close}
        />
      )}
    </Page>
  )
}
