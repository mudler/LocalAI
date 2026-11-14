/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation } from 'react-router-dom'
import Icon from '../Icon'
import SideSheet from '../SideSheet'
import SourcesView from './SourcesView'
import { filterSources, sourceState } from './catalogState'
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
//
// A page can add chips of its own after the library ones with `extraChips`
// (`{ id, label, tag? }`): the Models page puts its online sources there.
// `extraPicked` is the one in force, `onExtraPick(id)` reports a pick, and
// picking a library chip reports `onExtraPick(null)`, so the two kinds of chip
// are one choice.
export default function KindLibraries({ kind, adapter = addonsCatalog, onSelect, onChanged, bare = false, onSources, panelOpen, onPanelOpen, extraChips = [], extraPicked = '', onExtraPick }) {
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

  const pick = (id) => {
    setPicked(id)
    if (extraPicked && onExtraPick) onExtraPick(null)
    if (onSelect) onSelect(sources.find(s => s.id === id) || null)
  }
  const changed = useCallback(() => {
    setReload(n => n + 1)
    if (onChanged) onChanged()
  }, [onChanged])
  // A chip filters the table to the models one library lists. A library that
  // is off (the user turned it off, or the official libraries are off) lists
  // none, so it gets no chip: the build ships two records for the official
  // models, the library and the older gallery index, and only one is on.
  // Manage libraries still lists every record.
  const chips = sources.filter(s => sourceState(s) === 'enabled')
  const current = !extraPicked && chips.some(s => s.id === picked) ? picked : ''
  // The picked library went off or was removed in the panel: its chip is gone,
  // so the page goes back to All instead of filtering by a library with no chip.
  const pickedGone = !!picked && !chips.some(s => s.id === picked)
  useEffect(() => {
    if (!pickedGone) return
    setPicked('')
    if (onSelect) onSelect(null)
  // eslint-disable-next-line react-hooks/exhaustive-deps -- onSelect is a report, not an input
  }, [pickedGone])

  const panel = open && (
    <SideSheet title={t(`libraries.manageTitle.${kind}`)} onClose={() => setOpen(false)} closeLabel={t('catalog.close')} labelId="kind-libraries-title" testId="kind-libraries-panel" wide>
      <SourcesView kind={kind} adapter={adapter} describeError={e => describeError(e, t)} onChanged={changed} />
    </SideSheet>
  )
  if (bare) return panel || null

  return (
    <div className="cat-bar" data-testid={`kind-libraries-${kind}`}>
      {(chips.length > 0 || extraChips.length > 0) && (
        <div className="cat-chips" role="group" aria-label={t('catalog.libraries')}>
          <button type="button" className="dk-chip" aria-pressed={!current && !extraPicked} onClick={() => pick('')}>
            {t('catalog.allLibraries')}
          </button>
          {chips.map(s => (
            <button key={s.id} type="button" className="dk-chip" aria-pressed={current === s.id} onClick={() => pick(s.id)}>
              {s.name}
            </button>
          ))}
          {extraChips.map(c => (
            <button
              key={c.id}
              type="button"
              className="dk-chip"
              data-testid={`source-chip-${c.id}`}
              aria-pressed={extraPicked === c.id}
              onClick={() => onExtraPick?.(extraPicked === c.id ? null : c.id)}
            >
              {c.label}
              {c.tag && <span className="dk-chip__tag">{c.tag}</span>}
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
