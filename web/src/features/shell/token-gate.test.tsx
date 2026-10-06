import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { clearToken, getToken } from '@/shared/lib/token'
import { TokenGate } from './token-gate'

describe('tokenGate', () => {
  beforeEach(() => clearToken())
  afterEach(() => vi.restoreAllMocks())

  it('rejects a wrong token and stores a valid one', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response('{}', { status: 401 }))
      .mockResolvedValueOnce(new Response('{"version":"t"}', { status: 200 }))
    render(<TokenGate />)
    const input = screen.getByLabelText('访问令牌')
    const submit = screen.getByRole('button', { name: '进入' })
    expect(submit).toBeDisabled()

    fireEvent.change(input, { target: { value: 'wrong' } })
    fireEvent.click(submit)
    expect(await screen.findByText('令牌无效')).toBeInTheDocument()
    expect(getToken()).toBe('')

    fireEvent.change(input, { target: { value: '  secret-1  ' } })
    fireEvent.click(submit)
    await waitFor(() => expect(getToken()).toBe('secret-1'))
    expect(new Headers(fetchSpy.mock.calls[1]![1]!.headers).get('Authorization')).toBe('Bearer secret-1')
  })

  it('explains that a scoped token cannot sign in', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('{}', { status: 403 }))
    render(<TokenGate />)
    fireEvent.change(screen.getByLabelText('访问令牌'), { target: { value: 'cbt_scoped' } })
    fireEvent.click(screen.getByRole('button', { name: '进入' }))
    expect(await screen.findByText(/管理界面需要管理员令牌/)).toBeInTheDocument()
    expect(getToken()).toBe('')
  })
})
