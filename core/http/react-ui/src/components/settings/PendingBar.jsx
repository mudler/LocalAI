import Icon from '../Icon'
import { displayValue, restartChanges } from '../../utils/settingsPending'

// The bar that appears when the page holds edits the server has not seen. It
// says how many, offers Discard, Show diff and Apply, and opens a diff of the
// old and new values with the checks the browser can make on them.
//
//   changes   visible changes (see settingsPending.visibleChanges)
//   checks    [{ level, text }]; an 'error' disables Apply
export default function PendingBar({ changes, checks, saving, showDiff, onToggleDiff, onApply, onDiscard }) {
  const restart = restartChanges(changes).length
  const blocked = checks.some(c => c.level === 'error')
  return (
    <section className="st-pending" aria-label="Pending changes" data-testid="settings-pending">
      {showDiff && (
        <div className="st-diff" data-testid="settings-diff">
          <ul className="st-diff__rows">
            {changes.map(({ field, from, to }) => (
              <li key={field.key} className="st-diff__row">
                <div className="st-diff__what">
                  <span className="st-diff__label">{field.label}</span>
                  <code className="st-key">{field.key}</code>
                </div>
                <div className="st-diff__values">
                  <del className="dk-mono">{displayValue(field, from)}</del>
                  <Icon name="arrow-right" aria-hidden="true" />
                  <strong className="dk-mono">{displayValue(field, to)}</strong>
                  {field.apply === 'restart'
                    ? <span className="st-tag" data-tone="warn">Needs restart</span>
                    : field.apply === 'live' ? <span className="st-tag">Applies now</span> : null}
                </div>
              </li>
            ))}
          </ul>
          {checks.length > 0 && (
            <ul className="st-checks" aria-label="Checks">
              {checks.map((c, i) => (
                <li key={i} className="st-checks__item" data-level={c.level}>
                  <Icon name={c.level === 'ok' ? 'check' : c.level === 'warn' ? 'warning' : 'alert-circle'} aria-hidden="true" />
                  <span>{c.text}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      <div className="st-pending__bar">
        <p className="st-pending__count">
          <strong>{changes.length} pending</strong>
          {restart > 0 && <span className="st-pending__restart"> · {restart} need{restart === 1 ? 's' : ''} a restart</span>}
          {blocked && <span className="st-pending__blocked"> · fix the errors to apply</span>}
        </p>
        <div className="st-pending__acts">
          <button type="button" className="dk-btn dk-btn--ghost" onClick={onDiscard} disabled={saving}>Discard</button>
          <button type="button" className="dk-btn dk-btn--secondary" aria-expanded={showDiff} onClick={onToggleDiff}>{showDiff ? 'Hide diff' : 'Show diff'}</button>
          <button type="button" className="dk-btn dk-btn--primary" onClick={onApply} disabled={saving || blocked} aria-busy={saving || undefined}>
            {saving ? 'Applying…' : 'Apply'}
          </button>
        </div>
      </div>
    </section>
  )
}
