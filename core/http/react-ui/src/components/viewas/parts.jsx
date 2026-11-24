/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Children, cloneElement, isValidElement, useId } from 'react'
import { useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Callout from '../ui/Callout'
import Page from '../ui/Page'
import Tag from '../ui/Tag'
import Icon from '../Icon'
import { useViewAs } from '../../contexts/ViewAsContext'
import { followsPage } from '../../utils/viewAsRoutes'
import './viewas.css'

// The "alice's data" chip next to the title of a page that follows the mode.
// Drawn by PageHeader only while the mode is on and the page follows it.
export function ViewAsChip() {
  const { t } = useTranslation('viewas')
  const { target, acting } = useViewAs()
  const { pathname } = useLocation()
  if (!target || !followsPage(pathname)) return null
  return (
    <Tag tone={acting ? 'error' : 'accent'} icon="user" className="viewas-chip" testId="viewas-chip">{t('scope.chip', { name: target.name })}</Tag>
  )
}

// A page that does not follow the mode says so, because the bar above it still
// says "Viewing as alice". Administration pages are among them.
export function ViewAsNotice() {
  const { t } = useTranslation('viewas')
  const { active, target } = useViewAs()
  const { pathname } = useLocation()
  if (!active || followsPage(pathname)) return null
  return (
    <Page className="viewas-notice-page">
      <Callout tone="info" testId="viewas-notice">{t('scope.notice', { name: target.name })}</Callout>
    </Page>
  )
}

// The marker on a nav item whose page follows the mode. Used by Sidebar.
export function NavMark() {
  const { t } = useTranslation('viewas')
  const { target, acting } = useViewAs()
  if (!target) return null
  const label = t('scope.navMark', { name: target.name })
  return (
    <span className="viewas-mark" data-testid="viewas-mark" data-mode={acting ? 'actas' : 'readonly'} role="img" aria-label={label} title={label}>
      <Icon name="user" aria-hidden="true" />
    </span>
  )
}

// The one-line legend of the markers, under the recent chats.
export function NavLegend() {
  const { t } = useTranslation('viewas')
  const { active, target, acting } = useViewAs()
  if (!active) return null
  return (
    <p className="viewas-legend" data-testid="viewas-legend" data-mode={acting ? 'actas' : 'readonly'}>
      <span className="viewas-mark viewas-mark--static" aria-hidden="true"><Icon name="user" /></span>
      <span>{t('scope.legend', { name: target.name })}</span>
    </p>
  )
}

// A write control in read-only mode. Wrap one control: while the mode is
// read-only it is aria-disabled, not disabled, so a keyboard can still reach
// it and a screen reader reads why; it does nothing when pressed. Outside the
// mode, or while acting, it is the control itself.
//
//   <Guarded><Button onClick={save}>Save</Button></Guarded>
//
// The server is not told: this is a hint, and the request guard of
// utils/viewAsStore.js is what stops the write.
export function Guarded({ children }) {
  const { t } = useTranslation('viewas')
  const { active, acting } = useViewAs()
  const hintId = useId()
  const child = Children.only(children)
  if (!active || acting || !isValidElement(child)) return child
  return (
    <span className="viewas-guard" data-testid="viewas-guard">
      {cloneElement(child, {
        'aria-disabled': true,
        'aria-describedby': hintId,
        to: undefined,
        href: undefined,
        onClick: (e) => { e.preventDefault?.() },
        'data-viewas-locked': true,
      })}
      <span id={hintId} role="tooltip" className="viewas-guard__hint">{t('write.hint')}</span>
    </span>
  )
}
