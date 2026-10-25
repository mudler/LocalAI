import { gbLabel } from '../../../utils/modelLedger'
import { contextLabel, splitByContext } from '../../../utils/placement'

// The segments of the bar for one model at one context, from the figures the
// estimate returned for it.
//
// The estimate gives one number per context length. Its context term is linear,
// so two lengths give the part that grows with context exactly (see
// splitByContext). With a single reading the bar is one undivided segment.
export function memorySegments(estimate, contextSize, need, t) {
  const points = Object.entries(estimate?.estimates || {})
    .map(([ctx, point]) => ({ ctx: Number(ctx), bytes: Number(point.vramBytes) || 0 }))
    .filter(point => point.bytes > 0)
    .sort((a, b) => a.ctx - b.ctx)
  const here = points.find(point => point.ctx === contextSize)
  const other = points.find(point => point.ctx !== contextSize)
  const parts = here && other
    ? (other.ctx > here.ctx ? splitByContext(here, other) : splitByContext(other, here))
    : null
  if (!parts) return [{ key: 'model', label: t('placement.legend.model'), bytes: need, tone: 'model' }]
  // kv is the term at the first point of the pair; scale it to this context.
  const kv = Math.min(need, parts.slope * contextSize)
  return [
    { key: 'weights', label: t('page.fit.weights'), bytes: need - kv, tone: 'model' },
    { key: 'kv', label: t('placement.legend.kv'), bytes: kv, tone: 'kv' },
  ]
}

// Words for the one thing the strip says about fit. The states are the ones
// fitFor() returns, and the sentence repeats what the bar below shows.
export function fitWords(view) {
  const { fit, t, contextSize, budget } = view
  if (!fit) return null
  const context = contextLabel(contextSize)
  const need = gbLabel(fit.need)
  if (fit.state === 'fits') return { title: budget.hasGpu ? t('page.fit.fitsGpu') : t('page.fit.fitsMemory'), text: t('page.fit.fitsText', { need, free: gbLabel(fit.limit), context, spare: gbLabel(fit.amount) }), tone: 'ok' }
  if (fit.state === 'spill') return { title: t('page.fit.spill'), text: t('ledger.summary.spill', { need, cpu: gbLabel(fit.amount), context }), tone: 'warn' }
  return { title: t('page.fit.over'), text: t(budget.hasGpu ? 'ledger.summary.overGpu' : 'ledger.summary.overRam', { need, over: gbLabel(fit.amount), context }), tone: 'error' }
}

