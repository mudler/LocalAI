import { fitStyle, gbLabel, gbNumber } from '../../utils/modelLedger'
import Icon from '../Icon'

// What a row says about this machine: a solid bar, the memory the model needs
// at the chosen context, and the headroom in words ("3.7 free", "+1.5 on CPU",
// "0.9 over"). The words are the answer; the bar and its colour repeat it.
//
// fit is the result of fitFor(), or null while there is no estimate.
export default function FitCell({ fit, pending, t, contextLabel }) {
  if (!fit) {
    return (
      <span className="ledger-fit ledger-fit--none" data-fit="none">
        {pending ? t('rail.sizing') : <span aria-label={t('ledger.fit.noEstimate')}>&mdash;</span>}
      </span>
    )
  }
  const amount = gbNumber(fit.amount)
  const words = fit.state === 'fits'
    ? t('ledger.fit.free', { amount })
    : fit.state === 'spill'
      ? t('ledger.fit.spill', { amount })
      : t('ledger.fit.over', { amount })
  const explain = t(`ledger.fit.${fit.state}Title`, { amount: gbLabel(fit.amount), context: contextLabel, need: gbLabel(fit.need) })
  return (
    <span className="ledger-fit" data-fit={fit.state} title={explain}>
      <span className="ledger-fit__line">
        <span className="ledger-fit__bar" aria-hidden="true">
          <span className="ledger-fit__fill" style={fitStyle(fit.fraction)} />
        </span>
        <span className="ledger-fit__need">{gbLabel(fit.need)}</span>
      </span>
      <span className="ledger-fit__words">
        {fit.state !== 'fits' && <Icon name="alert-circle" className="ledger-fit__icon" />}
        {words}
        <span className="dk-sr-only"> {explain}</span>
      </span>
    </span>
  )
}
