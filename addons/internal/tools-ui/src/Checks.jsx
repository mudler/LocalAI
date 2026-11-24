import { formatBytes } from './format.js'

const ICON = { ok: 'check-circle', warn: 'warning', fail: 'alert-circle', info: 'info' }

// Byte counts arrive raw; the text shows them as sizes.
function withSizes(vals) {
  const out = { ...(vals || {}) }
  for (const key of ['free', 'total', 'used']) {
    if (typeof out[key] === 'number') out[key] = formatBytes(out[key])
    else if (key in out) out[key] = '?'
  }
  return out
}

function valueText(value) {
  if (!value) return ''
  if (typeof value === 'string') return value
  return `${formatBytes(value.free)} / ${formatBytes(value.total)}`
}

// A checklist: one row per check with a mark, a title, one sentence, an optional
// value and an optional meter of how much of a resource is in use. Rows are
// computed from facts the server reported, so the list can be redrawn on every
// change of the form. The backends page belongs to the host, so the link
// leaves the add-on.
export function createChecks(ctx) {
  const { h, Fragment, t, Icon } = ctx

  // The host translator picks a variant by context (`key_gpu`); the SDK one has
  // no such rule, so the variant is tried first and the plain key second.
  const withContext = (key, vars, context) => {
    if (context) {
      const variant = `${key}_${context}`
      const text = t(variant, vars)
      if (text !== variant) return text
    }
    return t(key, vars)
  }

  function Checks({ checks, isAdmin = false, label, testId = 'tool-checks' }) {
    return (
      <ul className="bt-checks" aria-label={label} data-testid={testId}>
        {checks.map(check => {
          const vals = withSizes(check.vals)
          const used = check.meter?.total > 0 ? Math.min(100, Math.round((check.meter.used / check.meter.total) * 100)) : null
          return (
            <li key={check.id} className="bt-check" data-tone={check.tone} data-check={check.id}>
              <span className="bt-check__mark"><Icon name={ICON[check.tone] || 'info'} /></span>
              <div className="bt-check__main">
                <div className="bt-check__head">
                  <span className="bt-check__title">{t(`checks.title.${check.id}`)}</span>
                  {check.value && <span className="bt-check__value dk-mono">{valueText(check.value)}</span>}
                </div>
                <p className="bt-check__text">
                  {withContext(`checks.${check.key}`, vals, check.vals?.kind || check.vals?.where)}
                  {check.action === 'backends' && isAdmin && (
                    <> <a className="dk-link" href={`${ctx.basePath}/app/backends`}>{t('checks.openBackends')}</a></>
                  )}
                </p>
                {used != null && (
                  <div
                    className="dk-meter bt-check__meter"
                    role="img"
                    aria-label={t('checks.meterLabel', { used, free: formatBytes(Math.max(0, check.meter.total - check.meter.used)), total: formatBytes(check.meter.total) })}
                  >
                    <span className={`dk-meter-seg${check.id === 'disk' ? ' dk-meter-seg--b' : ''}`} style={{ '--dk-w': `${used}%` }} />
                  </div>
                )}
              </div>
            </li>
          )
        })}
      </ul>
    )
  }
  return Checks
}
