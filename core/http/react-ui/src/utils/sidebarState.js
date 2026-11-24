import { readStored, writeStored } from './storage.js'

// Whether the sidebar is folded, and which of its sections are open. The
// Sidebar writes them; the shell, the boot screen and the chat page only read
// them, and hear a change through the 'sidebar-collapse' window event.
const COLLAPSED = 'sidebar.collapsed'
const SECTIONS = 'sidebar.sections'

export const readSidebarCollapsed = () => readStored(COLLAPSED, ['localai_sidebar_collapsed']) === 'true'
export const writeSidebarCollapsed = (collapsed) => writeStored(COLLAPSED, String(collapsed))
export const readSidebarSections = () => readStored(SECTIONS, ['localai_sidebar_sections'])
export const writeSidebarSections = (state) => writeStored(SECTIONS, JSON.stringify(state))
