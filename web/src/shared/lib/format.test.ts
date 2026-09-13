import type { Content } from '@/shared/api/types'
import { callState, clientId, contentPreview, displayChat, displayContact, formatBytes, formatTime, senderName, statusTone, systemText } from './format'

describe('format helpers', () => {
  it('previews every content kind', () => {
    const cases: [Content, string][] = [
      [{ type: 'text', text: 'hi' }, 'hi'],
      [{ type: 'image', text: 'look' }, '[图片] look'],
      [{ type: 'file', attachments: [{ media_id: 'm', mime: 'application/pdf', state: 'ready', file_name: 'a.pdf' }] }, '[文件] a.pdf'],
      [{ type: 'call', call: { kind: 'video', state: 'missed' } }, '[视频通话 · 未接]'],
      [{ type: 'location', location: { lat: 1, lon: 2, name: 'Home' } }, '[位置] Home'],
      [{ type: 'poll', poll: { question: 'Lunch?', options: [], multi: false, closed: false } }, '[投票] Lunch?'],
      [{ type: 'deleted' }, '[消息已撤回]'],
      [{ type: 'system', system: { kind: 'name_changed', value: 'Team' } }, '群名改为「Team」'],
      [{ type: 'sticker' }, '[贴纸]'],
    ]
    for (const [c, want] of cases)
      expect(contentPreview(c)).toBe(want)
  })

  it('renders system notices', () => {
    expect(systemText({ type: 'system', system: { kind: 'member_added', actor: 'A', targets: ['B', 'C'] } })).toBe('A 添加了 B、C')
    expect(systemText({ type: 'system', system: { kind: 'ephemeral_changed', value: '0' } })).toBe('关闭了阅后即焚')
    expect(systemText({ type: 'system', system: { kind: 'ephemeral_changed', value: '86400' } })).toBe('开启了阅后即焚（86400 秒）')
    expect(systemText({ type: 'system', system: { kind: 'other', value: 'x' } })).toBe('x')
    expect(systemText({ type: 'system', text: 'raw' })).toBe('raw')
    expect(callState('ringing')).toBe('响铃中')
    expect(callState('odd')).toBe('odd')
  })

  it('picks display names', () => {
    expect(displayContact({ id: 'u1', name: '', names: { profile: 'Alice' } })).toBe('Alice')
    expect(displayContact({ id: 'u1', name: '', names: {}, handle: '+1' })).toBe('+1')
    expect(displayContact(null)).toBe('')
    expect(displayChat({ id: 'c1' })).toBe('c1')
    expect(senderName({ from_me: true, sender: { id: 'x' } })).toBe('我')
    expect(senderName({ from_me: false, sender: { id: 'x', name: 'Bob', chat_name: 'B' } })).toBe('B')
  })

  it('formats sizes, times and tones', () => {
    expect(formatBytes(undefined)).toBe('')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(20 * 1024 * 1024)).toBe('20 MB')
    const now = new Date(2026, 8, 13, 12, 0)
    expect(formatTime(new Date(2026, 8, 13, 9, 5).toISOString(), now)).toBe('09:05')
    expect(formatTime(new Date(2026, 2, 1, 9, 5).toISOString(), now)).toBe('3-01 09:05')
    expect(formatTime(new Date(2024, 0, 2).toISOString(), now)).toBe('2024-01-02')
    expect(formatTime('nope', now)).toBe('')
    expect(formatTime(null)).toBe('')
    expect(statusTone('connected')).toBe('ok')
    expect(statusTone('disconnected')).toBe('warn')
    expect(statusTone('error')).toBe('bad')
    expect(statusTone('unpaired')).toBe('idle')
    expect(clientId()).toMatch(/^ui-/)
    expect(clientId()).not.toBe(clientId())
  })
})
