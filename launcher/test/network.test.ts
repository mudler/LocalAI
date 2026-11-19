import type os from 'node:os'
import { describe, expect, it } from 'vitest'
import { exposedAddress, normalizeAddress, isExposedAddress, isLoopbackHost, lanAddresses, loopbackAddress, splitAddress } from '../src/main/network'
import { buildServerLaunch, endpointFor } from '../src/main/serverLaunch'
import { envNameProblem } from '../src/shared/envVars'

const iface = (address: string, family: 'IPv4' | 'IPv6', internal = false) =>
  ({ address, family, internal, netmask: '', mac: '', cidr: null }) as unknown as os.NetworkInterfaceInfo

const IFACES: NodeJS.Dict<os.NetworkInterfaceInfo[]> = {
  lo: [iface('127.0.0.1', 'IPv4', true), iface('::1', 'IPv6', true)],
  eth0: [iface('192.0.2.24', 'IPv4'), iface('fe80::1', 'IPv6')],
  wlan0: [iface('192.0.2.7', 'IPv4')],
  docker0: [iface('192.0.2.200', 'IPv4')],
  'br-0a1b2c': [iface('192.0.2.201', 'IPv4')],
  virbr0: [iface('192.0.2.202', 'IPv4')],
  tun0: undefined as unknown as os.NetworkInterfaceInfo[],
}

describe('addresses', () => {
  it('splits host and port', () => {
    expect(splitAddress('127.0.0.1:8080')).toEqual({ host: '127.0.0.1', port: 8080 })
    expect(splitAddress(':9090')).toEqual({ host: '', port: 9090 })
    expect(splitAddress('[::1]:8081')).toEqual({ host: '[::1]', port: 8081 })
    expect(splitAddress('[::1]')).toEqual({ host: '[::1]', port: 8080 })
  })

  it('tells loopback from exposed', () => {
    for (const h of ['127.0.0.1', '127.1.2.3', 'localhost', '[::1]', '::1']) expect(isLoopbackHost(h), h).toBe(true)
    for (const a of ['0.0.0.0:8080', ':8080', '192.0.2.24:8080', '[::]:8080', 'host.example.test:8080']) expect(isExposedAddress(a), a).toBe(true)
    expect(isExposedAddress('127.0.0.1:8080')).toBe(false)
  })

  it('exposes on all IPv4 interfaces and returns to loopback, keeping the port', () => {
    expect(exposedAddress('127.0.0.1:8081')).toBe('0.0.0.0:8081')
    expect(loopbackAddress('0.0.0.0:8081')).toBe('127.0.0.1:8081')
  })

  it('lists non-internal IPv4 addresses only, sorted, without container and VM bridges', () => {
    expect(lanAddresses('0.0.0.0:8080', IFACES)).toEqual(['http://192.0.2.7:8080', 'http://192.0.2.24:8080'])
    expect(lanAddresses('127.0.0.1:8080', IFACES)).toEqual([])
    expect(lanAddresses('192.0.2.24:8080', IFACES)).toEqual(['http://192.0.2.24:8080'])
  })

  it('the WebUI keeps the loopback origin when the server is exposed', () => {
    expect(endpointFor('0.0.0.0:8080')).toBe('http://127.0.0.1:8080')
  })
})

describe('server launch', () => {
  const cfg = { models_path: '/m', backends_path: '/b', address: '0.0.0.0:8080', log_level: 'info' }

  it('passes the API key and user variables in the environment, never on the command line', () => {
    const key = 'lai-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
    const l = buildServerLaunch(cfg, '/d', { PATH: '/usr/bin', API_KEY: 'stray' }, undefined, { apiKey: key, env: { HF_TOKEN: 'tok-123456789', PATH: '/evil' } })
    expect(l.env.LOCALAI_API_KEY).toBe(key)
    expect(l.env.HF_TOKEN).toBe('tok-123456789')
    expect(l.env.PATH).toBe('/usr/bin')
    expect(l.env.API_KEY).toBeUndefined()
    expect(l.args.join(' ')).not.toContain(key)
    expect(l.args.join(' ')).not.toContain('tok-123456789')
    expect(l.args).toContain('--address')
    expect(l.args[l.args.indexOf('--address') + 1]).toBe('0.0.0.0:8080')
    expect(l.endpoint).toBe('http://127.0.0.1:8080')
  })

  it('without a key, the server gets no key variable', () => {
    const l = buildServerLaunch(cfg, '/d', { LOCALAI_API_KEY: 'inherited' })
    expect(l.env.LOCALAI_API_KEY).toBeUndefined()
  })
})

describe('environment variable names', () => {
  it('refuses bad shapes, long names and names the launcher controls', () => {
    expect(envNameProblem('HF_TOKEN')).toBeUndefined()
    expect(envNameProblem('_x1')).toBeUndefined()
    expect(envNameProblem('1A')).toBe('format')
    expect(envNameProblem('A-B')).toBe('format')
    expect(envNameProblem('')).toBe('length')
    expect(envNameProblem('A'.repeat(129))).toBe('length')
    for (const n of ['ELECTRON_RUN_AS_NODE', 'APPDIR', 'APPIMAGE', 'LD_LIBRARY_PATH', 'ARGV0', 'PATH', 'LOCALAI_API_KEY', 'API_KEY', 'LOCALAI_ADDRESS', 'LOCALAI_BASE_PATH', 'ELECTRON_X', 'APPIMAGE_X']) {
      expect(envNameProblem(n), n).toBe('controlled')
    }
  })
})

describe('a URL saved as the address', () => {
  const cfg = { models_path: '/m', backends_path: '/b', address: 'http://localhost:8080', log_level: 'info' }
  it('is reduced to host:port', () => {
    expect(normalizeAddress('http://localhost:8080')).toBe('localhost:8080')
    expect(normalizeAddress('https://user:pw@example.com:9000/path?x=1')).toBe('example.com:9000')
    expect(normalizeAddress('http://[::1]:8080/')).toBe('[::1]:8080')
    expect(normalizeAddress('http://localhost')).toBe('localhost:8080')
  })
  it('leaves host:port values alone', () => {
    for (const a of ['127.0.0.1:8080', ':8080', '0.0.0.0:9', '[::]:8080', 'localhost:8080']) expect(normalizeAddress(a)).toBe(a)
  })
  it('does not reach the server as a URL', () => {
    const l = buildServerLaunch(cfg, '/d', {})
    expect(l.args[l.args.indexOf('--address') + 1]).toBe('localhost:8080')
    expect(l.endpoint).toBe('http://localhost:8080')
  })
})
