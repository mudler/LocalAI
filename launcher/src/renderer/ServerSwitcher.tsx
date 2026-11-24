// The server switcher of the bar (mock screens 24 to 27 and 29). A button with
// the name of the server the window shows, and a popover that opens upward:
// the saved servers, "Add server…" and "Manage servers…". Arrow keys move,
// Enter picks, Escape closes and puts the focus back on the button. Picking
// a server makes it the default.
import { useEffect, useId, useRef, type RefObject } from 'react'
import { Icon } from '@localai/ui-kit'
import type { ServerRowView } from '../shared/types'
import { Popover } from './Popover'
import { ServerDot } from './screens/ServersTab'
import { switcherLine } from './serversUi'
import { S } from './strings'
import { clockTime } from './viewModel'

const isMac = () => typeof navigator !== 'undefined' && /mac/i.test(navigator.platform || navigator.userAgent || '')

export function ServerSwitcher({
  rows,
  current,
  open,
  setOpen,
  buttonRef,
  onPick,
  onAdd,
  onManage,
}: {
  rows: ServerRowView[]
  current: ServerRowView
  open: boolean
  setOpen: (v: boolean) => void
  buttonRef: RefObject<HTMLButtonElement | null>
  onPick: (id: string) => void
  onAdd: () => void
  onManage: () => void
}) {
  const menuId = useId()
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const close = () => setOpen(false)
  useEffect(() => {
    if (!open) return
    // The current server first: the person sees where they are.
    const at = Math.max(0, rows.findIndex((r) => r.isCurrent))
    refs.current[at]?.focus()
  }, [open, rows])
  const move = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const list = refs.current.filter(Boolean) as HTMLButtonElement[]
    const at = list.indexOf(document.activeElement as HTMLButtonElement)
    const go = (i: number) => {
      e.preventDefault()
      list[(i + list.length) % list.length]?.focus()
    }
    if (e.key === 'ArrowDown') go(at + 1)
    else if (e.key === 'ArrowUp') go(at - 1)
    else if (e.key === 'Home') go(0)
    else if (e.key === 'End') go(list.length - 1)
    else if (e.key === 'Tab') close()
  }
  const icon = current.kind === 'local' ? 'monitor' : 'server'
  const total = rows.length
  return (
    <span className="lnr-anchor">
      <button
        ref={buttonRef}
        type="button"
        className="lnr-switch"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        aria-label={S.switcher.label(current.name)}
        onClick={() => setOpen(!open)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
            e.preventDefault()
            setOpen(true)
          }
        }}
        data-testid="server-switcher"
      >
        <Icon name={icon} />
        <span className="lnr-switch__name">{current.name}</span>
        <Icon name="chevron-down" />
      </button>
      <Popover open={open} onClose={close} trigger={buttonRef} kind="menu" className="lnr-switcher" role="menu" label={S.switcher.title} onKeyDown={move}>
        <div id={menuId}>
          <div className="lnr-switcher__head" role="none">
            <strong>{S.switcher.title}</strong>
            <kbd className="lnr-kbd">{isMac() ? S.switcher.shortcutMac : S.switcher.shortcutWin}</kbd>
          </div>
          {rows.map((r, i) => (
            <div key={r.id} role="none">
              <button
                ref={(el) => {
                  refs.current[i] = el
                }}
                type="button"
                role="menuitemradio"
                aria-checked={r.isCurrent}
                tabIndex={-1}
                className="dk-menu-item lnr-switcher__row"
                onClick={() => {
                  close()
                  if (!r.isCurrent) onPick(r.id)
                  buttonRef.current?.focus()
                }}
                data-testid={`switch-${r.id}`}
              >
                <span className="lnr-switcher__tick" aria-hidden="true">{r.isCurrent ? <Icon name="check" /> : null}</span>
                <ServerDot row={r} />
                <span className="lnr-switcher__text">
                  <span className="lnr-switcher__name">{r.name}{r.isCurrent && <span className="dk-sr-only">{` (${S.switcher.current})`}</span>}</span>
                  <span className="lnr-switcher__line dk-small dk-muted">{switcherLine(r, (t) => clockTime(t))}</span>
                </span>
              </button>
            </div>
          ))}
          <hr className="dk-menu-sep" />
          <button
            ref={(el) => {
              refs.current[total] = el
            }}
            type="button"
            role="menuitem"
            tabIndex={-1}
            className="dk-menu-item"
            onClick={() => (close(), onAdd())}
          >
            {S.switcher.add}
          </button>
          <button
            ref={(el) => {
              refs.current[total + 1] = el
            }}
            type="button"
            role="menuitem"
            tabIndex={-1}
            className="dk-menu-item"
            onClick={() => (close(), onManage())}
          >
            {S.switcher.manage}
          </button>
          <p className="lnr-switcher__keys dk-small dk-muted" aria-hidden="true">
            <kbd className="lnr-kbd">↑</kbd> <kbd className="lnr-kbd">↓</kbd> {S.switcher.hint} · <kbd className="lnr-kbd">Enter</kbd> {S.switcher.open} · <kbd className="lnr-kbd">Esc</kbd> {S.switcher.close}
          </p>
        </div>
      </Popover>
    </span>
  )
}
