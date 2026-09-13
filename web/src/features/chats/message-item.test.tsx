import type { Message } from '@/shared/api/types'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { mockApi, renderWithClient } from '@/test/render'
import { MessageItem } from './message-item'

function msg(p: Partial<Message>): Message {
  return {
    id: 'm1',
    account_id: 'a1',
    chat_id: 'c1',
    sender: { id: 'u1', name: 'Bob' },
    from_me: false,
    timestamp: new Date().toISOString(),
    content: { type: 'text', text: 'hi' },
    mentions: [],
    forwarded: false,
    ephemeral: null,
    edited_at: null,
    deleted_at: null,
    reactions: [],
    ...p,
  }
}

const props = { accountId: 'a1', showSender: true, capabilities: ['message.reply', 'message.reaction', 'message.delete'], connected: true, onReply: () => {} }

describe('messageItem', () => {
  afterEach(() => vi.restoreAllMocks())

  it('shows html as text, quotes replies and groups reactions', () => {
    const target = msg({ id: 'm0', content: { type: 'text', text: 'original' } })
    const { container } = renderWithClient(
      <MessageItem
        {...props}
        replyTarget={target}
        message={msg({ reply_to: 'm0', forwarded: true, content: { type: 'text', text: '<b>hi</b> there', format: 'html' }, reactions: [{ emoji: '👍', sender_id: 'a' }, { emoji: '👍', sender_id: 'b' }] })}
      />,
    )
    expect(screen.getByText('hi there')).toBeInTheDocument()
    expect(container.querySelector('b')).toBeNull()
    expect(screen.getByText('Bob：original')).toBeInTheDocument()
    expect(screen.getByText('转发')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '👍 2' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '撤回' })).not.toBeInTheDocument()
  })

  it('retracts own messages and replies', async () => {
    const api = mockApi({ 'DELETE /v1/accounts/a1/messages/m1': () => msg({ content: { type: 'deleted' } }) })
    const onReply = vi.fn()
    renderWithClient(<MessageItem {...props} onReply={onReply} message={msg({ from_me: true, status: 'read' })} />)
    expect(screen.getByLabelText('已读')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '回复' }))
    expect(onReply).toHaveBeenCalledOnce()
    fireEvent.click(screen.getByRole('button', { name: '撤回' }))
    await waitFor(() => expect(api.calls.some(c => c.method === 'DELETE')).toBe(true))
  })

  it('renders system, call, deleted, location and contact bodies', () => {
    const { unmount } = renderWithClient(<MessageItem {...props} message={msg({ content: { type: 'system', system: { kind: 'name_changed', value: 'Team' } } })} />)
    expect(screen.getByText(/群名改为「Team」/)).toBeInTheDocument()
    unmount()
    renderWithClient(
      <>
        <MessageItem {...props} message={msg({ id: 'a', content: { type: 'call', call: { kind: 'video', state: 'missed' } } })} />
        <MessageItem {...props} message={msg({ id: 'b', content: { type: 'deleted' } })} />
        <MessageItem {...props} message={msg({ id: 'c', content: { type: 'location', location: { lat: 1.5, lon: 2.25, name: 'Home' } } })} />
        <MessageItem {...props} message={msg({ id: 'd', content: { type: 'contact', contacts: [{ name: 'Carol', phones: ['+1'] }] } })} />
      </>,
    )
    expect(screen.getByText(/视频通话 · 未接/)).toBeInTheDocument()
    expect(screen.getByText('[消息已撤回]')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Home/ })).toHaveAttribute('href', expect.stringMatching(/^https:\/\/www\.openstreetmap\.org\//))
    expect(screen.getByText('Carol')).toBeInTheDocument()
    expect(screen.getByText('· +1')).toBeInTheDocument()
    expect(screen.getByText('视频通话 · 未接')).toBeInTheDocument()
  })

  it('downloads remote attachments on request', async () => {
    const api = mockApi({ 'POST /v1/media/md1/fetch': () => ({ media_id: 'md1', mime: 'application/pdf', state: 'pending' }) })
    renderWithClient(<MessageItem {...props} message={msg({ content: { type: 'file', attachments: [{ media_id: 'md1', mime: 'application/pdf', state: 'remote', file_name: 'a.pdf', size: 2048 }] } })} />)
    expect(screen.getByText('a.pdf')).toBeInTheDocument()
    expect(screen.getByText('2.0 KB')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '下载' }))
    await waitFor(() => expect(api.calls.some(c => c.path === '/v1/media/md1/fetch')).toBe(true))
  })
})
