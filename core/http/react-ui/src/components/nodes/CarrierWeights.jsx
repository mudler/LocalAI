import { useEffect, useRef, useState } from 'react'
import { clusterApi } from '../../utils/api'
import { CARRIERS, defaultWeights, resetPayload, weightErrors, weightsInForce, weightsPayload, WEIGHT_MAX, WEIGHT_MIN } from '../../utils/carrierWeights'
import { Button } from '../ui'

function toDraft(weights) {
  return Object.fromEntries(CARRIERS.map(({ key }) => [key, String(weights[key])]))
}

// CarrierWeights sets how much the router prefers a worker that a frontend
// reaches over NATS, WebSocket or P2P when two workers fit a request equally
// well. It never overrides fit. A save reaches every frontend within seconds.
export default function CarrierWeights({ settings, onSaved }) {
  const current = weightsInForce(settings)
  const defaults = defaultWeights(settings)
  const [draft, setDraft] = useState(() => toDraft(current))
  const [saving, setSaving] = useState(false)
  const [result, setResult] = useState(null)
  const touched = useRef(false)
  const currentKey = CARRIERS.map(({ key }) => current[key]).join(',')

  // Follow the stored values until the admin starts typing.
  useEffect(() => {
    if (touched.current) return
    setDraft(toDraft(weightsInForce(settings)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentKey])

  const errors = weightErrors(draft)
  const payload = weightsPayload(draft, current)
  const reset = resetPayload(current, defaults)

  const send = async (body, okText) => {
    setSaving(true)
    setResult(null)
    try {
      await clusterApi.saveSettings(body)
      touched.current = false
      setResult({ ok: true, text: okText })
      onSaved()
    } catch (error) {
      setResult({ ok: false, text: error.message || 'The weights were not saved' })
    } finally {
      setSaving(false)
    }
  }

  const save = event => {
    event.preventDefault()
    if (payload) send(payload, 'Saved. Every frontend uses the new weights within a few seconds.')
  }

  const useDefaults = () => {
    if (!reset) return
    touched.current = false
    setDraft(toDraft(defaults))
    send(reset, 'The default weights are back. Every frontend uses them within a few seconds.')
  }

  return (
    <form className="transport-settings" onSubmit={save} aria-label="Carrier weights" data-testid="carrier-weights">
      <p className="form-label">Carrier weights</p>
      <p className="text-note">
        When two workers fit a request equally well, the router picks the one reached over the carrier with the higher weight.
        A weight never overrides fit. 0 means: use that carrier only when nothing else fits.
      </p>
      <div className="transport-weights">
        {CARRIERS.map(({ key, label }) => (
          <label className="transport-field" key={key}>
            <span>{label} (default {defaults[key]})</span>
            <input
              className="input input-mono"
              type="number"
              inputMode="numeric"
              min={WEIGHT_MIN}
              max={WEIGHT_MAX}
              step={1}
              aria-label={`${label} weight`}
              aria-invalid={errors[key] ? 'true' : undefined}
              value={draft[key]}
              onChange={event => { touched.current = true; setDraft(previous => ({ ...previous, [key]: event.target.value })) }}
            />
            {errors[key] && <span className="transport-error" role="alert" data-testid={`carrier-weight-error-${key}`}>{errors[key]}</span>}
          </label>
        ))}
      </div>
      <div className="transport-dialog__actions">
        <Button type="submit" size="sm" disabled={saving || !payload}>{saving ? 'Saving…' : 'Apply weights'}</Button>
        <Button type="button" size="sm" variant="ghost" disabled={saving || !reset} onClick={useDefaults}>Use defaults</Button>
      </div>
      {result && <p className={result.ok ? 'transport-ok' : 'transport-error'} role="status" data-testid="carrier-weights-result">{result.text}</p>}
    </form>
  )
}
