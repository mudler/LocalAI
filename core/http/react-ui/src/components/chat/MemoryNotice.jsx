/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { memoryFix, memoryOff, MEMORY_GROUP } from '../../utils/memoryState'
import './memorynotice.css'

// The one line that says memory is off here, and why, and where to turn it on.
// It sits at the top of the Tools menu while memory is off.
export function MemoryLine({ memory, builtins }) {
  const { t } = useTranslation('chats')
  const off = memoryOff(memory, (builtins || []).find((b) => b.group === MEMORY_GROUP))
  if (!off) return null
  const fix = memoryFix(off)
  return (
    <p className="mem-line" data-testid="chat-memory-line" data-code={off.kind}>
      <Icon name="eye-off" className="mem-line__icon" />
      <span>
        <strong>{t('memoryOff.title')}</strong>{' '}{t(`memoryOff.line.${off.kind}`)}
        {fix ? <>{' '}<Link className="cs-link" to={fix.to}>{t(`memoryOff.${fix.key}`)}</Link></> : null}
      </span>
    </p>
  )
}

// A small pill next to the Tools menu of the composer: "Private chat" for a
// chat that switched memory off, "Memory off" for the levels above.
export function MemoryChip({ memory, builtins }) {
  const { t } = useTranslation('chats')
  const off = memoryOff(memory, (builtins || []).find((b) => b.group === MEMORY_GROUP))
  if (!off) return null
  const text = off.kind === 'chat' ? t('memoryOff.chipPrivate') : t('memoryOff.chip')
  return (
    <span className="mem-chip" data-testid="chat-memory-chip" data-code={off.kind} title={t(`memoryOff.line.${off.kind}`)}>
      <Icon name="eye-off" />{text}
    </span>
  )
}
