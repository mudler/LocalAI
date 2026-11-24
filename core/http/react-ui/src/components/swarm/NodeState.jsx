import { useTranslation } from 'react-i18next'
import { nodeState } from '../../utils/swarm'
import Icon from '../Icon'

const ICON = { ok: 'check-circle', warn: 'warning', error: 'alert-circle', idle: 'circle' }

// A node's state as a mark and a word. The word always says it: the colour only
// repeats it.
export default function NodeState({ node }) {
  const { t } = useTranslation('swarm')
  const state = nodeState(node)
  const icon = state.key === 'draining' ? 'pause' : ICON[state.level]
  return (
    <span className="sw-state" data-level={state.level} data-state={state.key}>
      <Icon name={icon} />
      {t(`state.${state.key}`)}
    </span>
  )
}
