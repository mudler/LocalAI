import { useEffect } from 'react'
import { useModels } from '../hooks/useModels'

export default function ModelSelector({ value, onChange, filter, className = '' }) {
  const { models, loading } = useModels()

  const filtered = filter
    ? models.filter(m => !filter || m.id?.includes(filter))
    : models

  useEffect(() => {
    if (!value && filtered.length > 0) {
      onChange(filtered[0].id)
    }
  }, [filtered, value, onChange])

  return (
    <select
      className={`model-selector ${className}`}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={loading}
    >
      {loading && <option>Loading models...</option>}
      {!loading && filtered.length === 0 && <option>No models available</option>}
      {filtered.map(model => (
        <option key={model.id} value={model.id}>{model.id}</option>
      ))}
    </select>
  )
}
