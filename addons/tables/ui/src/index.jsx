// Tables: the native UI of the Tables add-on. The host loads this module by
// URL after the host was built and hands it one object, the SDK. React, the
// components, the typed calls and the navigation all come from that object;
// there is no import of the host and no second React. Nothing is global: the
// whole add-on is created inside createAddon and ends with dispose().
import { createContext } from './ctx.js'
import { createApi } from './api.js'
import { createHooks } from './hooks.js'
import { createUi } from './ui.jsx'
import { createFields } from './fields.jsx'
import { createDatabases } from './Databases.jsx'
import { createDatabase } from './Database.jsx'
import { createFilters } from './Filters.jsx'
import { createRows } from './Rows.jsx'
import { createExport } from './Export.jsx'
import { createTableView } from './TableView.jsx'
import { createSchema } from './Schema.jsx'
import { createSharing } from './Sharing.jsx'
import { createSelectDatabase } from './SelectDatabase.jsx'

// The second argument is for tests: the host passes the SDK only.
export function createAddon(sdk, options = {}) {
  const ctx = createContext(sdk, options.cache)
  const { h, t } = ctx
  const api = createApi(ctx)
  const hooks = createHooks(ctx)
  const ui = createUi(ctx)
  const fields = createFields(ctx, ui)
  const filters = createFilters(ctx, ui)
  const rows = createRows(ctx, ui, fields)
  const databases = createDatabases(ctx, ui, api, hooks)
  const exporter = createExport(ctx, ui, api, hooks)
  const tableView = createTableView(ctx, ui, api, hooks, rows, filters, exporter)
  const schema = createSchema(ctx, ui, fields, hooks)
  const sharing = createSharing(ctx, ui, api, hooks)
  const database = createDatabase(ctx, ui, api, hooks, { databases, tableView, schema, sharing })
  const select = createSelectDatabase(ctx, ui, api, hooks)

  // The one page of the add-on: the list, or one database by its ID.
  function Page({ page, path }) {
    const first = String(path || '').split('/')[0]
    if (page !== 'databases') return null
    return (
      <ui.Root className="tbl-app">
        {first ? <database.Database key={first} id={first} /> : <databases.Databases />}
      </ui.Root>
    )
  }

  // The component of the composer action: it gets the conversation the host
  // opened it from and a close function, and nothing else.
  function SelectDatabaseAction(props) {
    return <select.SelectDatabase conversationId={props.conversationId} close={props.close} />
  }

  const open = (context) => {
    sdk.layers.openDialog({
      title: t('select.title'),
      render: ({ close }) => <select.SelectDatabase conversationId={context.conversationId} close={close} />,
    })
  }

  // The language bundles are defined by createContext. Nothing was
  // registered anywhere else, so ending the add-on is clearing the cache.
  return {
    Component: Page,
    components: { SelectDatabase: SelectDatabaseAction },
    chatActions: [{ id: 'select-database', label: t('select.title'), icon: 'database', open }],
    dispose() { ctx.clear() },
  }
}
