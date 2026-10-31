/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import { RegistryContext } from './registryContext'
import { useAuth } from '../context/AuthContext'
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
// A section is a block: an inline-flex root would lay a form out in a row.
function Contributed({ c, props, label, block = false }) {
  const View = c.Component
  const Root = block ? 'div' : 'span'
  return (
    <Root className={block ? 'ad-slot ad-slot--block' : 'ad-slot'} data-addon={c.addonId} data-contribution={c.id} data-testid="addon-contribution">
      <SlotBoundary label={label}>
        <View {...props} />
      </SlotBoundary>
    </Root>
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

// settings.sections: one section per contribution, drawn on the add-on's own
// page (the Settings tab of its details, see AddonSettings.jsx), where the
// page already names the add-on. Drawn for several add-ons at once, each
// section has a heading with the add-on's title and icon.
function Sections({ items, props, label, own }) {
  return (
    <div className={own ? 'ad-sections ad-sections--own' : 'ad-sections'} data-testid="addon-settings-sections">
      {items.filter(c => c.kind === 'component').map(c => (
        <section
          key={c.key}
          id={`ad-section-${c.addonId}-${c.id}`}
          className="ad-section"
          aria-labelledby={own ? undefined : `ad-section-title-${c.addonId}-${c.id}`}
          aria-label={own ? c.addonTitle : undefined}
          data-testid="addon-settings-section"
          data-addon-section={c.addonId}
        >
          {!own && (
            <header className="ad-section__head">
              {c.appIcon
                ? <img className="ad-action-img" src={c.appIcon} alt="" width="20" height="20" />
                : <Icon name="puzzle" />}
              <h2 className="ad-section__title" id={`ad-section-title-${c.addonId}-${c.id}`}>{c.addonTitle}</h2>
            </header>
          )}
          <Contributed c={c} props={props} label={label} block />
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

// files.preview: the first contribution that handles the file's extension
// draws it, in a frame the host owns. The host's own view stays below it.
function Preview({ items, props, label }) {
  const c = items.find(x => x.kind === 'component')
  if (!c) return null
  return (
    <div className="ad-preview" data-testid="addon-preview" data-addon={c.addonId}>
      <Contributed c={c} props={props} label={label} />
    </div>
  )
}

// settings.sections is drawn for administrators only, as it was when the
// sections were part of the Settings page; context.addonId narrows it to one
// add-on. The id is the host's filter, never a prop of the contribution.
export default function AddonSlot({ slot, context, enabled = true }) {
  const { t } = useTranslation('addons')
  const { isAdmin } = useAuth()
  const sections = slot === 'settings.sections'
  const on = enabled && (!sections || isAdmin)
  const only = sections && typeof context?.addonId === 'string' && context.addonId !== '' ? context.addonId : undefined
  const items = useSlotContributions(slot, on, slot === 'files.preview' ? context?.extension : undefined, only)
  const props = useMemo(() => slotProps(slot, context), [slot, context])
  if (!on || items.length === 0 || props === null) return null
  const label = t('slots.unavailable')
  switch (SLOTS[slot].render) {
    case 'inline': return <InlineActions items={items} props={props} label={label} />
    case 'menu-item': return <ComposerMenu items={items} props={props} label={label} openLabel={t('slots.composerMore')} />
    case 'section': return <Sections items={items} props={props} label={label} own={Boolean(only)} />
    case 'nav': return <NavEntries items={items} props={props} label={label} />
    case 'preview': return <Preview items={items} props={props} label={label} />
    default: return null
  }
}
