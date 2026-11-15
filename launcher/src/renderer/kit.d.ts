// Types for the parts of the shared UI kit package (JavaScript, no types of its
// own) that the launcher uses. Props follow the comments in each kit file.
declare module '@localai/ui-kit' {
  import type { ButtonHTMLAttributes, ComponentType, ForwardRefExoticComponent, ReactNode, RefAttributes } from 'react'

  type Tone = 'ok' | 'warn' | 'error' | 'idle' | 'accent'

  export interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'type'> {
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'danger-ghost'
    size?: 'md' | 'sm' | 'lg'
    icon?: string
    iconEnd?: string
    busy?: boolean
    href?: string
    type?: 'button' | 'submit' | 'reset'
    children?: ReactNode
    [data: `data-${string}`]: string | undefined
  }
  export const Button: ForwardRefExoticComponent<ButtonProps & RefAttributes<HTMLButtonElement>>

  export const StatusChip: ComponentType<{
    status?: string
    tone?: Tone
    label?: string
    dot?: boolean
    icon?: string
    className?: string
    title?: string
    testId?: string
    children?: ReactNode
  }>

  export const Switch: ComponentType<{
    checked: boolean
    onChange?: (next: boolean) => void
    label?: string
    labelledBy?: string
    describedBy?: string
    disabled?: boolean
    id?: string
    className?: string
    testId?: string
  }>

  export const Segmented: ComponentType<{
    options: { id: string; label: ReactNode; count?: number; disabled?: boolean }[]
    value: string
    onChange?: (id: string) => void
    label: string
    className?: string
    testId?: string
    itemTestId?: string
  }>

  export const Callout: ComponentType<{
    tone?: 'info' | 'ok' | 'warn' | 'error'
    title?: ReactNode
    icon?: string
    actions?: ReactNode
    role?: string
    className?: string
    testId?: string
    children?: ReactNode
  }>

  export interface NoticeAction {
    label: string
    onClick?: () => void
    busy?: boolean
    disabled?: boolean
    testId?: string
    ariaLabel?: string
    variant?: string
  }
  export const Notice: ComponentType<{
    severity?: 'info' | 'warn' | 'error'
    title?: ReactNode
    reason?: ReactNode
    who?: ReactNode
    fix?: NoticeAction
    secondary?: NoticeAction
    icon?: string
    className?: string
    testId?: string
    children?: ReactNode
  }>

  export const EmptyState: ComponentType<{
    variant?: 'first-run' | 'no-results' | 'unavailable'
    title?: ReactNode
    body?: ReactNode
    actions?: ReactNode
    icon?: string
    level?: number
    className?: string
    testId?: string
    children?: ReactNode
  }>

  export const Page: ComponentType<{
    width?: 'content' | 'full'
    as?: string
    className?: string
    children?: ReactNode
    [attr: string]: unknown
  }>

  export const SearchField: ComponentType<{
    value: string
    onChange?: (text: string) => void
    label: string
    placeholder?: string
    className?: string
    testId?: string
  }>

  export const Icon: ComponentType<{ name: string; size?: number | string; className?: string; title?: string; spin?: boolean }>
}
