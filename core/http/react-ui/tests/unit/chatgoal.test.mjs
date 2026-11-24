import test from 'node:test'
import assert from 'node:assert/strict'
import { goalAnnouncement, goalBar, goalMarker, goalProgress, pausedWhyKey } from '../../src/utils/chatGoal.js'
import { threadRows } from '../../src/utils/chatThread.js'

const live = { text: '', reasoning: '', tools: [], phase: '' }
const t = (key, params) => (params ? `${key} ${JSON.stringify(params)}` : key)

test('chat goal: the bar shows the action that fits the state', () => {
  assert.equal(goalBar(null), null)
  assert.equal(goalBar({ state: 'cleared' }), null)
  assert.deepEqual(
    [goalBar({ text: 'g', state: 'active' }).action, goalBar({ text: 'g', state: 'active' }).tone],
    ['pause', 'ok'])
  const paused = goalBar({ text: 'g', state: 'paused', paused_by: 'turn_limit' })
  assert.equal(paused.action, 'resume')
  assert.equal(paused.tone, 'warn')
  assert.equal(paused.detailKey, 'goal.pausedBy.turn_limit')
  const blocked = goalBar({ text: 'g', state: 'blocked', note: 'the API key' })
  assert.equal(blocked.action, 'resume')
  assert.deepEqual(blocked.detail, { note: 'the API key' })
  const done = goalBar({ text: 'g', state: 'done', note: 'shipped' })
  assert.equal(done.action, null)
  assert.equal(done.detailKey, 'goal.doneNote')
})

test('chat goal: an unknown pause reason reads as paused by the person', () => {
  assert.equal(pausedWhyKey('step_limit'), 'goal.pausedBy.step_limit')
  assert.equal(pausedWhyKey('something new'), 'goal.pausedBy.user')
})

test('chat goal: the status line counts the automatic turns of an active goal only', () => {
  assert.equal(goalProgress(null), null)
  assert.equal(goalProgress({ state: 'paused', turns: 3, max_turns: 20 }), null)
  assert.deepEqual(goalProgress({ state: 'active', turns: 3, max_turns: 20 }), { key: 'goal.progress', turn: 3, max: 20 })
  assert.deepEqual(goalProgress({ state: 'active', turns: 0, max_turns: 20 }), { key: 'goal.progressStart', turn: 0, max: 20 })
  // A limit lowered while the goal ran never reads "turn 9 of 5".
  assert.deepEqual(goalProgress({ state: 'active', turns: 9, max_turns: 5 }), { key: 'goal.progress', turn: 5, max: 5 })
})

test('chat goal: transcript lines read in plain words', () => {
  assert.deepEqual(goalMarker({ goal_event: 'set', text: 'ship it' }), {
    kind: 'set', icon: 'target', tone: 'accent', labelKey: 'goal.markers.set', params: {}, text: 'ship it', textKey: '',
  })
  const cont = goalMarker({ goal_event: 'continue', text: 'Continue toward the goal: "ship it".', goal_turn: 4 })
  assert.equal(cont.labelKey, 'goal.markers.continue')
  assert.deepEqual(cont.params, { turn: 4 })
  assert.equal(cont.text, '', 'the message the server sent is not repeated')
  assert.equal(goalMarker({ goal_event: 'continue', text: 'x' }).labelKey, 'goal.markers.continueStart')
  const paused = goalMarker({ goal_event: 'paused', text: 'step_limit' })
  assert.equal(paused.textKey, 'goal.pausedBy.step_limit', 'the reason is a sentence, never the stored word')
  assert.equal(paused.text, '')
  assert.equal(goalMarker({ goal_event: 'done', text: 'all set' }).text, 'all set')
  assert.equal(goalMarker({ goal_event: 'mystery', text: 'x' }).kind, 'set')
})

