import type { Platform } from '@/shared/api/types'
import { buildConfig, inputProps, safeHttpUrl } from './account-utils'

describe('account utils', () => {
  it('converts form values to the config schema types', () => {
    const platform = { id: 't', name: 'T', capabilities: [], login_flows: [], instances: [], config_schema: { properties: {
      api_id: { type: 'integer' },
      api_hash: { 'type': 'string', 'x-secret': true },
      e2ee: { type: 'boolean' },
      device_name: { type: 'string' },
    } } } satisfies Platform
    expect(buildConfig(platform, { api_id: '123', api_hash: 'h', e2ee: true, device_name: '', stray: 'x' })).toEqual({ api_id: 123, api_hash: 'h', e2ee: true })
    expect(buildConfig(undefined, { a: 'b' })).toEqual({})
  })

  it('maps login field types to input attributes', () => {
    expect(inputProps({ name: 'phone', type: 'phone' })).toMatchObject({ type: 'tel', inputMode: 'tel' })
    expect(inputProps({ name: 'p', type: 'password' }).type).toBe('password')
    expect(inputProps({ name: 'c', type: 'code' })).toMatchObject({ autoComplete: 'one-time-code', inputMode: 'numeric' })
    expect(inputProps({ name: 'u', type: 'url' }).type).toBe('url')
    expect(inputProps({ name: 'x', type: 'text' }).type).toBe('text')
  })

  it('only links http(s) urls', () => {
    expect(safeHttpUrl('https://example.org/sso')).toBe('https://example.org/sso')
    expect(safeHttpUrl('javascript:alert(1)')).toBeUndefined()
    expect(safeHttpUrl('not a url')).toBeUndefined()
  })
})
