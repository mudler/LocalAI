// Stand-ins for the host's settings kit (sdk.ui.Section, Field, Choice, ...)
// in the node tests of first-party add-on UIs. The host draws the real ones
// (core/http/react-ui/src/addons/hostComponents.jsx); these keep the same
// props and draw plain markup with the test ids, so a test can check what an
// add-on asks the host to draw without the host's build.
export const KIT = ['Section', 'Field', 'Select', 'TextInput', 'Switch', 'Choice', 'Callout', 'StatusChip', 'List', 'ListRow', 'Code', 'Actions', 'Table', 'ExternalLink']

export function kitStubs(React) {
  const h = React.createElement
  const tid = (p) => (p.testId ? { 'data-testid': p.testId } : {})
  return {
    Section: (p) => h('section', { 'data-kit': 'Section', ...tid(p) }, p.title ? h('h3', null, p.title) : null, p.description ? h('p', null, p.description) : null, p.actions || null, p.children),
    Field: (p) => h('label', { 'data-kit': 'Field', ...tid(p) }, h('span', null, p.label), p.children, p.unit ? h('span', null, p.unit) : null, p.help ? h('span', { 'data-kit-help': '' }, p.help) : null, p.error ? h('span', { role: 'alert' }, p.error) : null),
    Select: (p) => h('select', { 'data-kit': 'Select', value: p.value, disabled: p.disabled, onChange: () => {}, ...tid(p) }, (p.options || []).map(o => h('option', { key: o.value, value: o.value }, o.label))),
    TextInput: (p) => h('input', { 'data-kit': 'TextInput', type: p.type === 'password' ? 'password' : 'text', value: p.value ?? '', disabled: p.disabled, onChange: () => {}, ...tid(p) }),
    Switch: (p) => h('button', { 'data-kit': 'Switch', role: 'switch', 'aria-checked': !!p.checked, 'aria-label': p.label, disabled: p.disabled, ...tid(p) }),
    Choice: (p) => h('fieldset', { 'data-kit': 'Choice', disabled: p.disabled, ...tid(p) }, p.legend ? h('legend', null, p.legend) : null,
      (p.options || []).map(o => h('label', { key: o.value, 'data-value': o.value, 'data-selected': p.value === o.value ? '' : undefined },
        h('input', { type: 'radio', checked: p.value === o.value, onChange: () => {}, disabled: o.disabled }), o.label, o.chip || null, o.description ? h('span', null, o.description) : null))),
    Callout: (p) => h('div', { 'data-kit': 'Callout', 'data-tone': p.tone || 'info', role: p.role, ...tid(p) }, p.title ? h('strong', null, p.title) : null, p.children, p.actions || null),
    StatusChip: (p) => h('span', { 'data-kit': 'StatusChip', 'data-tone': p.tone || 'idle', ...tid(p) }, p.children),
    List: (p) => h('ul', { 'data-kit': 'List', 'aria-label': p.label, ...tid(p) }, p.children),
    ListRow: (p) => h('li', { 'data-kit': 'ListRow', ...tid(p) }, h('strong', null, p.title), p.status || null, p.meta ? h('span', null, p.meta) : null, p.children,
      p.details ? h('details', null, h('summary', null, p.detailsLabel || 'Details'), p.details) : null),
    Code: (p) => h('pre', { 'data-kit': 'Code', ...tid(p) }, p.children),
    Actions: (p) => h('div', { 'data-kit': 'Actions' }, p.children),
    Table: (p) => h('table', { 'data-kit': 'Table', ...tid(p) }, h('tbody', null, (p.rows || []).map((r, i) => h('tr', { key: r.key ?? i }, (p.columns || []).map(c => h('td', { key: c.key }, r[c.key])))))),
    ExternalLink: (p) => h('a', { 'data-kit': 'ExternalLink', href: p.href, ...tid(p) }, p.children),
    Button: (p) => h('button', { 'data-kit': 'Button', 'data-variant': p.variant, disabled: p.disabled || p.busy === true, ...tid(p) }, p.children),
  }
}
