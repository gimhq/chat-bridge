import type { Account } from '@/shared/api/types'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { mockApi, renderWithClient } from '@/test/render'
import { LoginPanel } from './login-panel'

const account = { id: 'wa', platform: 'whatsapp', status: 'unpaired', capabilities: [], self: null, error: null } as unknown as Account
const platforms = { platforms: [{ id: 'whatsapp', name: 'WhatsApp', capabilities: [], instances: [], login_flows: [{ id: 'phone', name: '手机号配对' }] }] }

describe('loginPanel', () => {
  afterEach(() => vi.restoreAllMocks())

  it('walks input and display steps', async () => {
    // Stateful like the server: GET returns whatever step the last POST produced.
    let current: unknown
    const api = mockApi({
      'GET /v1/platforms': () => platforms,
      'POST /v1/accounts/wa/login': () => (current = { flow: 'phone', step: 'input', input: { fields: [{ name: 'phone', type: 'phone' }] } }),
      'POST /v1/accounts/wa/login/submit': () => (current = { flow: 'phone', step: 'display', display: { type: 'code', data: 'ABCD-EFGH' } }),
      'GET /v1/accounts/wa/login': () => current,
    })
    renderWithClient(<LoginPanel account={account} />)
    fireEvent.click(await screen.findByRole('button', { name: '手机号配对' }))
    const phone = await screen.findByLabelText('手机号')
    expect(phone).toHaveAttribute('type', 'tel')
    fireEvent.change(phone, { target: { value: '+8613800000000' } })
    fireEvent.click(screen.getByRole('button', { name: '继续' }))
    expect(await screen.findByText('ABCD-EFGH')).toBeInTheDocument()
    expect(api.calls.find(c => c.path === '/v1/accounts/wa/login/submit')?.body).toEqual({ fields: { phone: '+8613800000000' } })
    expect(api.calls.find(c => c.method === 'POST' && c.path === '/v1/accounts/wa/login')?.body).toEqual({ flow: 'phone' })
  })

  it('shows a failed step with retry', async () => {
    let starts = 0
    mockApi({
      'GET /v1/platforms': () => platforms,
      'POST /v1/accounts/wa/login': () => {
        starts++
        return { flow: 'phone', step: 'failed', error: { code: 'platform_error', message: '号码无效' } }
      },
      'GET /v1/accounts/wa/login': () => ({ flow: 'phone', step: 'failed', error: { code: 'platform_error', message: '号码无效' } }),
    })
    renderWithClient(<LoginPanel account={account} />)
    fireEvent.click(await screen.findByRole('button', { name: '手机号配对' }))
    expect(await screen.findByText('号码无效')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(starts).toBe(2))
  })
})
