import { useEffect, useRef, type RefObject } from 'react'
import { Icon } from '@localai/ui-kit'
import { Popover } from './Popover'
import { S } from './strings'

export type MenuAction = 'settings' | 'connect' | 'open-in-browser' | 'about'

interface Item {
  id: MenuAction
  label: string
  end?: React.ReactNode
  sepBefore?: boolean
}

export function menuItems(opts: { remote: boolean; canOpenInBrowser: boolean; serverVersion?: string }): Item[] {
  const items: Item[] = []
  items.push({ id: 'settings', label: S.menu.settings })
  items.push({ id: 'connect', label: S.menu.connect })
  if (opts.canOpenInBrowser) items.push({ id: 'open-in-browser', label: S.menu.openInBrowser, end: <Icon name="external-link" />, sepBefore: true })
  items.push({ id: 'about', label: S.menu.about, end: opts.serverVersion, sepBefore: true })
  return items
}

/** The bar menu: a WAI-ARIA menu. Arrow keys, Home and End move; Enter or Space picks; Escape and Tab close. */
export function BarMenu({
  open,
  onClose,
  trigger,
  items,
  onPick,
  id,
}: {
  open: boolean
  onClose: () => void
  trigger: RefObject<HTMLButtonElement | null>
  items: Item[]
  onPick: (a: MenuAction) => void
  id: string
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  useEffect(() => {
    if (open) refs.current[0]?.focus()
  }, [open])
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
    else if (e.key === 'Tab') onClose()
  }
  return (
    <Popover open={open} onClose={onClose} trigger={trigger} kind="menu" className="dk-menu--end lnr-menu" role="menu" label={S.bar.menu} onKeyDown={move}>
      <div id={id}>
        {items.map((it, i) => (
          <div key={it.id} role="none">
            {it.sepBefore && <hr className="dk-menu-sep" />}
            <button
              ref={(el) => {
                refs.current[i] = el
              }}
              type="button"
              role="menuitem"
              tabIndex={-1}
              className="dk-menu-item"
              onClick={() => {
                onClose()
                onPick(it.id)
              }}
            >
              <span>{it.label}</span>
              {it.end !== undefined && <span className="dk-menu-item-end">{it.end}</span>}
            </button>
          </div>
        ))}
      </div>
    </Popover>
  )
}
