import type { Chat, Contact, Message } from '@/shared/api/types'
import { fireEvent, screen } from '@testing-library/react'
import { mockApi, renderWithClient } from '@/test/render'
import { ContactDetail } from './contact-detail'

const contact: Contact = {
  id: 'u1',
  name: '',
  names: { alias: '向日葵', alias_source: 'local', profile: 'youli' },
  phone: '+66995618240',
  avatar: null,
  is_self: false,
  is_contact: true,
  blocked: false,
  updated_at: '2026-09-14T00:00:00Z',
}

function chat(id: string, kind: Chat['kind'], name: string): Chat {
  return { id, account_id: 'wa', kind, name, avatar: null, unread_count: 0, last_message_at: '2026-09-14T00:00:00Z', muted: false, archived: false, tags: [], pinned_message_ids: [], ephemeral_ttl_s: null }
}

function msg(id: string, chatId: string, text: string): Message {
  return { id, account_id: 'wa', chat_id: chatId, sender: { id: 'u1', name: '向日葵' }, from_me: false, timestamp: '2026-09-14T00:00:00Z', content: { type: 'text', text }, mentions: [], forwarded: false, ephemeral: null, edited_at: null, deleted_at: null, reactions: [] }
}

describe('contactDetail', () => {
  afterEach(() => vi.restoreAllMocks())

  it('shows the profile, shared chats and a timeline that widens to groups', async () => {
    const api = mockApi({
      'GET /v1/accounts/wa': () => ({ id: 'wa', platform: 'whatsapp', status: 'connected', capabilities: [] }),
      'GET /v1/accounts/wa/contacts/u1': () => contact,
      'GET /v1/accounts/wa/contacts/u1/chats': () => ({ chats: [chat('u1', 'direct', '向日葵'), chat('g1', 'group', 'Team')] }),
      'GET /v1/accounts/wa/contacts/u1/messages': (_, url) => ({
        messages: url.searchParams.get('scope') === 'all' ? [msg('g', 'g1', 'in the group'), msg('d', 'u1', 'hello')] : [msg('d', 'u1', 'hello')],
      }),
      'GET /v1/accounts/wa/requests': () => ({ requests: [] }),
      'GET /v1/persons': () => ({ persons: [] }),
    })
    renderWithClient(<ContactDetail accountId="wa" userId="u1" />)

    expect(await screen.findByRole('heading', { name: '向日葵' })).toBeInTheDocument()
    expect(screen.getByText('向日葵（本地）')).toBeInTheDocument()
    expect(screen.getByText('youli')).toBeInTheDocument()
    expect(screen.getByText('+66995618240', { selector: 'dd' })).toBeInTheDocument()
    expect(await screen.findByText('Team')).toBeInTheDocument()
    expect(await screen.findByText('hello')).toBeInTheDocument()
    expect(screen.queryByText('in the group')).toBeNull()
    expect(api.calls.find(c => c.path === '/v1/accounts/wa/requests')).toBeDefined()

    fireEvent.click(screen.getByRole('tab', { name: '含群聊' }))
    expect(await screen.findByText('in the group')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '关联到人' }))
    expect(await screen.findByText('关联「向日葵」')).toBeInTheDocument()
  })

  it('reports an unknown contact', async () => {
    mockApi({})
    renderWithClient(<ContactDetail accountId="wa" userId="nobody" />)
    expect(await screen.findByText(/no mock for GET/)).toBeInTheDocument()
  })
})
