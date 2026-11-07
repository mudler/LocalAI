/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { CAP_CHAT } from '../../utils/capabilities'
import { seesImages } from '../../utils/chatVision'
import { networkMode } from '../../utils/chatEgress'
import { gbLabel, gbNumber } from '../../utils/modelLedger'
import { useModelFit } from '../../hooks/useModelFit'
import { useCommandsNotice } from '../../hooks/useCommandsNotice'
import { SAVED_AGENTS, SUBAGENTS, useToolGroups } from '../../hooks/useToolGroups'
import { useChatGrants } from '../../hooks/useBuiltinGrants'
import { fillStyle, hostMemory, memoryFigure } from '../home/memory'
import HomeComposer from '../home/HomeComposer'
import ModelPicker from '../ModelPicker'
import AddonSlot from '../../addons/AddonSlot'
import AgentPicker from './AgentPicker'
import SkillsMenu from './SkillsMenu'
import SwitchMenu from './SwitchMenu'
import VisionNotice from './VisionNotice'
import { MemoryChip, MemoryLine } from './MemoryNotice'
import { memoryOff, MEMORY_GROUP } from '../../utils/memoryState'
import { ChatToolsControls } from '../../addons/hostControlComponents'
import AgentFolderChip from './AgentFolderChip'
import { agentFolderLine } from '../../utils/chatAgents'
import { CommandsChip, CommandsNotice } from './CommandsNotice'
import { ToolFallbacks } from './ToolFallbacks'
import Button from '../ui/Button'
import { availableCommands, commandGroups, COMMAND_GROUPS, filterArgs, usableSkills, reservedNames } from '../../utils/slashCommands'
import { CHAT_SLASH_ACTIONS } from './chatActions'
import { SLASH_ACTIONS } from '../home/homeActions'

