export function createAddon(sdk) {
  const h = sdk.React.createElement
  return { Component: () => <p className="fx">fixture</p>, dispose() {} }
}
