import { modelStateConfig } from './nodeStatus'

export default function ModelChip({ model }) {
  const cfg = modelStateConfig[model.state] || modelStateConfig.idle
  return (
    <span className="model-chip" style={{ background: cfg.bg, color: cfg.color, borderColor: cfg.border }}>
      <span className="model-chip__dot" style={{ background: cfg.color }} />
      {model.model_name}
      {model.state !== 'loaded' && <span className="model-chip__state"> {model.state}</span>}
    </span>
  )
}
