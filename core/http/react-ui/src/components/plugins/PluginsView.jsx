/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Tabs, TabPanel, useLoad } from '../ui'
import { useAuth } from '../../context/AuthContext'
import { useOperations } from '../../hooks/useOperations'
import { pluginsApi } from '../../utils/pluginsClient'
import BrowseView from './BrowseView'
import InstalledView from './InstalledView'
import InstallReview from './InstallReview'
import MarketplacesView from './MarketplacesView'
import UpdateReview from './UpdateReview'
import { jobLanded, pluginOps, resolveView, reviewTarget, VIEWS } from './pluginState'
import './plugins.css'

// How a user's install or update is watched: the installs list is read every
// WATCH_EVERY ms, WATCH_TRIES times, until the change shows.
const WATCH_EVERY = 2000
const WATCH_TRIES = 45

// The Plugins tab of the Add-ons page: plugins compatible with the Claude
// Code plugin format. Three views: Browse the marketplaces, the Installed
// plugins with the person's own switch, and the Marketplaces themselves.
// Installs and updates run in the background as plugin operations of the
// shared operations list; the lists are read again when one ends.
export default function PluginsView({ api = pluginsApi }) {
  const { t } = useTranslation('addons')
  const { isAdmin, user } = useAuth()
  const { addToast } = useOutletContext() || {}
  const [params, setParams] = useSearchParams()
  const view = resolveView(params.get('view'))
  const [reload, setReload] = useState(0)
  const [previews, setPreviews] = useState({})
  const [models, setModels] = useState({})
  const [reviewing, setReviewing] = useState(null)
  const [updating, setUpdating] = useState(null)
  const installs = useLoad(() => api.installs(), [reload])

  const { operations } = useOperations()
  const ops = useMemo(() => pluginOps(operations), [operations])
  // An install or update that leaves the operations list has ended: read the
  // lists again.
  const seen = useRef(new Set())
  useEffect(() => {
    const now = new Set(ops.keys())
    let ended = false
    for (const id of seen.current) if (!now.has(id)) ended = true
    seen.current = now
    if (ended) { setModels({}); setReload((n) => n + 1) }
  }, [ops])

  // The review link the CLI and the MCP tool print for a plugin that runs
  // code (?tab=plugins&plugin=<name@marketplace>) opens its review once; the
  // parameter goes, so a reload does not ask again.
  const reviewId = params.get('plugin') || ''
  const asked = useRef('')
  const mounted = useRef(true)
  useEffect(() => () => { mounted.current = false }, [])
  useEffect(() => {
    if (!reviewId || asked.current === reviewId) return
    asked.current = reviewId
    // Dropping the parameter renders again; the answer below must still land.
    setParams((prev) => { const out = new URLSearchParams(prev); out.delete('plugin'); return out }, { replace: true })
    const target = reviewTarget(reviewId)
    if (!target) return
    api.entry(target.marketplace, target.name)
      .then((entry) => { if (mounted.current) setReviewing(entry?.plugin ? entry : { plugin: reviewId, name: target.name }) })
      .catch(() => { if (mounted.current) setReviewing({ plugin: reviewId, name: target.name }) })
  }, [reviewId, api, setParams])

  const select = useCallback((next) => setParams((prev) => {
    const out = new URLSearchParams(prev)
    if (next === 'browse') out.delete('view')
    else out.set('view', next)
    return out
  }), [setParams])

  const changed = useCallback(() => { setModels({}); setReload((n) => n + 1) }, [])
  // A user cannot read the operations list, so the end of their own install
  // or update would go unseen: read the installs list until the change shows,
  // and past the wait point to the notifications, which tell how it ended.
  const watch = useCallback((plugin, name, before) => {
    let tries = 0
    const tick = async () => {
      if (!mounted.current) return
      tries += 1
      let list = null
      try { list = await api.installs() } catch { list = null }
      if (!mounted.current) return
      if (list && jobLanded(list, plugin, before)) {
        addToast?.(t(before ? 'plugins.job.updated' : 'plugins.job.installed', { name }), 'success')
        changed()
        return
      }
      if (tries >= WATCH_TRIES) {
        addToast?.(t('plugins.job.unknown', { name }), 'warning')
        changed()
        return
      }
      setTimeout(tick, WATCH_EVERY)
    }
    setTimeout(tick, WATCH_EVERY)
  }, [api, addToast, t, changed])
  // update: the commit the install had, or null for an install. An update
  // whose old commit is not known is not watched: any row would match.
  const queued = (name, update = null) => (job) => {
    addToast?.(t('plugins.queued', { name }), 'info')
    if (job?.plugin) seen.current.add(job.plugin)
    if (!isAdmin && job?.plugin && update !== '') watch(job.plugin, name, update || '')
    changed()
  }
  const shaOf = (plugin) => (installs.status === 'ready' ? (installs.data.find((i) => i.plugin === plugin)?.sha || '') : '')

  const count = installs.status === 'ready' ? installs.data.length : 0
  return (
    <div className="pl-view" data-testid="plugins-view">
      <Tabs
        label={t('plugins.views.label')}
        idPrefix="pl"
        variant="pill"
        value={view}
        onChange={select}
        items={VIEWS.map((id) => ({ id, label: t(`plugins.views.${id}`), ...(id === 'installed' && count > 0 ? { count } : {}), testId: `plugins-view-${id}` }))}
      />
      <TabPanel idPrefix="pl" value={view}>
        {view === 'browse' && (
          <BrowseView
            api={api} isAdmin={isAdmin} ops={ops} previews={previews} models={models} reload={reload}
            onModel={(id, d) => setModels((m) => ({ ...m, [id]: d }))}
            onInstall={setReviewing}
            onUpdate={(item, ref) => setUpdating({ id: ref.id, name: item.name, plugin: item.plugin })}
          />
        )}
        {view === 'installed' && (
          <InstalledView
            list={installs} api={api} isAdmin={isAdmin} userId={user?.id || ''} ops={ops} addToast={addToast} openId={params.get('install') || ''}
            onChanged={changed} onUpdate={setUpdating} onBrowse={() => select('browse')}
          />
        )}
        {view === 'marketplaces' && <MarketplacesView api={api} isAdmin={isAdmin} onChanged={changed} addToast={addToast} />}
      </TabPanel>
      {reviewing && (
        <InstallReview
          entry={reviewing} api={api} isAdmin={isAdmin}
          onReviewed={(p) => setPreviews((m) => ({ ...m, [reviewing.plugin]: p }))}
          onQueued={(job) => { const name = reviewing.name; setReviewing(null); queued(name)(job) }}
          onClose={() => setReviewing(null)}
        />
      )}
      {updating && (
        <UpdateReview
          install={updating} api={api}
          onQueued={(job) => { const name = updating.name || updating.plugin; const before = shaOf(updating.plugin); setUpdating(null); queued(name, before)(job) }}
          onClose={() => setUpdating(null)}
        />
      )}
    </div>
  )
}
