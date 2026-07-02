import { useNavigate } from 'react-router-dom'
import StatusPill from './StatusPill'
import ModelChip from './ModelChip'
import ActionMenu from '../ActionMenu'
import { formatVRAM } from './nodeStatus'

export default function NodePanel({ node, models = [], onApprove, onDrain, onResume, onRemove }) {
  const navigate = useNavigate()
  const isAgent = node.node_type === 'agent'
  const open = () => navigate(`/app/nodes/${node.id}`)
  const usedVRAM = node.total_vram && node.available_vram != null ? node.total_vram - node.available_vram : null

  return (
    <div className="node-panel">
      <div className="node-panel__main" onClick={open} role="button" tabIndex={0}
        onKeyDown={(e) => { if (e.key === 'Enter') open() }}>
        <div className="node-panel__head">
          <div className="node-panel__id">
            <StatusPill status={node.status} />
            <span className="node-panel__name">{node.name}</span>
            <span className="cell-mono cell-muted">{node.address}</span>
          </div>
          <div className="node-panel__actions" onClick={(e) => e.stopPropagation()}>
            {node.status === 'pending' && (
              <button className="btn btn-primary btn-sm" onClick={() => onApprove(node.id)}>
                <i className="fas fa-check" /> Approve
              </button>
            )}
            <ActionMenu
              ariaLabel={`Actions for ${node.name}`}
              triggerLabel={`Actions for ${node.name}`}
              items={[
                { key: 'resume', icon: 'fa-play', label: 'Resume', hidden: node.status !== 'draining', onClick: () => onResume(node.id) },
                { key: 'drain', icon: 'fa-pause', label: 'Drain', hidden: node.status === 'draining' || node.status === 'pending', onClick: () => onDrain(node.id) },
                { divider: true, hidden: node.status === 'pending' },
                { key: 'remove', icon: 'fa-trash', label: 'Remove from cluster', danger: true, onClick: () => onRemove(node) },
              ]}
            />
          </div>
        </div>

        {!isAgent && (
          <>
            <div className="node-panel__meta">
              {node.total_vram > 0 && (
                <span className="cell-mono">VRAM {formatVRAM(usedVRAM) || '0'} / {formatVRAM(node.total_vram)}</span>
              )}
              <span className="cell-mono">{node.in_flight_count || 0} in-flight</span>
            </div>
            <div className="node-panel__models">
              {models.length === 0
                ? <span className="cell-muted">No models loaded</span>
                : models.map(m => <ModelChip key={`${m.model_name}-${m.replica_index ?? 0}`} model={m} />)}
            </div>
          </>
        )}
      </div>
    </div>
  )
}
