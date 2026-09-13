import type { Person, PersonSuggestion } from '@/shared/api/types'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { mockApi, renderWithClient } from '@/test/render'
import { LinksCard } from './links-card'
import { SuggestionsCard } from './suggestions-card'

const person: Person = {
  id: 'per_1',
  name: 'Alice',
  tags: ['work'],
  notes: '',
  created_at: 't',
  updated_at: 't',
  channels: [],
  links: [
    { account_id: 'wa', user_id: '86138@s.whatsapp.net', platform: 'whatsapp', name: 'Alice', phone: '+86138', source: 'phone', linked_at: 't' },
    { account_id: 'tg', user_id: '12345', platform: 'telegram', name: 'Alice T', handle: '@alice', source: 'manual', linked_at: 't' },
  ],
}

describe('persons components', () => {
  afterEach(() => vi.restoreAllMocks())

  it('lists linked identities and unlinks one', async () => {
    const api = mockApi({ 'DELETE /v1/persons/per_1/links/tg/12345': () => ({ ...person, links: person.links.slice(0, 1) }) })
    renderWithClient(<LinksCard person={person} />)
    expect(screen.getByText('whatsapp')).toBeInTheDocument()
    expect(screen.getByText('wa · +86138 · 同手机号')).toBeInTheDocument()
    expect(screen.getByText('tg · @alice · 手动')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '解除关联 Alice T' }))
    await waitFor(() => expect(api.calls.some(c => c.method === 'DELETE' && c.path === '/v1/persons/per_1/links/tg/12345')).toBe(true))
  })

  it('groups a phone suggestion into a new person', async () => {
    const suggestion: PersonSuggestion = { reason: 'phone', contacts: [
      { account_id: 'wa', user_id: 'b@s.whatsapp.net', platform: 'whatsapp', name: 'Bob', phone: '+1555', source: 'phone', linked_at: 't' },
      { account_id: 'tg', user_id: '999', platform: 'telegram', name: 'Bob T', source: 'phone', linked_at: 't' },
    ] }
    const api = mockApi({ 'POST /v1/persons': () => ({ ...person, id: 'per_2', name: 'Bob' }) })
    renderWithClient(<SuggestionsCard suggestions={[suggestion]} />)
    expect(screen.getByText('whatsapp · Bob、telegram · Bob T')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '归为一个人' }))
    await waitFor(() => expect(api.calls).toHaveLength(1))
    expect(api.calls[0]!.body).toEqual({ name: 'Bob', links: [{ account_id: 'wa', user_id: 'b@s.whatsapp.net' }, { account_id: 'tg', user_id: '999' }] })
  })
})
