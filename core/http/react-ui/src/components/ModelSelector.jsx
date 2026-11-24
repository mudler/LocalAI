import { useMemo } from 'react'
import ModelPicker from './ModelPicker'

// The name add-ons know the model picker by. It keeps the props they were
// given (a plain list of names in `options`, `triggerClassName`, `style`) and
// draws the shared ModelPicker.
export default function ModelSelector({
  value, onChange, capability, className = '', options, loading,
  disabled, style, id, triggerClassName = '',
}) {
  const models = useMemo(() => options?.map(name => ({ id: name })), [options])
  return (
    <div style={style}>
      <ModelPicker
        id={id} variant="field" searchable className={`${className} ${triggerClassName}`.trim()}
        value={value || ''} onChange={onChange} capability={capability}
        models={models} loading={loading} disabled={disabled}
      />
    </div>
  )
}
