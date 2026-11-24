// Small layout pieces the screens share. Controls come from the kit; these only
// arrange them (a settings row, a list of steps, a code block).
import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Button, Icon } from '@localai/ui-kit'
import type { StepView } from './viewModel'

/** One settings row: name and note on the left, the control on the right. */
export function Field({ name, note, children, wide, id }: { name: ReactNode; note?: ReactNode; children?: ReactNode; wide?: boolean; id?: string }) {
  const auto = useId()
  const base = id ?? auto
  return (
    <div className={`lnr-field${wide ? ' lnr-field--wide' : ''}`}>
      <div className="lnr-field__name">
        <span id={`${base}-name`} className="lnr-field__label">{name}</span>
        {note && <span id={`${base}-note`} className="lnr-field__note">{note}</span>}
      </div>
      {children}
    </div>
  )
}

/** The id pair Field gives its name and note, for a control's labelledBy and describedBy. */
export function fieldIds(id: string) {
  return { labelledBy: `${id}-name`, describedBy: `${id}-note` }
}

export function Card({ title, children, className = '' }: { title?: string; children: ReactNode; className?: string }) {
  return (
    <section className={`dk-card dk-card--tight lnr-card ${className}`.trim()} aria-label={title}>
      {title && <h3 className="dk-eyebrow lnr-card__title">{title}</h3>}
      {children}
    </section>
  )
}

const MARK: Record<StepView['status'], string> = { done: 'lnr-step__mark--done', active: 'lnr-step__mark--active', todo: 'lnr-step__mark--todo' }

/** A numbered list of steps: done steps get a tick, the active one a bar when it has a percent. */
export function StepList({ steps, label }: { steps: StepView[]; label?: string }) {
  return (
    <ol className="lnr-steps" aria-label={label}>
      {steps.map((s, i) => (
        <li key={i} className="lnr-step" aria-current={s.status === 'active' ? 'step' : undefined}>
          <div className="lnr-step__row">
            <span className={`lnr-step__mark ${MARK[s.status]}`} aria-hidden="true">
              {s.status === 'done' ? <Icon name="check" /> : i + 1}
            </span>
            <span className={s.status === 'todo' ? 'dk-muted' : undefined}>
              {s.status === 'active' ? <strong>{s.label}</strong> : s.label}
              {s.note && <span className="dk-muted"> {s.note}</span>}
            </span>
          </div>
          {s.status === 'active' && (
            <div
              className="dk-progress lnr-step__progress"
              role="progressbar"
              aria-label={s.label}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={s.percent}
              data-indeterminate={s.percent === undefined ? '' : undefined}
            >
              <span className="dk-progress-bar" style={s.percent !== undefined ? { width: `${s.percent}%` } : undefined} />
            </div>
          )}
        </li>
      ))}
    </ol>
  )
}

/** A Copy button that says "Copied" for a moment. */
export function CopyButton({ onCopy, label, copied, size = 'sm', variant = 'secondary' }: { onCopy: () => Promise<void> | void; label: string; copied: string; size?: 'sm' | 'md'; variant?: 'secondary' | 'ghost' }) {
  const [done, setDone] = useState(false)
  const t = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(t.current), [])
  return (
    <Button
      size={size}
      variant={variant}
      icon={done ? 'check' : 'copy'}
      onClick={async () => {
        await onCopy()
        setDone(true)
        clearTimeout(t.current)
        t.current = setTimeout(() => setDone(false), 1500)
      }}
    >
      <span aria-live="polite">{done ? copied : label}</span>
    </Button>
  )
}

/** Monospace text in an inset box. */
export function Code({ children, label }: { children: ReactNode; label?: string }) {
  return (
    <pre className="lnr-code dk-mono" aria-label={label} tabIndex={0}>
      {children}
    </pre>
  )
}
