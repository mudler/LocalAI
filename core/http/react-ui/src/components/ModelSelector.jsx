import { useEffect } from 'react'
import { useModels } from '../hooks/useModels'

export default function ModelSelector({ value, onChange, capability, className = '' }) {
  const { models, loading } = useModels(capability)

  useEffect(() => {
    if (!value && models.length > 0) {
      onChange(models[0].id)
    }
  }, [models, value, onChange])

  return (
    <select
      className={`model-selector ${className}`}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={loading}
    >
      {loading && <option>Loading models...</option>}
      {!loading && models.length === 0 && <option>No models available</option>}
      {models.map(model => (
        <option key={model.id} value={model.id}>{model.id}</option>
      ))}
    </select>
  )
}
