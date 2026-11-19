/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import { Button, Callout, LoadError, Segmented, SkeletonRows } from '../ui'
import { p2pRelaysApi } from '../../utils/api'
import { defaultLimits, hasCeiling, hasLimits, limitsFromReported, limitsIssues, limitsMaxText, limitsPatch, relayName, validLimits } from './p2pSettingsModel'
// The dialog also opens from the Nodes page, which does not load the P2P
// settings styles.
import './p2psettings.css'

const FIELDS = [
  { key: 'dataGiB', unit: 'gib' },
  { key: 'minutes', unit: 'minutes' },
  { key: 'kibps', unit: 'kibps' },
]

// The limits of one relay: the data, the time and the bandwidth of one
// circuit. The dialog reads them from the relay and, when this server is an
// admin of a gated relay, saves a change to it. A new bandwidth limit applies
// to the next circuit; a new data or time limit makes the relay restart its
// circuit service, and the dialog says so. A relay this server does not
// administer (an open relay, or one where it is only a member) shows its
// limits and cannot change them. The operator of the relay sets a ceiling for
// each limit with its start flags: the dialog shows it next to the field,
// checks the form against it before it sends, and shows the error of the
// relay if the relay refuses anyway. A limit with a ceiling cannot be
// removed, so a relay with a ceiling shows no Mode switch and no No limit. The
// warning for No limit stays: one paired peer could then use all the
// bandwidth of the relay.
export default function RelayLimitsDialog({ relay, onClose, onSaved, addToast }) {
  const { t } = useTranslation('p2psettings')
  const open = relay.mode === 'open' || relay.state === 'open'
  const [info, setInfo] = useState(null)
  const [error, setError] = useState(null)
  const [form, setForm] = useState(() => limitsFromReported(relay.limits))
  const [noLimit, setNoLimit] = useState(() => !hasLimits(relay.limits))
  const [saving, setSaving] = useState(false)
  const [refused, setRefused] = useState('')
  const name = relayName(relay)

  const load = useCallback(async () => {
    setError(null)
    try {
      const got = await p2pRelaysApi.limits(relay.id)
      setInfo(got)
      setForm(limitsFromReported(got.limits))
      setNoLimit(!hasLimits(got.limits) && !hasCeiling(got.ceiling))
    } catch (err) {
      setError(err)
    }
  }, [relay.id])

  useEffect(() => { load() }, [load])

  const canChange = !!info?.can_change
  const ceiling = info?.ceiling
  const capped = hasCeiling(ceiling)
  const numbersOk = noLimit || validLimits(form)
  const issues = numbersOk ? limitsIssues({ form, noLimit, ceiling }) : {}
  const valid = numbersOk && Object.keys(issues).length === 0

  const save = async () => {
    setSaving(true)
    setRefused('')
    try {
      const res = await p2pRelaysApi.setLimits(relay.id, limitsPatch({ form, noLimit }))
      addToast?.(t(res.restarted ? 'limits.savedRestarted' : 'limits.saved', { name }), 'success')
      await onSaved?.()
      onClose()
    } catch (err) {
      setRefused(err.message)
      setSaving(false)
    }
  }

  return (
    <Dialog
      title={<><Icon name="sliders" /> {t('limits.title', { name })}</>}
      description={open ? t('limits.ledeOpen') : t('limits.ledeGated')}
      onClose={onClose} pending={saving} closeLabel={t('actions.close')} testId="relay-limits-dialog" labelId="relay-limits-title"
      foot={(
        <>
          {canChange && <Button variant="ghost" className="p2ps-foot-note" disabled={saving} onClick={() => { setForm(defaultLimits(open, ceiling)); setNoLimit(false); setRefused('') }} data-testid="relay-limits-reset">{t('limits.reset')}</Button>}
          {canChange
            ? (
              <>
                <Button variant="ghost" disabled={saving} onClick={onClose}>{t('actions.cancel')}</Button>
                <Button variant="primary" busy={saving} disabled={!valid} onClick={save} data-testid="relay-limits-save">{t('limits.save')}</Button>
              </>
            )
            : <Button variant="primary" onClick={onClose}>{t('actions.done')}</Button>}
        </>
      )}
    >
      <div className="p2ps-section">
        {error && <LoadError title={t('limits.loadError')} error={error} onRetry={load} />}
        {!error && !info && <SkeletonRows />}
        {!error && info && (
          <>
            {!canChange && <Callout tone="info" title={t('limits.readOnlyTitle')} testId="relay-limits-readonly">{open ? t('limits.readOnlyOpen') : t('limits.readOnlyMember')}</Callout>}
            {capped && canChange && <Callout tone="info" title={t('limits.ceilingTitle')} testId="relay-limits-ceiling">{t('limits.ceilingBody')}</Callout>}
            {noLimit && <Callout tone={open ? 'error' : 'warn'} title={t('limits.noLimitTitle')} testId="relay-no-limit-warning">{open ? t('limits.noLimitOpen') : t('limits.noLimitGated')}</Callout>}
            {!capped && (
              <div className="dk-field">
                <span className="dk-label" id="relay-limits-mode">{t('limits.mode')}</span>
                <Segmented
                  mode="radio" labelledBy="relay-limits-mode" value={noLimit ? 'none' : 'limited'} onChange={(id) => canChange && setNoLimit(id === 'none')}
                  options={[{ id: 'limited', label: t('limits.limited') }, { id: 'none', label: t('limits.none') }]}
                  testId="relay-limits-mode"
                />
              </div>
            )}
            {!noLimit && (
              <div className="p2ps-limits">
                {FIELDS.map(f => (
                  <div key={f.key} className="dk-field">
                    <label className="dk-label" htmlFor={`relay-limit-${f.key}`}>{t(`limits.fields.${f.key}`)}</label>
                    <span className="p2ps-unit">
                      <input
                        id={`relay-limit-${f.key}`} className="dk-input" inputMode="decimal" value={form[f.key]} disabled={!canChange || saving}
                        aria-invalid={!validLimits({ ...defaultLimits(open), [f.key]: form[f.key] }) || !!issues[f.key] || undefined}
                        aria-describedby={`relay-limit-${f.key}-max`}
                        onChange={(e) => { setRefused(''); setForm(prev => ({ ...prev, [f.key]: e.target.value })) }}
                      />
                      <span className="p2ps-unit__label">{t(`units.${f.unit}`)}</span>
                    </span>
                    <span id={`relay-limit-${f.key}-max`} className="dk-hint" data-testid={`relay-limit-${f.key}-max`}>
                      {limitsMaxText(f.key, ceiling) ? t('limits.max', { value: limitsMaxText(f.key, ceiling) }) : t('limits.noMax')}
                    </span>
                    {issues[f.key] && <span className="dk-field-error" role="alert" data-testid={`relay-limit-${f.key}-issue`}>{t(`limits.issue.${issues[f.key]}`, { max: limitsMaxText(f.key, ceiling) })}</span>}
                  </div>
                ))}
              </div>
            )}
            {canChange && (
              <>
                {!capped && <p className="dk-hint">{t('limits.zeroHint')}</p>}
                {!numbersOk && <p className="dk-hint" role="alert">{t('limits.invalid')}</p>}
                {refused && <Callout tone="error" title={t('limits.refusedTitle')} testId="relay-limits-refused">{refused}</Callout>}
                <p className="dk-hint">{t('limits.applyHint')}</p>
              </>
            )}
          </>
        )}
      </div>
    </Dialog>
  )
}