test('chat goal: a screen reader hears each change once', () => {
  const active = { text: 'g', state: 'active', turns: 1 }
  assert.equal(goalAnnouncement(null, null, t), '')
  assert.equal(goalAnnouncement(null, active, t), 'goal.announce.set {"text":"g"}')
  assert.equal(goalAnnouncement(active, { ...active, turns: 2 }, t), '', 'a new turn is not news')
  assert.equal(goalAnnouncement(active, { text: 'g', state: 'paused', paused_by: 'user' }, t), 'goal.announce.paused {"why":"goal.pausedBy.user"}')
  assert.equal(goalAnnouncement({ text: 'g', state: 'paused' }, active, t), 'goal.announce.resumed')
  assert.equal(goalAnnouncement(active, { text: 'g', state: 'done', note: 'ok' }, t), 'goal.announce.done {"note":"ok"}')
  assert.equal(goalAnnouncement(active, { text: 'g', state: 'blocked', note: 'key' }, t), 'goal.announce.blocked {"note":"key"}')
  assert.equal(goalAnnouncement(active, null, t), 'goal.announce.cleared')
  assert.equal(goalAnnouncement(active, { text: 'other', state: 'active' }, t), 'goal.announce.set {"text":"other"}', 'a replaced goal is said')
})

test('chat goal: goal lines are rows of their own between the messages', () => {
  const rows = threadRows([
    { seq: 1, turn: 0, role: 'goal', goal_event: 'set', text: 'ship it' },
    { seq: 2, turn: 1, role: 'goal', goal_event: 'continue', text: 'Work toward this goal now', goal_turn: 1 },
    { seq: 3, turn: 1, role: 'tool', text: 'bash: ls' },
    { seq: 4, turn: 1, role: 'assistant', text: 'done' },
    { seq: 5, turn: 1, role: 'goal', goal_event: 'done', text: 'shipped' },
  ], live)
  assert.deepEqual(rows.map(r => r.kind), ['goal', 'goal', 'msg', 'goal'])
  assert.equal(rows[2].activity.length, 1, 'the tool line still belongs to the answer')
  assert.equal(rows[3].key, 'g-5')
})

test('chat goal: /goal reads as show, clear, resume or a new goal', async () => {
  const { parseGoalCommand } = await import('../../src/utils/slashCommands.js')
  assert.deepEqual(parseGoalCommand('/goal'), { action: 'show' })
  assert.deepEqual(parseGoalCommand('  /goal  '), { action: 'show' })
  assert.deepEqual(parseGoalCommand('/goal clear'), { action: 'clear' })
  assert.deepEqual(parseGoalCommand('/goal Resume'), { action: 'resume' })
  assert.deepEqual(parseGoalCommand('/goal clear the old drafts'), { action: 'set', text: 'clear the old drafts' })
  assert.deepEqual(parseGoalCommand('/goal write the\nreport'), { action: 'set', text: 'write the\nreport' })
  assert.equal(parseGoalCommand('/goals are fun'), null)
  assert.equal(parseGoalCommand('set a /goal'), null)
})

test('chat goal: the /goal menu offers resume and clear only for the goal the chat has', async () => {
  const { availableCommands, slashQuery, parseInvocation } = await import('../../src/utils/slashCommands.js')
  const argsOf = async (ctx, text) => {
    const cmds = availableCommands('chat', { hasChat: true, ...ctx })
    const q = slashQuery(text, cmds)
    return q?.mode === 'args' ? (await q.command.args(q.query, ctx)).map(i => i.name) : null
  }
  assert.ok(availableCommands('chat', { hasChat: true }).some(c => c.id === 'goal'))
  assert.ok(!availableCommands('chat', { hasChat: true, canGoal: false }).some(c => c.id === 'goal'), 'a chat that started by itself has no /goal')
  assert.ok(!availableCommands('home', {}).some(c => c.id === 'goal'), 'Home has no chat to give a goal')
  assert.deepEqual(await argsOf({}, '/goal '), [])
  assert.deepEqual(await argsOf({ goalState: 'active' }, '/goal '), ['clear'])
  assert.deepEqual(await argsOf({ goalState: 'paused' }, '/goal '), ['resume', 'clear'])
  assert.deepEqual(await argsOf({ goalState: 'blocked' }, '/goal re'), ['resume'])
  assert.deepEqual(await argsOf({ goalState: 'paused' }, '/goal write the report'), [], 'a new goal closes the menu')
  // A skill named goal does not take the command.
  assert.equal(parseInvocation('/goal ship it', { skills: [{ name: 'goal' }] }), null)
})
