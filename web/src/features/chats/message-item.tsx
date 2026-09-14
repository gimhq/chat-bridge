import type { Message } from '@/shared/api/types'
import { Link } from '@tanstack/react-router'
import { CheckCheckIcon, CheckIcon, ForwardIcon, MapPinIcon, PhoneIcon, ReplyIcon, SmilePlusIcon, Trash2Icon, VideoIcon } from 'lucide-react'
import { toast } from 'sonner'
import { useMessageActions } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/shared/components/ui/dropdown-menu'
import { callState, contentPreview, formatTime, senderName, systemText } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { cn } from '@/shared/lib/utils'
import { AttachmentView } from './attachment-view'
import { groupReactions, plainText } from './chat-utils'

const QUICK_EMOJI = ['👍', '❤️', '😂', '😮', '😢', '🙏']

/** HTML bodies (Matrix) are shown as text: never inject platform HTML into the page. */

interface Props {
  accountId: string
  message: Message
  replyTarget?: Message
  showSender: boolean
  capabilities: string[]
  connected: boolean
  onReply: (m: Message) => void
}

export function MessageItem({ accountId, message: m, replyTarget, showSender, capabilities, connected, onReply }: Props) {
  const actions = useMessageActions(accountId, m.chat_id)
  const c = m.content
  if (c.type === 'system') {
    return (
      <p className="self-center py-1 text-center text-xs text-muted-foreground">
        {systemText(c)}
        {' '}
        ·
        {' '}
        {formatTime(m.timestamp)}
      </p>
    )
  }
  const canReact = connected && capabilities.includes('message.reaction') && c.type !== 'deleted'
  const canDelete = connected && m.from_me && capabilities.includes('message.delete') && c.type !== 'deleted'
  const react = (emoji: string, remove: boolean) =>
    actions.react.mutate({ message: m.id, emoji, remove }, { onError: e => toast.error(errorMessage(e)) })

  return (
    <div className={cn('group flex max-w-[85%] flex-col gap-0.5', m.from_me ? 'items-end self-end' : 'items-start self-start')}>
      {showSender && !m.from_me && (
        <Link to="/accounts/$accountId/contacts/$userId" params={{ accountId, userId: m.sender.id }} className="px-1 text-xs text-muted-foreground underline-offset-4 hover:underline">
          {senderName(m)}
        </Link>
      )}
      <div className={cn('flex items-center gap-1', m.from_me && 'flex-row-reverse')}>
        <div className={cn('rounded-2xl px-3 py-2 text-sm', m.from_me ? 'bg-primary text-primary-foreground' : 'bg-muted')}>
          {m.forwarded && (
            <p className="mb-1 flex items-center gap-1 text-xs opacity-70">
              <ForwardIcon className="size-3" aria-hidden />
              转发
            </p>
          )}
          {m.reply_to && (
            <p className="mb-1 border-l-2 border-current/40 pl-2 text-xs opacity-80">
              {replyTarget ? `${senderName(replyTarget)}：${contentPreview(replyTarget.content)}` : '回复一条消息'}
            </p>
          )}
          <Body accountId={accountId} message={m} />
        </div>
        <div className="flex opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          {connected && capabilities.includes('message.reply') && (
            <Button variant="ghost" size="icon-xs" aria-label="回复" onClick={() => onReply(m)}><ReplyIcon /></Button>
          )}
          {canReact && (
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button variant="ghost" size="icon-xs" aria-label="添加表情" />}>
                <SmilePlusIcon />
              </DropdownMenuTrigger>
              <DropdownMenuContent className="flex min-w-0 gap-0.5 p-1">
                {QUICK_EMOJI.map(e => <DropdownMenuItem key={e} className="text-base" onClick={() => react(e, false)}>{e}</DropdownMenuItem>)}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          {canDelete && (
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="撤回"
              onClick={() => actions.remove.mutate(m.id, { onSuccess: () => toast.success('已撤回'), onError: e => toast.error(errorMessage(e)) })}
            >
              <Trash2Icon />
            </Button>
          )}
        </div>
      </div>
      {m.reactions?.length > 0 && (
        <div className="flex flex-wrap gap-1 px-1">
          {groupReactions(m.reactions).map(r => (
            <button
              key={r.emoji}
              type="button"
              disabled={!canReact}
              title={r.senders.join(', ')}
              className="rounded-full border bg-background px-1.5 text-xs disabled:cursor-default"
              onClick={() => react(r.emoji, true)}
            >
              {r.emoji}
              {r.count > 1 ? ` ${r.count}` : ''}
            </button>
          ))}
        </div>
      )}
      <span className="flex items-center gap-1 px-1 text-[11px] text-muted-foreground">
        {formatTime(m.timestamp)}
        {m.edited_at && ' · 已编辑'}
        {m.from_me && (m.status === 'read' ? <CheckCheckIcon className="size-3 text-success" aria-label="已读" /> : m.status === 'delivered' ? <CheckCheckIcon className="size-3" aria-label="已送达" /> : m.status === 'sent' ? <CheckIcon className="size-3" aria-label="已发送" /> : m.status === 'failed' ? <span className="text-destructive">发送失败</span> : null)}
      </span>
    </div>
  )
}

