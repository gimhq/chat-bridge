import type { ApiEvent } from './types'
import { invalidationsFor, qk } from './queries'

const ev = (type: string, data: unknown, account_id = 'a1'): ApiEvent => ({ id: '1', type, account_id, timestamp: 't', data })

describe('invalidationsFor', () => {
  it('maps events to the queries they make stale', () => {
    expect(invalidationsFor(ev('message.new', { chat_id: 'c1' }))).toEqual([qk.messages('a1', 'c1'), qk.chats('a1')])
    expect(invalidationsFor(ev('message.reaction', { chat_id: 'c2' }))).toEqual([qk.messages('a1', 'c2'), qk.chats('a1')])
    expect(invalidationsFor(ev('chat.updated', { id: 'c1' }))).toEqual([qk.chats('a1'), qk.chat('a1', 'c1')])
    expect(invalidationsFor(ev('contact.updated', {}))).toEqual([qk.contacts('a1')])
    expect(invalidationsFor(ev('account.status', {}))).toEqual([qk.accounts, qk.account('a1'), qk.status])
    expect(invalidationsFor(ev('request.new', {}))).toEqual([qk.requests('a1'), qk.account('a1'), qk.accounts])
    expect(invalidationsFor(ev('chat.typing', {}))).toEqual([])
    expect(invalidationsFor(ev('person.updated', { id: 'per_1' }, ''))).toEqual([qk.persons, qk.person('per_1'), qk.personMessages('per_1'), qk.suggestions])
  })
})
