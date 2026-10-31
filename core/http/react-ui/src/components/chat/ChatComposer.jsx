/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { CAP_CHAT } from '../../utils/capabilities'
import { seesImages } from '../../utils/chatVision'
import { connectorRef } from '../../utils/chatMCP'
import { networkMode } from '../../utils/chatEgress'
import { gbLabel, gbNumber } from '../../utils/modelLedger'
import { SANDBOX_ADDON } from '../../utils/sandboxStatus'
import { useModelFit } from '../../hooks/useModelFit'
import { useSandboxStatus } from '../../hooks/useSandboxStatus'
import { useCommandsNotice } from '../../hooks/useCommandsNotice'
import { fillStyle, hostMemory, memoryFigure } from '../home/memory'
import HomeComposer from '../home/HomeComposer'
import HomeModelPicker from '../home/HomeModelPicker'
import AddonSlot from '../../addons/AddonSlot'
import AgentPicker from './AgentPicker'
import SkillsMenu from './SkillsMenu'
import SwitchMenu from './SwitchMenu'
import VisionNotice from './VisionNotice'
import NetworkRow from './NetworkRow'
import { CommandsChip, CommandsNotice } from './CommandsNotice'
import { ToolFallbacks } from './ToolFallbacks'
import Button from '../ui/Button'
// The model list, the switch menus and the notices are styled with the chat
// page; Home draws the same composer.
import '../../pages/chat.css'

