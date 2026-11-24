/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { ListPicker } from '../ui'
import './composerpickers.css'

// The saved agent a new chat starts with, as a chip of the composer. An agent
// gives the chat its instructions and exactly the tools it grants; its model,
// when it names one, replaces the model of the chip. Once the chat exists its
// agent does not change, so the chip only shows it (readOnly).
//
// It is the composer's ListPicker: a "No agent" row on top, one row per agent
// with its description under the name and its model at the end, and a filter
// box once the list is long.
export default function AgentPicker({ agents, value, onChange, readOnly = false }) {
  const { t } = useTranslation('chat')
  const items = useMemo(() => agents.map((a) => ({
    id: a.id,
    label: a.name,
    description: a.description || undefined,
    secondary: a.model || undefined,
    keywords: [a.model].filter(Boolean),
  })), [agents])

  const picked = agents.find((a) => a.id === value)
  if (readOnly) {
    if (!picked) return null
    return (
      <span className="home-chip" data-testid="chat-agent" title={t('agent.fixed')}>
        <Icon name="robot" /> <span className="home-chip__text">{picked.name}</span>
      </span>
    )
  }
  if (agents.length === 0) return null

  return (
    <div className="agent-picker" data-testid="chat-agent-picker">
      <ListPicker
        items={items}
        value={picked ? picked.id : ''}
        onChange={(id) => onChange(id)}
        icon="robot"
        placeholder={t('agent.label')}
        ariaLabel={t('agent.title')}
        noneLabel={t('agent.none')}
        heading={t('agent.heading')}
        footer={<p className="cp-note">{t('agent.note')}</p>}
        menuClassName="agent-picker__menu"
        testId="chat-agent"
        triggerProps={{ 'data-testid': 'chat-agent-button', title: t('agent.title') }}
      />
    </div>
  )
}
