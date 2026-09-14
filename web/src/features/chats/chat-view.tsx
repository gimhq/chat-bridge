import type { Message } from '@/shared/api/types'
import { Link } from '@tanstack/react-router'
import { BellIcon, BellOffIcon, CheckCheckIcon, EllipsisVerticalIcon, PencilIcon, SearchIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { useAccount, useChat, useMarkRead, useMessages, usePatchChat } from '@/shared/api/queries'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/shared/components/ui/dropdown-menu'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { displayChat } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { Composer } from './composer'
import { MessageItem } from './message-item'
import { RenameDialog } from './rename-dialog'
import { SearchDialog } from './search-dialog'

const kindLabels: Record<string, string> = { direct: '私聊', group: '群组', channel: '频道', self: '自己' }

export function ChatView({ accountId, chatId }: { accountId: string, chatId: string }) {
  const account = useAccount(accountId)
  const chat = useChat(accountId, chatId)
  const caps = account.data?.capabilities ?? []
  const messages = useMessages(accountId, chatId, caps.includes('message.history'))
  const patch = usePatchChat(accountId, chatId)
  const markRead = useMarkRead(accountId, chatId)
  const [replyTo, setReplyTo] = useState<Message>()
  const [renaming, setRenaming] = useState(false)

  const list = messages.data?.pages.flatMap(p => p.messages) ?? []
  const byId = new Map(list.map(m => [m.id, m]))
  const c = chat.data
  const unread = c?.unread_count ?? 0
  const canRead = caps.includes('chat.read')
  const connected = account.data?.status === 'connected'

  const { mutate: markReadNow } = markRead
  useEffect(() => {
    if (unread > 0 && canRead && connected)
      markReadNow()
  }, [unread, canRead, connected, markReadNow])

  const toggle = (body: { muted?: boolean, archived?: boolean }) => patch.mutate(body, { onError: e => toast.error(errorMessage(e)) })

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-3 border-b px-4 py-2.5">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h2 className="truncate font-medium">{c ? displayChat(c) : chatId}</h2>
            {c && <Badge variant="secondary">{kindLabels[c.kind] ?? c.kind}</Badge>}
            {c?.muted && <BellOffIcon className="size-3.5 text-muted-foreground" aria-label="已静音" />}
            {c?.person_id && (
              <Link to="/persons/$personId" params={{ personId: c.person_id }} className="text-xs text-muted-foreground underline-offset-4 hover:underline">
                查看此人
              </Link>
            )}
            {c?.kind === 'direct' && (
              <Link to="/accounts/$accountId/contacts/$userId" params={{ accountId, userId: chatId }} className="text-xs text-muted-foreground underline-offset-4 hover:underline">
                查看联系人
              </Link>
            )}
          </div>
          <p className="truncate text-xs text-muted-foreground">
            {chatId}
            {c?.participants?.length ? ` · ${c.participants.length} 位成员` : ''}
          </p>
        </div>
        <SearchDialog accountId={accountId} chatId={chatId} trigger={<Button variant="ghost" size="icon-sm" aria-label="在会话中搜索"><SearchIcon /></Button>} />
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label="会话操作" />}>
            <EllipsisVerticalIcon />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => toggle({ muted: !c?.muted })}>
              {c?.muted ? <BellIcon /> : <BellOffIcon />}
              {c?.muted ? '取消静音' : '静音'}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => toggle({ archived: !c?.archived })}>{c?.archived ? '取消归档' : '归档'}</DropdownMenuItem>
            {canRead && (
              <DropdownMenuItem disabled={!connected} onClick={() => markRead.mutate()}>
                <CheckCheckIcon />
                标记已读
              </DropdownMenuItem>
            )}
            {c?.kind === 'group' && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem disabled={!connected} onClick={() => setRenaming(true)}>
                  <PencilIcon />
                  重命名群组
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
        {c && <RenameDialog open={renaming} onOpenChange={setRenaming} accountId={accountId} chat={c} />}
      </div>
      <div className="flex min-h-0 flex-1 flex-col-reverse gap-1 overflow-y-auto px-4 py-3" role="log" aria-live="polite" aria-label="消息">
        {list.map(m => (
          <MessageItem
            key={m.id}
            accountId={accountId}
            message={m}
            replyTarget={m.reply_to ? byId.get(m.reply_to) : undefined}
            showSender={c?.kind !== 'direct'}
            capabilities={caps}
            connected={connected}
            onReply={setReplyTo}
          />
        ))}
        {messages.isLoading && <div className="flex flex-col gap-2">{[0, 1, 2].map(i => <Skeleton key={i} className="h-10 w-2/3" />)}</div>}
        {messages.hasNextPage && (
          <Button variant="ghost" size="sm" className="self-center" disabled={messages.isFetchingNextPage} onClick={() => void messages.fetchNextPage()}>
            {messages.isFetchingNextPage ? '加载中…' : '加载更早的消息'}
          </Button>
        )}
        {messages.isSuccess && list.length === 0 && <p className="py-10 text-center text-sm text-muted-foreground">暂无消息</p>}
      </div>
      <Composer accountId={accountId} chatId={chatId} disabled={!connected} replyTo={replyTo} onClearReply={() => setReplyTo(undefined)} />
    </div>
  )
}
