// Stand-in for `react/jsx-runtime`: JSX becomes createElement of the host React.
import { getSdk } from '../runtime.js'

const React = getSdk().React
export const Fragment = React.Fragment
export function jsx(type, props, key) {
  return key === undefined ? React.createElement(type, props) : React.createElement(type, { ...props, key })
}
export const jsxs = jsx
export const jsxDEV = jsx
