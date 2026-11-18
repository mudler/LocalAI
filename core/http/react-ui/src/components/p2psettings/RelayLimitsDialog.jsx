/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import ConfirmDialog from '../ConfirmDialog'
import CommandBlock from '../swarm/CommandBlock'
import Icon from '../Icon'
import { Button, Callout, Segmented } from '../ui'
import { defaultLimits, limitsCommand, limitsFromRelay, relayName, validLimits } from './p2pSettingsModel'

const FIELDS = [
  { key: 'dataGiB', unit: 'gib' },
  { key: 'minutes', unit: 'minutes' },
  { key: 'kibps', unit: 'kibps' },
  { key: 'circuits', unit: 'circuits' },
]

// The limits of one relay. A relay reads its limits when it starts, and the
// relay admin protocol has no operation to change them, so the dialog builds
// the start command with the chosen limits: the person runs the relay again
// with it. Choosing No limit on an open relay asks first, because one
// stranger could then use all its bandwidth.
export default function RelayLimitsDialog({ relay, peerID, onClose, addToast }) {
  const { t } = useTranslation('p2psettings')
  const open = relay.mode === 'open' || relay.state === 'open'
  const [form, setForm] = useState(() => limitsFromRelay(relay))
  const [noLimit, setNoLimit] = useState(false)
  const [asking, setAsking] = useState(false)
  const name = relayName(relay)
  const valid = noLimit || validLimits(form)

  const choose = (id) => {
    if (id === 'none' && !noLimit) {
      if (open) setAsking(true)
      else setNoLimit(true)
      return
    }
    if (id === 'limited') setNoLimit(false)
  }

  return (
    <>
      <Dialog
        title={<><Icon name="sliders" /> {t('limits.title', { name })}</>}
        description={open ? t('limits.ledeOpen') : t('limits.ledeGated')}
        onClose={onClose} pending={asking} closeLabel={t('actions.close')} testId="relay-limits-dialog" labelId="relay-limits-title"
        foot={(
          <>
            <Button variant="ghost" className="p2ps-foot-note" onClick={() => { setForm(defaultLimits(open)); setNoLimit(false) }} data-testid="relay-limits-reset">{t('limits.reset')}</Button>
            <Button variant="primary" onClick={onClose}>{t('actions.done')}</Button>
          </>
        )}
      >
        <div className="p2ps-section">
          {open && !noLimit && <Callout tone="warn" title={t('limits.openWarnTitle')}>{t('limits.openWarnBody')}</Callout>}
          {noLimit && <Callout tone={open ? 'error' : 'warn'} title={t('limits.noLimitTitle')} testId="relay-no-limit-warning">{open ? t('limits.noLimitOpen') : t('limits.noLimitGated')}</Callout>}
          <div className="dk-field">
            <span className="dk-label" id="relay-limits-mode">{t('limits.mode')}</span>
            <Segmented
              mode="radio" labelledBy="relay-limits-mode" value={noLimit ? 'none' : 'limited'} onChange={choose}
              options={[{ id: 'limited', label: t('limits.limited') }, { id: 'none', label: t('limits.none') }]}
              testId="relay-limits-mode"
            />
          </div>
          {!noLimit && (
            <div className="p2ps-limits">
              {FIELDS.map(f => (
                <div key={f.key} className="dk-field">
                  <label className="dk-label" htmlFor={`relay-limit-${f.key}`}>{t(`limits.fields.${f.key}`)}</label>
                  <span className="p2ps-unit">
                    <input
                      id={`relay-limit-${f.key}`} className="dk-input" inputMode="decimal" value={form[f.key]}
                      aria-invalid={!validLimits({ ...defaultLimits(open), [f.key]: form[f.key] }) || undefined}
                      onChange={(e) => setForm(prev => ({ ...prev, [f.key]: e.target.value }))}
                    />
                    <span className="p2ps-unit__label">{t(`units.${f.unit}`)}</span>
                  </span>
                </div>
              ))}
            </div>
          )}
          <p className="dk-hint">{t('limits.zeroHint')}</p>
          <p className="p2ps-step-title">{t('limits.commandTitle')}</p>
          {valid
            ? <CommandBlock command={limitsCommand({ peerID, open, form, noLimit })} label={t('limits.commandTitle')} addToast={addToast} />
            : <p className="dk-hint" role="alert">{t('limits.invalid')}</p>}
          <p className="dk-hint">{t('limits.applyHint')}</p>
        </div>
      </Dialog>
      <ConfirmDialog
        open={asking} danger title={t('limits.confirmTitle')}
        message={t('limits.confirmBody', { name })}
        confirmLabel={t('limits.confirmRemove')} cancelLabel={t('limits.confirmKeep')}
        onConfirm={() => { setNoLimit(true); setAsking(false) }}
        onCancel={() => setAsking(false)}
      />
    </>
  )
}
