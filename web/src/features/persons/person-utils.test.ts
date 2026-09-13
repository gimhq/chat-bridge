import { linkLabel, parseTags, sourceLabels } from './person-utils'

describe('person utils', () => {
  it('parses comma separated tags', () => {
    expect(parseTags('work, vip，work , ,family')).toEqual(['work', 'vip', 'family'])
    expect(parseTags('')).toEqual([])
  })

  it('labels links by platform and name', () => {
    expect(linkLabel({ account_id: 'wa', user_id: 'u1', platform: 'whatsapp', name: 'Alice' })).toBe('whatsapp · Alice')
    expect(linkLabel({ account_id: 'wa', user_id: 'u1' })).toBe('wa · u1')
    expect(sourceLabels.phone).toBe('同手机号')
  })
})
