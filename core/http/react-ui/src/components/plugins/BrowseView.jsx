/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { AppIcon } from '../addons/AddonBits'
import { Button, EmptyState, LoadError, SearchField, Segmented, SkeletonRows, StatusChip, Tag } from '../ui'
import { CodeMarks, ContributionTags } from './PluginBits'
import { cardAction, categoriesOf, runsCodeOf, textOnly } from './pluginState'

const PAGE = 48

// The entries of the active marketplaces as cards. A card says what the
// plugin adds when it is known (after a review, or from an install) and
// whether it runs code, and has one control: Install, Installed or Update.
// A user sees Install only on a plugin that runs no code; one that runs code
// says that an administrator can install it.
//
//   api, isAdmin
//   ops         the plugin operations by plugin ID (pluginState.pluginOps)
//   previews    the reviews of this visit by plugin ID, and models the
//               packages of the installs by install ID: what a card shows
//   reload      a counter: the catalog is read again when it moves
//   onInstall(item), onUpdate(item, installRef)
export default function BrowseView({ api, isAdmin, ops, previews, models, onModel, reload, onInstall, onUpdate }) {
  const { t } = useTranslation('addons')
  const [query, setQuery] = useState('')
  const [typed, setTyped] = useState('')
  const [marketplace, setMarketplace] = useState('')
  const [category, setCategory] = useState('')
  const [only, setOnly] = useState('all')
  const [limit, setLimit] = useState(PAGE)
  const [mkts, setMkts] = useState([])
  const [page, setPage] = useState({ status: 'loading', items: [], total: 0, error: null })
  const [attempt, setAttempt] = useState(0)

  // The search waits for a pause in the typing.
  useEffect(() => {
    const id = setTimeout(() => { setQuery(typed.trim()); setLimit(PAGE) }, 250)
    return () => clearTimeout(id)
  }, [typed])

  useEffect(() => {
    const ctl = new AbortController()
    api.marketplaces.list(ctl.signal).then((list) => setMkts(list.filter((m) => m.active))).catch(() => {})
    return () => ctl.abort()
  }, [api, reload])

  useEffect(() => {
    const ctl = new AbortController()
    setPage((p) => ({ ...p, status: p.items.length ? 'refreshing' : 'loading', error: null }))
    api.catalog({ q: query, marketplace, category, limit }, ctl.signal)
      .then((res) => setPage({ status: 'ready', items: res.items, total: res.total, error: null }))
      .catch((e) => { if (!ctl.signal.aborted) setPage({ status: 'error', items: [], total: 0, error: e }) })
    return () => ctl.abort()
  }, [api, query, marketplace, category, limit, reload, attempt])

  // What an installed entry adds comes from the package of its install.
  useEffect(() => {
    const want = page.items.flatMap((it) => it.installs || []).map((i) => i.id).filter((id) => !(id in models))
    if (want.length === 0) return undefined
    const ctl = new AbortController()
    for (const id of want) {
      api.detail(id, ctl.signal).then((d) => onModel(id, d)).catch(() => onModel(id, null))
    }
    return () => ctl.abort()
  }, [page.items]) // eslint-disable-line react-hooks/exhaustive-deps

  const names = useMemo(() => Object.fromEntries(mkts.map((m) => [m.id, m])), [mkts])
  const categories = categoriesOf(page.items)
  const shown = textOnly(page.items, only === 'text')
  const filtering = Boolean(query || marketplace || category || only !== 'all')
  const clear = () => { setTyped(''); setQuery(''); setMarketplace(''); setCategory(''); setOnly('all') }

  return (
    <div className="pl-browse" data-testid="plugins-browse">
      <div className="ad-toolbar pl-toolbar">
        <SearchField value={typed} onChange={setTyped} label={t('plugins.browse.search')} testId="plugins-search" />
        {mkts.length > 1 && (
          <span className="dk-select-wrap pl-select">
            <select className="dk-select" value={marketplace} onChange={(e) => { setMarketplace(e.target.value); setLimit(PAGE) }} aria-label={t('plugins.browse.marketplace')} data-testid="plugins-filter-marketplace">
              <option value="">{t('plugins.browse.allMarketplaces')}</option>
              {mkts.map((m) => <option key={m.id} value={m.id}>{m.name || m.id}</option>)}
            </select>
          </span>
        )}
        {(categories.length > 0 || category) && (
          <span className="dk-select-wrap pl-select">
            <select className="dk-select" value={category} onChange={(e) => { setCategory(e.target.value); setLimit(PAGE) }} aria-label={t('plugins.browse.category')} data-testid="plugins-filter-category">
              <option value="">{t('plugins.browse.allCategories')}</option>
              {[...new Set([...categories, category].filter(Boolean))].map((c) => <option key={c} value={c}>{c}</option>)}
            </select>
          </span>
        )}
        <Segmented label={t('plugins.browse.kind')} value={only} onChange={setOnly} itemTestId="plugins-only-"
          options={[{ id: 'all', label: t('plugins.browse.all') }, { id: 'text', label: t('plugins.browse.textOnly') }]} />
      </div>

      {page.status === 'loading' && <SkeletonRows count={3} label={t('plugins.browse.loading')} />}
      {page.status === 'error' && (
        <LoadError title={t('plugins.browse.loadFailed')} error={page.error} onRetry={() => setAttempt((n) => n + 1)} testId="plugins-browse-error" />
      )}
      {page.status !== 'loading' && page.status !== 'error' && shown.length === 0 && (filtering ? (
        <EmptyState variant="no-results" title={t('plugins.browse.noMatch')} testId="plugins-no-match"
          actions={<Button size="sm" variant="ghost" onClick={clear}>{t('plugins.browse.clear')}</Button>} />
      ) : (
        <EmptyState icon="store" title={t('plugins.browse.emptyTitle')} body={t(isAdmin ? 'plugins.browse.emptyAdmin' : 'plugins.browse.emptyUser')} testId="plugins-empty" />
      ))}
      {shown.length > 0 && page.status !== 'error' && (
        <ul className="pl-cards" aria-label={t('plugins.browse.label')} aria-busy={page.status === 'refreshing' || undefined}>
          {shown.map((it) => (
            <PluginCard
              key={it.plugin}
              item={it}
              marketplace={names[it.marketplace]}
              isAdmin={isAdmin}
              op={ops.get(it.plugin)}
              preview={previews[it.plugin]}
              model={(it.installs || []).map((i) => models[i.id]).find(Boolean)}
              onInstall={onInstall}
              onUpdate={onUpdate}
            />
          ))}
        </ul>
      )}
      {page.status !== 'error' && page.total > page.items.length && (
        <div className="pl-more">
          <Button variant="ghost" icon="chevron-down" onClick={() => setLimit((n) => n + PAGE)} busy={page.status === 'refreshing'} data-testid="plugins-more">
            {t('plugins.browse.more', { shown: page.items.length, total: page.total })}
          </Button>
        </div>
      )}
    </div>
  )
}

