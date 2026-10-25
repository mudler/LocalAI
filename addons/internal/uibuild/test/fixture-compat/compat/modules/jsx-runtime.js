export const Fragment = Symbol.for('fragment')
export function jsx(type, props, key) { return globalThis.__fixtureSdk.React.createElement(type, key === undefined ? props : { ...props, key }) }
export const jsxs = jsx
export const jsxDEV = jsx
