import { useEffect, useRef, type ReactNode } from 'react'

const FOCUSABLE = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * A modal dialog. It traps the Tab key, closes on Escape and gives the focus
 * back to the control that had it before. `initialFocus` names the element
 * that gets the focus first (by selector inside the dialog); without it the
 * first control does.
 */
export function Dialog({
  title,
  titleId,
  onClose,
  initialFocus,
  children,
  className = '',
  testId,
  role = 'dialog',
}: {
  title: ReactNode
  titleId: string
  onClose: () => void
  initialFocus?: string
  children: ReactNode
  className?: string
  testId?: string
  role?: 'dialog' | 'alertdialog'
}) {
  const ref = useRef<HTMLDivElement>(null)
  const opener = useRef<Element | null>(typeof document === 'undefined' ? null : document.activeElement)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const first = (initialFocus ? el.querySelector<HTMLElement>(initialFocus) : null) ?? el.querySelector<HTMLElement>(FOCUSABLE)
    first?.focus()
    const back = opener.current
    return () => {
      if (back instanceof HTMLElement && back.isConnected) back.focus()
    }
    // The dialog is mounted only while it is open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onClose()
      return
    }
    if (e.key !== 'Tab') return
    const list = [...(ref.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])]
    if (list.length === 0) return
    const at = list.indexOf(document.activeElement as HTMLElement)
    const next = e.shiftKey ? (at <= 0 ? list.length - 1 : at - 1) : at === list.length - 1 ? 0 : at + 1
    e.preventDefault()
    list[next]?.focus()
  }
  return (
    <div className="lnr-scrim" data-testid={testId ? `${testId}-scrim` : undefined}>
      <div ref={ref} className={`lnr-dialog ${className}`.trim()} role={role} aria-modal="true" aria-labelledby={titleId} onKeyDown={onKeyDown} data-testid={testId}>
        <h2 id={titleId} className="lnr-dialog__title">{title}</h2>
        {children}
      </div>
    </div>
  )
}
