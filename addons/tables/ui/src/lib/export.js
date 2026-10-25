// The arguments of the CSV export of the grid a person is looking at: the
// table, the filter and the sort of the view, never a page size or a cursor.
// The add-on and the host own every other rule (limits, quoting, who may
// read), so this is only the translation of the view into the tool call.
import { buildQuery } from './query.js'

export const EXPORT_TOOL = 'rows-export'

// exportArguments returns { arguments, errors }. With rowId the row identifier
// is the first column of the file; otherwise every column is exported in the
// order of the schema.
export function exportArguments({ table, conditions = [], mode = 'and', sort = [], rowId = false }, columns) {
  const built = buildQuery({ table, conditions, mode, sort }, columns)
  const args = { tool: EXPORT_TOOL, table }
  if (built.arguments.filter) args.filter = built.arguments.filter
  if (built.arguments.sort) args.sort = built.arguments.sort
  if (rowId === true) args.columns = ['_id', ...columns.map(c => c.name)]
  return { arguments: args, errors: built.errors }
}

// exportOutcome reads the answer of the host: the file of the artifact, or
// the reason the add-on gave for not making one. The name, size and expiry
// shown to the person come from the host's record of the file, not from here.
export function exportOutcome(answer) {
  const files = answer && Array.isArray(answer.files) ? answer.files : []
  if (files.length === 1 && typeof files[0].id === 'string') return { kind: 'file', id: files[0].id }
  const p = answer && answer.payload && typeof answer.payload === 'object' ? answer.payload : {}
  if (p.error === 'too-large') return { kind: 'too-large' }
  if (p.error === 'busy') return { kind: 'busy' }
  return { kind: 'none' }
}