// The composer of a chat, on the chat page and on Home: the model list (with
// what each model can do and how it fits this machine), the saved agent, the
// Tools and Skills menus, the attach buttons, dictation, paste to attach, the
// slash menu and the add-on slot, over HomeComposer. Its state is
// useChatComposer's. Before the chat exists the switches are a draft that the
// create request carries (useChatSwitches).
//
//   composer       the result of useChatComposer
//   chatId         the open chat, or null for a new one
//   activeChat     the open chat's record, or null
//   onPickModel    what picking a model does in an open chat (the draft model
//                  of a new chat changes otherwise)
//   pickerRef      reaches the model list (the /model command opens it)
//   pickerDisabled, pickerPlaceholder   Home without a model installed
//   egress         the chat's network (useChatEgress), for the Tools menu
//   toolsExtra     more chips after Skills (the sub-agents of a chat)
//   chips          more chips after the agent (the Files chip)
//   imageModel     the model that describes images to a model that cannot see
// The other props go to HomeComposer as they are.
export default function ChatComposer({
  composer, chatId = null, activeChat = null, filesOn = true, loadedIds, imageModel = '',
  onPickModel, pickerRef, pickerDisabled = false, pickerPlaceholder, egress = null, toolsExtra, chips,
  slotContext, placeholder, onDictateError, ...rest
}) {
  const { t } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')
  const { isAdmin } = useAuth()
  const { drafts, draftId, switches, models, modelsLoading, pickedAgent, effectiveModel, agents, agentId, setAgentId, setDraftModel, attach, paste } = composer
  const { text, files, setText, setFiles } = drafts

  const [fitOpenCount, setFitOpenCount] = useState(0)
  const modelNames = useMemo(() => models.map(m => m.id), [models])
  const modelFit = useModelFit({ openCount: fitOpenCount, names: modelNames })
  const onPickerOpen = useCallback(() => setFitOpenCount(n => n + 1), [])

  const sandbox = useSandboxStatus(switches.addons.some(a => a.id === SANDBOX_ADDON), chatId)
  const commands = useCommandsNotice(switches.commands.state, switches.commands.reason, isAdmin)
  // What the Tools picker says under an add-on: the Sandbox's state.
  const addonNote = (a) => {
    if (a.id !== SANDBOX_ADDON || !sandbox || sandbox.state === 'unknown') return {}
    if (sandbox.state === 'cannot') return { usable: false, reason: sandbox.reason ? tc('tools.sandbox.cannotWhy', { reason: sandbox.reason }) : tc('tools.sandbox.cannot') }
    if (sandbox.state === 'host') return { note: tc('tools.sandbox.host'), warn: true }
    return { note: tc('tools.sandbox.isolated', { backend: sandbox.backend }) }
  }
  // Who serves a group of built-in tools in this chat: the built-in itself, or
  // an add-on that shadows a tool of the group (the server resolves it).
  const builtinNote = (b) => {
    const by = b.providers || {}
    const shadowed = Object.entries(by).filter(([, p]) => p && p !== 'builtin')
    if (shadowed.length === 0) return {}
    const provider = (p) => (p === 'builtin' ? tc('tools.servedByBuiltin') : p)
    const mixed = b.provider === 'mixed'
    const note = mixed
      ? Object.entries(by).map(([tool, p]) => tc('tools.servedByTool', { tool, provider: provider(p) })).join(' · ')
      : tc('tools.servedBy', { provider: b.provider })
    // One provider fits a chip next to the name; one per tool is a line under it.
    return { [mixed ? 'note' : 'chip']: note, title: tc('tools.builtinByAddon', { tools: (b.tools || []).join(', '), provider: mixed ? note : b.provider }) }
  }

  // What a row of the model list says: whether it is loaded, what it can do
  // and, where the server can estimate it, how it fits this machine.
  const describeModel = (name, warm) => {
    const vision = seesImages(models.find(m => m.id === name))
    const reading = modelFit.reading(name)
    let fit = null
    if (warm) fit = { tone: 'ok', text: t('picker.readyNow') }
    else if (reading?.fit) {
      const f = reading.fit
      fit = f.state === 'fits'
        ? { tone: 'ok', text: t('picker.fits', { amount: gbNumber(f.amount) }) }
        : f.state === 'spill'
          ? { tone: 'warn', text: t('picker.spill', { amount: gbNumber(f.amount) }) }
          : { tone: 'err', text: t('picker.over', { amount: gbNumber(f.amount) }) }
    }
    return { vision: !!vision, size: reading?.bytes ? gbLabel(reading.bytes) : null, fit }
  }
  const memory = hostMemory(modelFit.resources)
  const memoryNumbers = memory ? memoryFigure(memory.used, memory.total) : null
  const picker = (
    <HomeModelPicker
      ref={pickerRef}
      value={effectiveModel}
      onChange={chatId && onPickModel ? onPickModel : setDraftModel}
      capability={CAP_CHAT}
      models={models}
      loading={modelsLoading}
      loadedIds={loadedIds}
      grouped
      describe={describeModel}
      onOpen={onPickerOpen}
      disabled={pickerDisabled || (!chatId && !!pickedAgent?.model)}
      autoSelect={!chatId && !pickedAgent?.model}
      rememberSelection={!chatId}
      placeholder={pickerPlaceholder}
      footer={memoryNumbers && (
        <div className="home-menu__foot" data-testid="chat-model-memory">
          <span>{memory.isGpu ? t('picker.gpuMemory') : t('picker.memory')}</span>
          <span className="cx-ctx__bar" aria-hidden="true"><i style={fillStyle(memory.pct)} /></span>
          <span>{t('picker.memoryUsed', { used: memoryNumbers.used, total: memoryNumbers.total, unit: memoryNumbers.unit })}</span>
        </div>
      )}
    />
  )

  // The tools and skills of the chat, each with its switch. A new chat gets
  // the draft's when it starts; an agent's chat lists only what the agent grants.
  const toolGroups = [
    {
      // The tools LocalAI serves itself, such as web search: one switch per group.
      key: 'builtins', title: tc('tools.builtins'), icon: 'globe', onSet: switches.setBuiltinsOn,
      rows: switches.builtins.map((b) => ({
        key: `b-${b.group}`, id: b.group, optIn: !!b.opt_in, label: tc(`tools.builtin.${b.group}`, { defaultValue: b.group }),
        title: tc('tools.builtinBy', { tools: (b.tools || []).join(', ') }),
        on: !b.off, usable: b.usable, reason: b.reason, onToggle: (on) => switches.toggleBuiltin(b.group, on),
        ...builtinNote(b),
      })),
    },
    {
      key: 'addons', title: tc('tools.addons'), icon: 'puzzle', onSet: switches.setAddonsOn,
      rows: switches.addons.map((a) => ({ key: `a-${a.id}`, id: a.id, label: a.id, meta: t('tools.toolCount', { count: a.tools }), on: !a.off, onToggle: (on) => switches.toggleAddon(a.id, on), ...addonNote(a) })),
    },
    {
      key: 'servers', title: tc('tools.servers'), icon: 'server', onSet: switches.setMCPOn,
      rows: switches.servers.map((s) => ({ key: `s-${s.name}`, id: s.name, label: s.name, on: !s.off, usable: s.usable, reason: s.reason, onToggle: (on) => switches.toggleMCP(s.name, on) })),
    },
    {
      key: 'connectors', title: tc('tools.connectors'), icon: 'plug', onSet: switches.setMCPOn,
      rows: switches.connectors.map((c) => ({ key: `c-${c.id}`, id: connectorRef(c.id), label: c.name, on: !c.off, usable: c.usable, reason: c.reason, onToggle: (on) => switches.toggleMCP(connectorRef(c.id), on) })),
    },
  ]
  const network = egress && networkMode(egress) !== 'hidden'
  const tools = (
    <>
      <SwitchMenu
        icon="plug" label={t('tools.label')} title={t('tools.title')} heading={t('tools.heading')}
        note={chatId ? t('tools.note') : t('tools.draftNote')}
        groups={toolGroups} error={switches.error} testId="chat-tools"
        notice={commands.info || network || switches.fallbacks.length > 0
          ? <>{commands.info ? <CommandsNotice notice={commands} /> : null}<ToolFallbacks fallbacks={switches.fallbacks} />{network ? <NetworkRow egress={egress} /> : null}</>
          : null}
        footer={switches.connectors.length > 0 || switches.servers.length > 0
          ? <Button size="sm" variant="ghost" iconEnd="arrow-right" to="/app/mcp-connectors" data-testid="chat-tools-manage">{tc('tools.manage')}</Button>
          : null}
      />
      <CommandsChip notice={commands} />
      <SkillsMenu skills={switches.skills} replaced={switches.replacedSkills} onSet={switches.setSkillsOn} error={switches.skillsError} />
      {toolsExtra}
    </>
  )

  const effectivePlaceholder = placeholder || (effectiveModel
    ? t('input.placeholderModel', { model: effectiveModel })
    : (!modelsLoading && models.length === 0 ? t('input.placeholderNoModel') : t('input.placeholderPick')))

  return (
    <>
      <VisionNotice files={files} model={effectiveModel} models={models} imageModel={imageModel} />
      <HomeComposer
        onDictateError={onDictateError}
        onDictate={(said) => drafts.append(draftId, said)}
        message={text}
        onMessage={setText}
        picker={picker}
        mcp={tools}
        chips={(
          <>
            <AgentPicker agents={agents} value={activeChat ? activeChat.agent_id : agentId} onChange={setAgentId} readOnly={!!chatId} />
            {chips}
          </>
        )}
        extras={<AddonSlot slot="chat.composer.actions" context={slotContext} />}
        files={files}
        onRemoveFile={(f) => setFiles(prev => prev.filter(x => x !== f))}
        onAttach={filesOn ? attach : undefined}
        fileTestId="chat-file-input"
        placeholder={effectivePlaceholder}
        onPaste={paste}
        fileAccept="*/*"
        {...rest}
      />
    </>
  )
}