function PluginCard({ item, marketplace, isAdmin, op, preview, model, onInstall, onUpdate }) {
  const { t } = useTranslation('addons')
  const action = cardAction(item, { isAdmin, busy: Boolean(op) })
  // What the plugin adds: this person's review, the details they opened, or
  // the last review of the plugin by anyone.
  const known = preview || model || item.reviewed || null
  const hint = !preview && item.runsCodeFrom === 'curated'
  const runsCode = preview ? preview.runsCode : runsCodeOf(item)
  const update = (item.installs || []).find((i) => i.state === 'update_available')
  const id = `pl-card-${item.plugin.replace(/[^a-zA-Z0-9_-]/g, '-')}`
  return (
    <li className="ad-card pl-card" aria-labelledby={id} data-testid="plugin-card" data-plugin={item.plugin} data-action={action}>
      <div className="ad-card__head">
        <AppIcon id={item.plugin} name={item.name} size={36} />
        <div className="ad-card__id">
          <h3 className="ad-entry__title" id={id}>{item.name}</h3>
          <p className="ad-card__by">
            <Icon name="store" aria-hidden="true" />
            {marketplace?.default ? t('plugins.browse.curated') : (marketplace?.name || item.marketplace)}
            {item.author && <> · {t('plugins.browse.by', { author: item.author })}</>}
            {item.version && <> · <span className="pl-version">{t('plugins.facts.versionValue', { version: item.version })}</span></>}
          </p>
        </div>
      </div>
      {item.description && <p className="ad-entry__text">{item.description}</p>}
      {item.category && <span className="ui-tags pl-tags"><Tag icon="tag">{item.category}</Tag></span>}
      {known && <ContributionTags model={known.model} hosts={known.hosts} runsCode={runsCode} />}
      {!known && runsCode === true && <span className="ui-tags pl-tags"><CodeMarks /></span>}
      {/* With no install, review or curated claim, the card says that
          nothing is known yet and never suggests that it runs none. */}
      {runsCode === null && item.supported !== false && (
        <span className="ui-tags pl-tags">
          <Tag icon="help-circle" title={t('plugins.adds.uncheckedTitle')} testId="plugin-unchecked">{t('plugins.adds.unchecked')}</Tag>
        </span>
      )}
      {runsCode === false && !known && (
        <span className="ui-tags pl-tags"><Tag icon="check" testId="plugin-no-code">{t('plugins.adds.noCode')}</Tag></span>
      )}
      {hint && typeof runsCode === 'boolean' && (
        <span className="ui-tags pl-tags">
          <Tag icon="info" title={t('plugins.adds.curatedHintTitle')} testId="plugin-curated-hint">{t('plugins.adds.curatedHint')}</Tag>
        </span>
      )}
      <div className="ad-card__foot">
        {action === 'unsupported' && <p className="ad-card__note" data-testid="plugin-unsupported">{item.reason || t('plugins.browse.unsupported')}</p>}
        {action === 'admin-only' && <p className="ad-card__note" data-testid="plugin-admin-only">{t('plugins.browse.adminOnly')}</p>}
        {action === 'off' && <p className="ad-card__note">{t('plugins.browse.userInstallsOff')}</p>}
        <div className="ad-entry__acts">
          {action === 'install' && (
            <Button size="sm" icon="download" onClick={() => onInstall(item)} aria-label={t('plugins.browse.installNamed', { name: item.name })} data-testid="plugin-install">
              {t('plugins.browse.install')}
            </Button>
          )}
          {action === 'installing' && <StatusChip status="installing" label={t('plugins.browse.installing')} testId="plugin-installing" />}
          {action === 'installed' && <StatusChip tone="ok" label={t('plugins.browse.installed')} testId="plugin-installed" />}
          {action === 'update' && (
            <Button size="sm" icon="arrow-up" onClick={() => onUpdate(item, update)} aria-label={t('plugins.browse.updateNamed', { name: item.name })} data-testid="plugin-update">
              {t('plugins.browse.update')}
            </Button>
          )}
        </div>
      </div>
    </li>
  )
}
