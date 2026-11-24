import { createContext } from 'react'

// What the Administration layout hands to its pages: the breadcrumb trail of
// the current page, and a claim so the layout draws its own trail only when no
// page draws one in its header (see AdminPage).
export const AdminShellContext = createContext(null)
