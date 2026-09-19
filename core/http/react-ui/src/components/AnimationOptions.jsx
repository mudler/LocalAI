// SPDX-License-Identifier: MIT
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { fileToBase64 } from '../utils/api'

function AnimationInput({ input, value, onChange }) {
  const { t } = useTranslation('media')
  const [error, setError] = useState('')
  const selection = useRef(0)
  useEffect(() => () => { selection.current++ }, [])
  const id = `animation-${input.name}`
  const data = value?.data || ''
  const tooLong = input.type === 'text' && input.max_bytes && new TextEncoder().encode(data).length > input.max_bytes
  const byteError = tooLong ? t('threed.animation.inputTooLong', 'Input exceeds the {{limit}} byte limit.', { limit: input.max_bytes }) : ''
  return (
    <div className="form-group">
      <label className="form-label" htmlFor={id}>{input.label}</label>
      {input.type === 'text' ? <textarea
        id={id} className="input" rows={4} required={input.required} value={data}
        ref={element => element?.setCustomValidity(byteError)}
        aria-invalid={!!byteError}
        onChange={event => onChange({ type: input.type, data: event.target.value })}
      /> : <input
        id={id} className="input" type="file" required={input.required && !data}
        accept={{ image: 'image/*', video: 'video/*', mesh: '.glb,.gltf,.obj,.ply,.stl' }[input.type]}
        onChange={async event => {
          const current = ++selection.current
          const file = event.target.files?.[0]
          setError('')
          onChange(null)
          if (!file) return
          try {
            if (file.size > (input.max_bytes || 32 * 1024 * 1024)) throw new Error(t('threed.animation.fileTooLarge', 'The selected file is too large.'))
            const encoded = await fileToBase64(file)
            if (current === selection.current) onChange({ type: input.type, data: encoded })
          } catch (err) {
            if (current === selection.current) setError(err?.message || t('threed.animation.readFailed', 'Could not read the selected file.'))
          }
        }}
      />}
      {(byteError || error) && <p role="alert" className="form-error">{byteError || error}</p>}
    </div>
  )
}

export default function AnimationOptions({ operation, inputs, onInputsChange, params, onParamsChange }) {
  const { t } = useTranslation('media')
  const [advanced, setAdvanced] = useState(false)
  return (
    <div className="stack">
      {operation.inputs.map(input => (
        <AnimationInput key={input.name} input={input} value={inputs[input.name]}
          onChange={value => onInputsChange(previous => {
            const next = { ...previous }
            if (value) next[input.name] = value
            else delete next[input.name]
            return next
          })} />
      ))}
      <div className="form-grid-2col">
        {operation.parameters.filter(parameter => !parameter.advanced || advanced).map(parameter => (
          <div className="form-group" key={parameter.name}>
            <label className="form-label" htmlFor={`animation-${parameter.name}`}>{parameter.label}</label>
            {parameter.type === 'enum' ? <select
              id={`animation-${parameter.name}`} className="input"
              value={params[parameter.name] ?? parameter.default ?? ''}
              onChange={event => onParamsChange({ ...params, [parameter.name]: event.target.value })}
            >
              {parameter.options.map(value => <option key={value} value={value}>{value}</option>)}
            </select> : <input
              id={`animation-${parameter.name}`}
              className="input"
              type={parameter.type === 'uint64' ? 'text' : 'number'}
              inputMode={parameter.type === 'uint64' ? 'numeric' : undefined}
              pattern={parameter.type === 'uint64' ? '[0-9]{1,20}' : undefined}
              min={parameter.min ?? 0}
              max={parameter.max || undefined}
              step={parameter.type === 'integer' ? 1 : 'any'}
              placeholder={parameter.default}
              value={params[parameter.name] ?? ''}
              onChange={event => onParamsChange({ ...params, [parameter.name]: event.target.value })}
            />}
          </div>
        ))}
      </div>
      <button type="button" className={`collapsible-header ${advanced ? 'open' : ''}`} aria-expanded={advanced} onClick={() => setAdvanced(!advanced)}>
        <i className="fas fa-chevron-right" aria-hidden="true" /> {t('threed.labels.advanced')}
      </button>
      {operation.output === 'skeleton_animation' && <p className="form-hint">{t('threed.animation.skeletonHint', 'Generates an animated skeleton, without a mesh or skin.')}</p>}
    </div>
  )
}
