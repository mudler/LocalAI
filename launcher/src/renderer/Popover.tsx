import { useEffect, useRef, type ReactNode, type RefObject } from 'react'

/**
 * A surface that opens upward from the bar (the kit's dk-menu or dk-popover).
 * Escape and a click outside close it; closing by Escape puts the focus back on
 * the control that opened it.
 */
export function Popover({
  open,
  onClose,
  trigger,
  kind,
  className = '',
  label,
  labelledBy,
  role,
  children,
  onKeyDown,
}: {
  open: boolean
  onClose: () => void
  trigger: RefObject<HTMLElement | null>
  kind: 'menu' | 'popover'
  className?: string
  label?: string
  labelledBy?: string
  role?: string
  children: ReactNode
  onKeyDown?: (e: React.KeyboardEvent<HTMLDivElement>) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const down = (e: MouseEvent) => {
      const t = e.target as Node
      if (ref.current?.contains(t) || trigger.current?.contains(t)) return
      onClose()
    }
    document.addEventListener('mousedown', down)
    return () => document.removeEventListener('mousedown', down)
  }, [open, onClose, trigger])
  if (!open) return null
  return (
    <div
      ref={ref}
      className={`dk-${kind} dk-${kind}--up lnr-pop ${className}`.trim()}
      role={role}
      aria-label={label}
      aria-labelledby={labelledBy}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.stopPropagation()
          onClose()
          trigger.current?.focus()
          return
        }
        onKeyDown?.(e)
      }}
    >
      {children}
    </div>
  )
}