function Body({ accountId, message: m }: { accountId: string, message: Message }) {
  const c = m.content
  switch (c.type) {
    case 'text':
      return <p className="whitespace-pre-wrap break-words">{plainText(c.text ?? '', c.format)}</p>
    case 'deleted':
    case 'expired':
    case 'unsupported':
      return (
        <p className="italic opacity-70">
          {contentPreview(c)}
          {c.unsupported ? ` (${c.unsupported.platform_type})` : ''}
        </p>
      )
    case 'location': {
      const l = c.location
      if (!l)
        return null
      const href = `https://www.openstreetmap.org/?mlat=${l.lat}&mlon=${l.lon}#map=16/${l.lat}/${l.lon}`
      return (
        <a href={href} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 underline underline-offset-4">
          <MapPinIcon className="size-4" aria-hidden />
          {l.name || `${l.lat.toFixed(5)}, ${l.lon.toFixed(5)}`}
        </a>
      )
    }
    case 'contact':
      return (
        <ul className="flex flex-col gap-1">
          {(c.contacts ?? []).map(card => (
            <li key={`${card.name}/${card.phones?.[0] ?? ''}`}>
              <span className="font-medium">{card.name}</span>
              {card.phones?.length
                ? (
                    <span className="opacity-80">{` · ${card.phones.join(', ')}`}</span>
                  )
                : null}
            </li>
          ))}
        </ul>
      )
    case 'poll':
      return (
        <div>
          <p className="font-medium">{c.poll?.question}</p>
          <ul className="mt-1 text-xs">
            {(c.poll?.options ?? []).map(o => (
              <li key={o.text}>
                {o.text}
                {' '}
                ·
                {' '}
                {o.votes}
              </li>
            ))}
          </ul>
        </div>
      )
    case 'call':
      return (
        <p className="flex items-center gap-1.5">
          {c.call?.kind === 'video' ? <VideoIcon className="size-4" aria-hidden /> : <PhoneIcon className="size-4" aria-hidden />}
          {`${c.call?.kind === 'video' ? '视频通话' : '语音通话'} · ${callState(c.call?.state ?? '')}`}
        </p>
      )
    default:
      return (
        <div className="flex flex-col gap-1.5">
          {(c.attachments ?? []).map(att => <AttachmentView key={att.media_id} accountId={accountId} chatId={m.chat_id} type={c.type} attachment={att} />)}
          {c.text && <p className="whitespace-pre-wrap break-words">{plainText(c.text, c.format)}</p>}
        </div>
      )
  }
}