// /model and /models take a model name: the menu suggests the installed chat
// models as the person types.
function withModelArgs(commands) {
  return commands.map((c) => ((c.id === 'model' || c.id === 'models')
    ? { ...c, args: (query, ctx) => filterArgs((ctx.models || []).map((name) => ({ key: `model-${name}`, name, icon: 'cube' })), query) }
    : c))
}
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
//   slashSurface   'chat' or 'home': which commands of the registry
//                  (utils/slashCommands.js) the slash menu offers
//   slashContext   what the page knows for them (see availableCommands); the
//                  composer adds the skills, the saved agents, the models
//                  and the staged files
//   onRunAction    (id, item) for the commands the page runs; the composer
//                  runs /model with a name, /models, and "turn on saved agents"
// The other props go to HomeComposer as they are.
export default function ChatComposer({
  composer, chatId = null, activeChat = null, filesOn = true, loadedIds, imageModel = '',
  onPickModel, pickerRef, pickerDisabled = false, pickerPlaceholder, egress = null, toolsExtra, chips,
  slotContext, placeholder, onDictateError, slashSurface = 'chat', slashContext, onRunAction, ...rest
}) {
  const { t } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')
  const { isAdmin, user } = useAuth()
  const { drafts, draftId, switches, models, modelsLoading, pickedAgent, effectiveModel, agents, agentId, setAgentId, setDraftModel, attach, paste } = composer
  const { text, files, setText, setFiles } = drafts

  const [fitOpenCount, setFitOpenCount] = useState(0)
  const modelNames = useMemo(() => models.map(m => m.id), [models])
  const modelFit = useModelFit({ openCount: fitOpenCount, names: modelNames })
  const onPickerOpen = useCallback(() => setFitOpenCount(n => n + 1), [])

  const commands = useCommandsNotice(switches.commands, isAdmin, [user, chatId, effectiveModel, agentId])
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
    <ModelPicker
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
  const grants = useChatGrants(pickedAgent)
  const toolGroups = useToolGroups({ ...switches, grants }, { commandsInfo: commands.info })
  const network = egress && networkMode(egress) !== 'hidden'
  const memoryIsOff = !!memoryOff(switches.memory, switches.builtins.find((b) => b.group === MEMORY_GROUP))
  // A chat whose agent has a folder reaches only that folder with its file tools.
  const scopeLine = agentFolderLine(pickedAgent, t)
  const tools = (
    <>
      <SwitchMenu
        icon="plug" label={t('tools.label')} title={t('tools.title')} heading={t('tools.heading')}
        note={chatId ? t('tools.note') : t('tools.draftNote')}
        groups={toolGroups} error={switches.error} testId="chat-tools"
        notice={scopeLine || commands.info || network || switches.fallbacks.length > 0 || memoryIsOff
          ? <><MemoryLine memory={switches.memory} builtins={switches.builtins} />{scopeLine ? <p className="cmd-notice__line" data-testid="chat-tools-scope">{scopeLine}</p> : null}{commands.info ? <CommandsNotice notice={commands} /> : null}<ToolFallbacks fallbacks={switches.fallbacks} />{network ? <ChatToolsControls egress={egress} /> : null}</>
          : null}
        footer={switches.connectors.length > 0 || switches.servers.length > 0
          ? <Button size="sm" variant="ghost" iconEnd="arrow-right" to="/app/mcp-connectors" data-testid="chat-tools-manage">{tc('tools.manage')}</Button>
          : null}
      />
      <CommandsChip notice={commands} />
      <MemoryChip memory={switches.memory} builtins={switches.builtins} />
      <SkillsMenu skills={switches.skills} replaced={switches.replacedSkills} onSet={switches.setSkillsOn} error={switches.skillsError} />
      {toolsExtra}
    </>
  )

  // The slash menu: the registry's commands for this surface, with what this
  // composer knows added to the page's context.
  const { t: th } = useTranslation('home')
  const savedGroup = switches.builtins.find((b) => b.group === SAVED_AGENTS)
  const subGroup = switches.builtins.find((b) => b.group === SUBAGENTS)
  const savedAgentsOn = !!savedGroup && !savedGroup.off && savedGroup.usable !== false && !subGroup?.off
  const slashCtx = useMemo(() => ({
    ...(slashContext || {}),
    skills: switches.skills, agents, canAgents: agents.length > 0 && !!savedGroup, savedAgentsOn,
    models: modelNames, canAttach: filesOn, staged: files.length,
  }), [slashContext, switches.skills, agents, savedGroup, savedAgentsOn, modelNames, filesOn, files.length])
  const slash = useMemo(() => {
    const own = slashSurface === 'home' ? th : t
    const ownIds = new Set((slashSurface === 'home' ? SLASH_ACTIONS : CHAT_SLASH_ACTIONS).map((a) => a.id))
    const words = (a) => (ownIds.has(a.id) ? own : t)
    return {
      actions: withModelArgs(availableCommands(slashSurface, slashCtx)),
      groups: commandGroups(slashSurface),
      ctx: slashCtx,
      label: (a) => a.label || words(a)(`slash.${a.id}.label`),
      desc: (a) => a.addonId ? a.label : words(a)(`slash.${a.id}.desc`),
      groupLabel: (g) => (COMMAND_GROUPS.includes(g) ? t(`slash.group.${g}`) : own(`slash.group.${g}`)),
      argsLabel: (c) => t(`slash.args.${c.id === 'models' ? 'model' : c.id}`, { defaultValue: c.cmd }),
      argLabel: (c, it) => (it.action === 'enableAgents' ? t('slash.agent.enable') : it.action === 'loopStopAll' ? t('slash.loop.stopAll') : ''),
      argDesc: (c, it) => ({
        clearFiles: t('slash.attach.clearDesc'), enableAgents: t('slash.agent.enableDesc'),
        goalClear: t('slash.goal.clearDesc'), goalResume: t('slash.goal.resumeDesc'),
        loopList: t('slash.loop.listDesc'), loopStopAll: t('slash.loop.stopAllDesc'),
      })[it.action] || (it.key === 'loop-stop' ? t('slash.loop.stopDesc') : ''),
      badgeLabel: (b) => t(`slash.badge.${b}`),
      noteLabel: (n) => (n === 'collides' ? t('slash.collides') : n),
    }
  }, [slashSurface, slashCtx, t, th])
  const runSlash = useCallback((id, item) => {
    if ((id === 'model' || id === 'models') && item?.name) {
      if (chatId && onPickModel) onPickModel(item.name)
      else setDraftModel(item.name)
      return
    }
    if (id === 'models') { pickerRef?.current?.open(); return }
    if (id === 'agent' && item?.action === 'enableAgents') { switches.toggleBuiltin(SAVED_AGENTS, true); setText('/agent '); return }
    if (id === 'help') {
      // The commands of the menu, and the skills apart: they are many.
      const entries = slash.actions.filter((a) => !a.label).map((a) => ({ cmd: a.cmd, desc: slash.desc(a), icon: a.icon }))
      const skills = slash.actions.filter((a) => a.label).map((a) => a.cmd)
      onRunAction?.(id, { entries, skills })
      return
    }
    if (id === 'about') {
      const on = (rows) => rows.filter((r) => !r.off && r.usable !== false).length
      const tools = on(switches.builtins) + switches.addons.filter((a) => !a.off).reduce((n, a) => n + (a.tools || 0), 0)
        + on(switches.servers) + on(switches.connectors)
      const selected = switches.commands?.state
      const runs = selected === 'available' ? 'isolated'
        : ['host', 'host-off', 'host-unavailable', 'provider-host'].includes(selected) ? 'host'
          : ['none', 'off', 'switched_off', 'unavailable', 'provider-unavailable'].includes(selected) ? 'none' : 'unknown'
      onRunAction?.(id, { about: { tools, skills: usableSkills(switches.skills).length, commands: runs } })
      return
    }
    onRunAction?.(id, item?.addonId ? { ...item, addonOptions: { nativeNames: reservedNames(), skillNames: usableSkills(switches.skills).map(s => s.name) } } : item)
  }, [chatId, onPickModel, setDraftModel, pickerRef, switches, setText, onRunAction, slash])

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
            <AgentFolderChip agent={pickedAgent} />
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
        slash={slash}
        onRunAction={runSlash}
        onClearFiles={() => setFiles([])}
        {...rest}
      />
    </>
  )
}
