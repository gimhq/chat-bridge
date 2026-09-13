import type { AccountStatus, Chat, ChatRequest, Contact, Content, Message } from '@/shared/api/types'

export function displayContact(c: Pick<Contact, 'id' | 'name' | 'names' | 'handle'> | null | undefined): string {
  if (!c)
    return ''
  return c.name || c.names?.alias || c.names?.profile || c.names?.username || c.handle || c.id
}

export function displayChat(chat: Pick<Chat, 'id' | 'name'>): string {
  return chat.name || chat.id
}

export function senderName(m: Pick<Message, 'sender' | 'from_me'>): string {
  if (m.from_me)
    return '我'
  return m.sender.chat_name || m.sender.name || m.sender.id
}

const typeLabels: Record<string, string> = {
  image: '图片',
  video: '视频',
  audio: '音频',
  voice: '语音',
  file: '文件',
  sticker: '贴纸',
  location: '位置',
  contact: '名片',
  poll: '投票',
  call: '通话',
  payment: '支付',
  deleted: '消息已撤回',
  expired: '消息已过期',
  unsupported: '不支持的消息',
}

/** One-line preview of a message body for chat lists and reply quotes. */
export function contentPreview(c: Content): string {
  switch (c.type) {
    case 'text':
      return c.text ?? ''
    case 'system':
      return systemText(c)
    case 'call':
      return `[${c.call?.kind === 'video' ? '视频通话' : '语音通话'}${c.call?.state ? ` · ${callState(c.call.state)}` : ''}]`
    case 'location':
      return `[位置] ${c.location?.name ?? ''}`.trim()
    case 'poll':
      return `[投票] ${c.poll?.question ?? ''}`.trim()
    case 'deleted':
    case 'expired':
    case 'unsupported':
      return `[${typeLabels[c.type]}]`
    default: {
      const label = `[${typeLabels[c.type] ?? c.type}]`
      const name = c.attachments?.[0]?.file_name
      return [label, c.text || (c.type === 'file' ? name : '')].filter(Boolean).join(' ')
    }
  }
}

export function callState(state: string): string {
  return ({ ringing: '响铃中', missed: '未接', declined: '已拒绝', ended: '已结束' } as Record<string, string>)[state] ?? state
}

export function systemText(c: Content): string {
  const s = c.system
  if (!s)
    return c.text ?? '[系统消息]'
  const targets = s.targets?.join('、') ?? ''
  switch (s.kind) {
    case 'member_joined': return `${targets || s.actor || '有人'} 加入了`
    case 'member_left': return `${targets || s.actor || '有人'} 离开了`
    case 'member_added': return `${s.actor ?? '有人'} 添加了 ${targets}`
    case 'member_removed': return `${s.actor ?? '有人'} 移除了 ${targets}`
    case 'name_changed': return `群名改为「${s.value ?? ''}」`
    case 'created': return '创建了群聊'
    case 'ephemeral_changed': return s.value && s.value !== '0' ? `开启了阅后即焚（${s.value} 秒）` : '关闭了阅后即焚'
    default: return c.text || s.value || `[${s.kind}]`
  }
}

export const statusLabels: Record<AccountStatus, string> = {
  unpaired: '未登录',
  logging_in: '登录中',
  connecting: '连接中',
  connected: '已连接',
  disconnected: '已断开',
  error: '错误',
}

export type Tone = 'ok' | 'warn' | 'bad' | 'idle'

export function statusTone(s: AccountStatus): Tone {
  switch (s) {
    case 'connected': return 'ok'
    case 'connecting':
    case 'logging_in':
    case 'disconnected': return 'warn'
    case 'error': return 'bad'
    default: return 'idle'
  }
}

export const requestKindLabels: Record<ChatRequest['kind'], string> = {
  call: '来电',
  chat_invite: '群/房间邀请',
  join_request: '入群申请',
  contact_request: '好友请求',
}

export const requestStateLabels: Record<ChatRequest['state'], string> = {
  pending: '待处理',
  accepted: '已接受',
  rejected: '已拒绝',
  expired: '已过期',
  ignored: '已忽略',
}

export function formatBytes(n: number | undefined): string {
  if (!n)
    return ''
  const units = ['B', 'KB', 'MB', 'GB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

/** Short timestamp: time today, month-day this year, full date otherwise. */
export function formatTime(iso: string | null | undefined, now = new Date()): string {
  if (!iso)
    return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime()))
    return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  if (d.toDateString() === now.toDateString())
    return hm
  if (d.getFullYear() === now.getFullYear())
    return `${d.getMonth() + 1}-${pad(d.getDate())} ${hm}`
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** Local client id for idempotent sends. */
export function clientId(): string {
  return `ui-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return [d ? `${d} 天` : '', h ? `${h} 小时` : '', `${m} 分钟`].filter(Boolean).join(' ')
}
