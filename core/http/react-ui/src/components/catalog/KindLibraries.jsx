/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation } from 'react-router-dom'
import Icon from '../Icon'
import SideSheet from '../SideSheet'
import SourcesView from './SourcesView'
import { filterSources } from './catalogState'
import { addonsCatalog } from '../../utils/addonsClient'
import { describeError } from '../addons/errors'
import './catalog.css'

// The library sources of one kind (models or backends) for the page that lists
// that kind: chips with All and one per source, and a button that opens the
// panel where a source is added, turned on or off, refreshed or removed. It
// reuses SourcesView with the kind fixed, so the page never shows another
// kind's sources. `onSelect(source)` tells the page which chip is picked
// (null for All); `onChanged` runs after a confirmed change.
//
// A page that draws the choice itself (the Backends page puts it in its
// filter menu) passes `bare`: nothing renders but the panel, `onSources`
// receives the sources, and `panelOpen` / `onPanelOpen` own the panel.
export default function KindLibraries({ kind, adapter = addonsCatalog, onSelect, onChanged, bare = false, onSources, panelOpen, onPanelOpen }) {
  const { t } = useTranslation('addons')
  const [sources, setSources] = useState([])
  const [picked, setPicked] = useState('')
  // Settings links here with ?libraries=1 to land on the open panel.
  const location = useLocation()
  const [ownOpen, setOwnOpen] = useState(() => new URLSearchParams(location.search).get('libraries') === '1')
  const controlled = typeof onPanelOpen === 'function'
  const open = controlled ? !!panelOpen : ownOpen
  const setOpen = controlled ? onPanelOpen : setOwnOpen
  const [reload, setReload] = useState(0)

  useEffect(() => {
    const ctl = new AbortController()
    adapter.listSources(ctl.signal)
      .then(all => {
        const list = filterSources(all, kind)
        setSources(list)
        if (onSources) onSources(list.filter(s => !s.removed))
      })
      .catch(() => { /* the panel reports errors; the chips stay as they were */ })
    return () => ctl.abort()
  // eslint-disable-next-line react-hooks/exhaustive-deps -- onSources is a report, not an input
  }, [adapter, kind, reload])

  const pick = (id) => { setPicked(id); if (onSelect) onSelect(sources.find(s => s.id === id) || null) }
  const changed = useCallback(() => {
    setReload(n => n + 1)
    if (onChanged) onChanged()
  }, [onChanged])
  const chips = sources.filter(s => !s.removed)
  const current = chips.some(s => s.id === picked) ? picked : ''

  const panel = open && (
    <SideSheet title={t(`libraries.manageTitle.${kind}`)} onClose={() => setOpen(false)} closeLabel={t('catalog.close')} labelId="kind-libraries-title" testId="kind-libraries-panel" wide>
      <SourcesView kind={kind} adapter={adapter} describeError={e => describeError(e, t)} onChanged={changed} />
    </SideSheet>
  )
  if (bare) return panel || null

  return (
    <div className="cat-bar" data-testid={`kind-libraries-${kind}`}>
      {chips.length > 0 && (
        <div className="cat-chips" role="group" aria-label={t('catalog.libraries')}>
          <button type="button" className="dk-chip" aria-pressed={!current} onClick={() => pick('')}>
            {t('catalog.allLibraries')}
          </button>
          {chips.map(s => (
            <button key={s.id} type="button" className="dk-chip" aria-pressed={current === s.id} onClick={() => pick(s.id)}>
              {s.name}
            </button>
          ))}
        </div>
      )}
      <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" data-action="manage-libraries" onClick={() => setOpen(true)}>
        <Icon name="store" /> {t('libraries.manage')}
      </button>
      {panel}
    </div>
  )
}
