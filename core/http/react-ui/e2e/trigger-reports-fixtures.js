// Triggers that report in each way, for the specs and the screenshots.
import { TRIGGERS } from './agents-fixtures.js'

const base = TRIGGERS[0]
const trigger = (id, name, delivery) => ({ ...base, id, name, last_run: undefined, delivery })

export const REPORT_TRIGGERS = [
  trigger('d1', 'To the Inbox', { target: 'inbox', mode: 'always' }),
  trigger('d2', 'To a channel', {
    target: 'channel', channel_label: 'General', channel_ref: 'G1', connection_id: 'b1', connection_name: 'Team bot', connection_kind: 'telegram',
    connection_kind_title: 'Telegram', connection_addon: 'telegram', state: 'ok', mode: 'on_change',
  }),
  trigger('d3', 'To a chat', {
    target: 'origin', chat_ref: '123…789', in_thread: false, connection_id: 'b2', connection_name: 'Ops bot', connection_kind: 'slack',
    connection_kind_title: 'Slack', connection_addon: 'slack', state: 'ok', mode: 'always',
  }),
  trigger('d4', 'Connection off', {
    target: 'channel', channel_label: 'Alerts', channel_ref: 'C0…XYZ', connection_id: 'b1', connection_name: 'Team bot', connection_kind: 'telegram',
    connection_kind_title: 'Telegram', connection_addon: 'telegram', state: 'off', mode: 'on_problem',
  }),
  trigger('d5', 'Connection deleted', {
    target: 'channel', channel_label: 'Old room', channel_ref: 'R1', connection_kind: '', state: 'gone', mode: 'always',
  }),
]

