// Resource apply returns the receipt payload directly in result; reads use payload.
export const unpack = response => response?.payload ?? response?.result?.payload ?? response?.result ?? response
