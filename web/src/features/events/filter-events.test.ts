import type { ApiEvent } from '@/shared/api/types'
import { formatUptime } from '@/shared/lib/format'
import { filterEvents } from './filter-events'

const ev = (id: string, type: string, account_id?: string): ApiEvent => ({ id, type, account_id, timestamp: 't', data: {} })

describe('filterEvents', () => {
  it('matches every term against type or account', () => {
    const list = [ev('1', 'message.new', 'wa-main'), ev('2', 'request.new', 'tg-main'), ev('3', 'account.status', 'wa-main')]
    expect(filterEvents(list, '').map(e => e.id)).toEqual(['1', '2', '3'])
    expect(filterEvents(list, 'WA').map(e => e.id)).toEqual(['1', '3'])
    expect(filterEvents(list, 'new tg').map(e => e.id)).toEqual(['2'])
  })

  it('formats uptime', () => {
    expect(formatUptime(59)).toBe('0 分钟')
    expect(formatUptime(3 * 86400 + 2 * 3600 + 5 * 60)).toBe('3 天 2 小时 5 分钟')
  })
})
