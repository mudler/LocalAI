// Network exposure of the server: which address it listens on and how other
// devices reach it. Pure, no Electron import.
//
// "Exposed" means the server listens on more than the loopback interface. The
// launcher uses 0.0.0.0:<port> to expose and 127.0.0.1:<port> to stop. The
// WebUI view always uses the loopback URL (serverLaunch.endpointFor), so it
// keeps working either way.
import type os from 'node:os'

export interface HostPort {
  host: string
  port: number
}

/** Splits host:port; a missing port is 8080 (the server's default). Brackets of an IPv6 host are kept. */
export function splitAddress(address: string): HostPort {
  const i = address.lastIndexOf(':')
  if (i < 0 || address.endsWith(']')) return { host: address, port: 8080 }
  const port = Number(address.slice(i + 1))
  return { host: address.slice(0, i), port: Number.isInteger(port) && port > 0 && port <= 65535 ? port : 8080 }
}

/**
 * A saved address is host:port. An older or hand-edited config may hold a URL
 * ("http://localhost:8080/"); the server rejects that with "too many colons",
 * so the scheme and any path are dropped. Anything else is returned as is.
 */
export function normalizeAddress(address: string): string {
  const m = /^[a-z][a-z0-9+.-]*:\/\/([^/?#]*)/i.exec(address.trim())
  if (!m) return address
  const hp = m[1].replace(/^[^@]*@/, '')
  if (hp === '') return '127.0.0.1:8080'
  const i = hp.lastIndexOf(':')
  return i < 0 || hp.endsWith(']') ? `${hp}:8080` : hp
}

export function isLoopbackHost(host: string): boolean {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase()
  if (h === 'localhost' || h === '::1') return true
  return /^127(\.\d{1,3}){3}$/.test(h)
}

/** An empty host (":8080"), a wildcard or a LAN address all reach past this computer. */
export function isExposedAddress(address: string): boolean {
  return !isLoopbackHost(splitAddress(address).host)
}

export function exposedAddress(address: string): string {
  return `0.0.0.0:${splitAddress(address).port}`
}

export function loopbackAddress(address: string): string {
  return `127.0.0.1:${splitAddress(address).port}`
}

export const MAX_LAN_ADDRESSES = 8

/**
 * Interfaces of containers and virtual machines on this computer. Their
 * addresses are not reachable from other devices, so they are not offered.
 */
const VIRTUAL_IFACE = /^(docker|br-|veth|virbr|vboxnet|vmnet|cni|flannel|podman|lxcbr|lxdbr)/

/**
 * The origins other devices can use: one per non-internal IPv4 address of
 * this computer, except container and VM bridges, sorted, at most
 * MAX_LAN_ADDRESSES. When the server listens on one address only, that
 * address alone.
 */
export function lanAddresses(address: string, ifaces: NodeJS.Dict<os.NetworkInterfaceInfo[]>): string[] {
  const { host, port } = splitAddress(address)
  if (isLoopbackHost(host)) return []
  if (host !== '' && host !== '0.0.0.0' && host !== '[::]' && host !== '::') {
    return [`http://${host}:${port}`]
  }
  const ips = new Set<string>()
  for (const [name, list] of Object.entries(ifaces)) {
    if (VIRTUAL_IFACE.test(name)) continue
    for (const a of list ?? []) {
      // Node 18.0-18.3 reported family as a number.
      const v4 = a.family === 'IPv4' || (a.family as unknown) === 4
      if (v4 && !a.internal && /^\d{1,3}(\.\d{1,3}){3}$/.test(a.address)) ips.add(a.address)
    }
  }
  const sorted = [...ips].sort((a, b) => {
    const pa = a.split('.').map(Number)
    const pb = b.split('.').map(Number)
    for (let i = 0; i < 4; i++) if (pa[i] !== pb[i]) return pa[i] - pb[i]
    return 0
  })
  return sorted.slice(0, MAX_LAN_ADDRESSES).map((ip) => `http://${ip}:${port}`)
}
