/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import { RegistryContext } from './registryContext'
import { useSlotContributions } from './useAddonRegistry'
import SlotBoundary from './SlotBoundary'
import { SLOTS, slotProps } from './slots'

// The one place the host draws add-on contributions in existing pages. A page
// puts <AddonSlot slot="..." context={...} /> where the slot belongs and
// nothing else: what is drawn, where, and with which props is decided by the
// closed registry in slots.js and the server's apps list.
//
// Rules this component keeps:
//   - it adds to a dedicated container and never wraps or replaces host nodes;
//   - a contribution gets only the props of its slot (slotProps);
//   - each contribution sits in its own error boundary, so a throw leaves a
//     small marker and the page keeps working;
//   - nothing is drawn unless the registry's list and a ready instance of the
//     current generation both say so.

// A component contribution in its own root. The add-on's scoped style sheet
// reaches only this element (see cssScope.js).
function Contributed({ c, props, label }) {
  const View = c.Component
  return (
    <span className="ad-slot" data-addon={c.addonId} data-contribution={c.id} data-testid="addon-contribution">
      <SlotBoundary label={label}>
        <View {...props} />
      </SlotBoundary>
    </span>
  )
}

function ActionIcon({ c }) {
  if (c.iconUrl) return <img className="ad-action-img" src={c.iconUrl} alt="" width="16" height="16" />
  return <Icon name={c.icon || 'puzzle'} />
}

// Actions trigger through the registry, which checks the instance again.
function useActionRunner(registry) {
  const [busy, setBusy] = useState('')
  const run = useCallback(async (c, context) => {
    if (!registry || busy) return
    setBusy(c.key)
    try { await registry.runAction(c, context) } finally { setBusy('') }
  }, [registry, busy])
  return { busy, run }
}

// chat.message.actions: icon buttons in the message's own action row.
function InlineActions({ items, props, label }) {
  const registry = useContext(RegistryContext)
  const { busy, run } = useActionRunner(registry)
  return (
    <>
      {items.map(c => (c.kind === 'action' ? (
        <button
          key={c.key}
          type="button"
          className="cx-icobtn"
          data-testid="addon-action"
          data-addon-action={c.id}
          title={c.label}
          aria-label={c.label}
          aria-busy={busy === c.key || undefined}
          disabled={busy !== ''}
          onClick={() => run(c, props)}
        >
          <ActionIcon c={c} />
        </button>
      ) : <Contributed key={c.key} c={c} props={props} label={label} />))}
    </>
  )
}

// chat.composer.actions: a host-owned overflow button next to the attach
// buttons. The menu holds the actions as menu items and the components in
// their own roots; it is dismissed with Escape or a click outside.
function ComposerMenu({ items, props, label, openLabel }) {
  const registry = useContext(RegistryContext)
  const { busy, run } = useActionRunner(registry)
  const [open, setOpen] = useState(false)
  const rootRef = useRef(null)
  const buttonRef = useRef(null)
  const panelRef = useRef(null)

  useEffect(() => {
    if (!open) return undefined
    panelRef.current?.querySelector('[role="menuitem"]')?.focus()
    const away = (e) => { if (rootRef.current && !rootRef.current.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [open])

  const onKeyDown = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      setOpen(false)
      buttonRef.current?.focus()
      return
    }
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    const entries = Array.from(panelRef.current?.querySelectorAll('[role="menuitem"]') || [])
    if (entries.length === 0) return
    e.preventDefault()
    const at = entries.indexOf(document.activeElement)
    const next = e.key === 'ArrowDown' ? (at + 1) % entries.length : (at - 1 + entries.length) % entries.length
    entries[next].focus()
  }

  return (
    <span className="ad-menu" ref={rootRef} data-testid="addon-composer">
      <button
        ref={buttonRef}
        type="button"
        className="home-attach-btn"
        data-testid="addon-composer-menu"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={openLabel}
        title={openLabel}
        disabled={props.disabled === true}
        onClick={() => setOpen(v => !v)}
      >
        <Icon name="puzzle" />
      </button>
      {open && (
        <div className="ad-menu__panel" role="menu" aria-label={openLabel} ref={panelRef} onKeyDown={onKeyDown}>
          {items.map(c => (c.kind === 'action' ? (
            <button
              key={c.key}
              type="button"
              role="menuitem"
              className="ad-menu__item"
              data-testid="addon-action"
              data-addon-action={c.id}
              disabled={busy !== ''}
              onClick={() => { setOpen(false); run(c, props) }}
            >
              <ActionIcon c={c} />
              <span>{c.label}</span>
            </button>
          ) : (
            <div key={c.key} role="none" className="ad-menu__slot">
              <Contributed c={c} props={props} label={label} />
            </div>
          )))}
        </div>
      )}
    </span>
  )
}

// settings.sections: one titled section per contribution, under a heading the
// host draws with the add-on's own title and icon.
function Sections({ items, props, label }) {
  return (
    <div className="ad-sections" data-testid="addon-settings-sections">
      {items.filter(c => c.kind === 'component').map(c => (
        <section key={c.key} className="ad-section" aria-labelledby={`ad-section-${c.addonId}-${c.id}`} data-testid="addon-settings-section">
          <header className="ad-section__head">
            {c.appIcon
              ? <img className="ad-action-img" src={c.appIcon} alt="" width="20" height="20" />
              : <Icon name="puzzle" />}
            <h2 className="ad-section__title" id={`ad-section-${c.addonId}-${c.id}`}>{c.addonTitle}</h2>
          </header>
          <Contributed c={c} props={props} label={label} />
        </section>
      ))}
    </div>
  )
}

// app.nav: entries after the add-on navigation links of the hub's second row.
function NavEntries({ items, props, label }) {
  return (
    <span className="hub-subnav__slot" data-testid="addon-nav-slot">
      {items.filter(c => c.kind === 'component').map(c => <Contributed key={c.key} c={c} props={props} label={label} />)}
    </span>
  )
}

export default function AddonSlot({ slot, context, enabled = true }) {
  const { t } = useTranslation('addons')
  const items = useSlotContributions(slot, enabled)
  const props = useMemo(() => slotProps(slot, context), [slot, context])
  if (!enabled || items.length === 0 || props === null) return null
  const label = t('slots.unavailable')
  switch (SLOTS[slot].render) {
    case 'inline': return <InlineActions items={items} props={props} label={label} />
    case 'menu-item': return <ComposerMenu items={items} props={props} label={label} openLabel={t('slots.composerMore')} />
    case 'section': return <Sections items={items} props={props} label={label} />
    case 'nav': return <NavEntries items={items} props={props} label={label} />
    default: return null
  }
}
