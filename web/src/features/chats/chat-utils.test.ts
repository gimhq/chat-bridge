import { contentTypeFor, groupReactions, initials, parseMembers, plainText } from './chat-utils'

describe('chat utils', () => {
  it('derives avatar initials', () => {
    expect(initials('@alice:example.org')).toBe('AL')
    expect(initials('+8613800000000')).toBe('86')
    expect(initials('')).toBe('?')
  })

  it('parses member lists with mixed separators and duplicates', () => {
    expect(parseMembers('a@s, b@s；c@s\nb@s  ')).toEqual(['a@s', 'b@s', 'c@s'])
    expect(parseMembers('   ')).toEqual([])
  })

  it('renders html bodies as text without markup', () => {
    expect(plainText('<b>hi</b><br>there<script>x</script>', 'html')).toBe('hi\nthere' + 'x')
    expect(plainText('<b>raw</b>')).toBe('<b>raw</b>')
  })

  it('groups reactions by emoji', () => {
    expect(groupReactions([{ emoji: '👍', sender_id: 'a' }, { emoji: '❤️', sender_id: 'b' }, { emoji: '👍', sender_id: 'c' }]))
      .toEqual([{ emoji: '👍', count: 2, senders: ['a', 'c'] }, { emoji: '❤️', count: 1, senders: ['b'] }])
  })

  it('chooses content types from mime', () => {
    expect(contentTypeFor('image/png')).toBe('image')
    expect(contentTypeFor('video/mp4')).toBe('video')
    expect(contentTypeFor('audio/ogg')).toBe('audio')
    expect(contentTypeFor('application/pdf')).toBe('file')
  })
})
