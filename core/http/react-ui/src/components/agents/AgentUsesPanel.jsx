/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { Button } from '../ui'
import AgentUses from './AgentUses'
import AgentMcpPicker from './AgentMcpPicker'
import { AppliedNotice } from './AgentPresets'
import { skillKey } from '../../utils/chatSkills'
import { useSandboxProfileChoices } from '../../hooks/useSandboxProfileChoices'

// The tool switches of an agent: built-in groups, add-ons and MCP servers, with
// the handlers of useAgentAbilities. The full editor draws it under the levels;
// the wizard draws it under "Fine-tune individual tools".
//   abilities    the result of useAgentAbilities
//   tools        the result of useAgentTools
//   toolGroups   the result of useToolGroups
//   presets, presetNote, notices   optional nodes above the switches
export default function AgentUsesPanel({ abilities: ab, tools, toolGroups, form, updateField, presets, presetNote, notices }) {
  const { t } = useTranslation('agents')
  const profiles = useSandboxProfileChoices()
  const appliedFor = (id) => (ab.applied?.kind === 'addon' && ab.applied.id === id
    ? <AppliedNotice applied={ab.applied} onUndo={ab.undoApplied} onKeep={() => ab.setApplied(null)} testId="agent-addon-applied" />
    : null)
  return (
    <AgentUses
      presets={presets}
      presetNote={presetNote}
      notices={notices}
      builtinGroups={toolGroups.filter(g => !['addons', 'servers', 'connectors'].includes(g.key))}
      error={tools.error}
      network={(
        <div className="form-group">
          <label className="form-label" htmlFor="ag-network">{t('create.network')}</label>
          <select id="ag-network" className="input" value={form.network || 'off'} onChange={(e) => updateField('network', e.target.value)} data-testid="agent-network">
            {['off', 'registries', 'all'].map(p => <option key={p} value={p}>{t(`create.networkPresets.${p}`)}</option>)}
          </select>
          <p className="ag-note">{t('create.networkHelp')}</p>
          {profiles.length > 0 && (
            <>
              <label className="form-label" htmlFor="ag-sandbox-profile">{t('create.sandboxProfile')}</label>
              <select id="ag-sandbox-profile" className="input" value={form.sandbox_profile || ''} onChange={(e) => updateField('sandbox_profile', e.target.value)} data-testid="agent-sandbox-profile">
                <option value="">{t('create.sandboxProfileDefault')}</option>
                {profiles.map(p => <option key={p.id} value={p.id} disabled={p.allowed === false}>{p.title}</option>)}
              </select>
              <p className="ag-note">{t('create.sandboxProfileHelp')}</p>
            </>
          )}
        </div>
      )}
      mcpNode={(
        <>
          <p className="ag-note">{t('create.mcpHelp')}</p>
          <AgentMcpPicker rows={ab.mcpList} onSet={ab.setMcp} />
        </>
      )}
      footer={<Button size="sm" variant="ghost" iconEnd="arrow-right" to="/app/mcp-connectors" data-testid="agent-tools-manage">{t('create.manageConnectors')}</Button>}
      addons={tools.addons}
      skillRows={tools.skills}
      choices={tools.resourceChoices}
      appliedFor={appliedFor}
      onAddon={ab.switchAddon}
      onAccounts={ab.pickAccounts}
      onSkill={(sk, on) => tools.setSkillsOn(new Set([skillKey(sk)]), on)}
      onResource={ab.pickResource}
    />
  )
}
