import type { Contact } from '@/shared/api/types'
import { Link } from '@tanstack/react-router'
import { BanIcon, LinkIcon, MessageSquareIcon, PencilIcon, UserRoundIcon } from 'lucide-react'
import { Fragment, useState } from 'react'
import { toast } from 'sonner'
import { MessageItem } from '@/features/chats/message-item'
import { LinkContactDialog } from '@/features/persons/link-contact-dialog'
import { linkLabel } from '@/features/persons/person-utils'
import { RequestList } from '@/features/requests/requests-page'
import { useAccount, useContact, useContactChats, useContactMessages, useContactRequests, usePatchContact, usePerson } from '@/shared/api/queries'
import { PageHeader } from '@/shared/components/page-header'
import { Alert, AlertDescription } from '@/shared/components/ui/alert'
import { Badge } from '@/shared/components/ui/badge'
import { Button, buttonVariants } from '@/shared/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/shared/components/ui/tabs'
import { displayChat, displayContact, formatTime } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { AliasDialog } from './alias-dialog'
import { useOpenChat } from './use-open-chat'

const kindLabels: Record<string, string> = { direct: '私聊', group: '群组', channel: '频道', self: '自己' }

/** Everything the bridge knows about one contact of one account. */
export function ContactDetail({ accountId, userId }: { accountId: string, userId: string }) {
  const contact = useContact(accountId, userId)
  if (contact.isLoading)
    return <div className="p-6"><Skeleton className="h-48" /></div>
  if (contact.error || !contact.data) {
    return (
      <div className="p-6">
        <Alert variant="destructive">
          <AlertDescription>{contact.error ? errorMessage(contact.error) : '找不到这个联系人'}</AlertDescription>
        </Alert>
      </div>
    )
  }
  const c = contact.data
  return (
    <div className="flex flex-col">
      <PageHeader
        title={displayContact(c)}
        description={(
          <span className="flex flex-wrap items-center gap-1">
            {c.is_self && <Badge variant="secondary">自己</Badge>}
            {c.is_contact && <Badge variant="outline">通讯录</Badge>}
            {c.blocked && <Badge variant="destructive">已拉黑</Badge>}
            <span>{c.phone || c.handle || c.id}</span>
          </span>
        )}
        actions={<ContactActions accountId={accountId} contact={c} />}
      />
      <div className="grid gap-4 p-6 xl:grid-cols-[24rem_1fr]">
        <div className="flex flex-col gap-4">
          <ProfileCard contact={c} />
          <PersonCard accountId={accountId} contact={c} />
          <ChatsCard accountId={accountId} userId={userId} />
          <RequestsCard accountId={accountId} userId={userId} />
        </div>
        <Timeline accountId={accountId} userId={userId} />
      </div>
    </div>
  )
}

function ContactActions({ accountId, contact: c }: { accountId: string, contact: Contact }) {
  const account = useAccount(accountId)
  const connected = account.data?.status === 'connected'
  const openChat = useOpenChat(accountId, account.data?.capabilities ?? [])
  const patch = usePatchContact(accountId)
  const [editing, setEditing] = useState(false)
  return (
    <>
      <Button size="sm" disabled={!connected} onClick={() => void openChat(c)}>
        <MessageSquareIcon />
        发消息
      </Button>
      <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
        <PencilIcon />
        设置备注
      </Button>
      {c.person_id && (
        <Link to="/persons/$personId" params={{ personId: c.person_id }} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
          <UserRoundIcon />
          查看此人
        </Link>
      )}
      {!c.is_self && (
        <Button
          variant={c.blocked ? 'outline' : 'destructive'}
          size="sm"
          disabled={!connected}
          onClick={() => patch.mutate({ user: c.id, blocked: !c.blocked }, {
            onSuccess: () => toast.success(c.blocked ? '已取消拉黑' : '已拉黑'),
            onError: e => toast.error(errorMessage(e)),
          })}
        >
          <BanIcon />
          {c.blocked ? '取消拉黑' : '拉黑'}
        </Button>
      )}
      {editing && (
        <AliasDialog
          contact={c}
          onClose={() => setEditing(false)}
          onSave={alias => patch.mutate({ user: c.id, alias }, { onSuccess: () => setEditing(false), onError: e => toast.error(errorMessage(e)) })}
        />
      )}
    </>
  )
}

function ProfileCard({ contact: c }: { contact: Contact }) {
  const rows: [string, string | undefined][] = [
    ['备注', c.names.alias && `${c.names.alias}${c.names.alias_source === 'local' ? '（本地）' : ''}`],
    ['昵称', c.names.profile],
    ['名', c.names.first],
    ['姓', c.names.last],
    ['用户名', c.names.username],
    ['号码', c.phone],
    ['句柄', c.handle !== c.phone ? c.handle : undefined],
    ['邮箱', c.email],
    ['签名', c.bio],
    ['ID', c.id],
    ['更新于', formatTime(c.updated_at)],
  ]
  return (
    <Card>
      <CardHeader><CardTitle>资料</CardTitle></CardHeader>
      <CardContent>
        <dl className="grid grid-cols-[4rem_1fr] gap-x-3 gap-y-1.5 text-sm">
          {rows.filter(([, v]) => v).map(([k, v]) => (
            <Fragment key={k}>
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="min-w-0 break-all">{v}</dd>
            </Fragment>
          ))}
        </dl>
      </CardContent>
    </Card>
  )
}

function PersonCard({ accountId, contact: c }: { accountId: string, contact: Contact }) {
  const [linking, setLinking] = useState(false)
  return (
    <Card>
      <CardHeader>
        <CardTitle>跨平台</CardTitle>
        <CardDescription>同一个人在其他账号上的身份。</CardDescription>
      </CardHeader>
      <CardContent>
        {c.person_id
          ? <PersonLinks accountId={accountId} userId={c.id} personId={c.person_id} />
          : (
              <div className="flex flex-col items-start gap-2">
                <p className="text-sm text-muted-foreground">还没有归到某个人名下。</p>
                <Button variant="outline" size="sm" onClick={() => setLinking(true)}>
                  <LinkIcon />
                  关联到人
                </Button>
              </div>
            )}
        {linking && <LinkContactDialog accountId={accountId} contact={c} onClose={() => setLinking(false)} />}
      </CardContent>
    </Card>
  )
}

function PersonLinks({ accountId, userId, personId }: { accountId: string, userId: string, personId: string }) {
  const person = usePerson(personId)
  if (person.isLoading)
    return <Skeleton className="h-16" />
  const p = person.data
  if (!p)
    return null
  const others = p.links.filter(l => l.account_id !== accountId || l.user_id !== userId)
  return (
    <div className="flex flex-col gap-2">
      <Link to="/persons/$personId" params={{ personId }} className="font-medium underline-offset-4 hover:underline">
        {p.name || '未命名'}
      </Link>
      {others.length === 0 && <p className="text-sm text-muted-foreground">没有其他平台身份。</p>}
      <ul className="flex flex-col gap-1">
        {others.map(l => (
          <li key={`${l.account_id}/${l.user_id}`}>
            <Link to="/accounts/$accountId/contacts/$userId" params={{ accountId: l.account_id, userId: l.user_id }} className="text-sm underline-offset-4 hover:underline">
              {linkLabel(l)}
            </Link>
          </li>
        ))}
      </ul>
    </div>
  )
}

function ChatsCard({ accountId, userId }: { accountId: string, userId: string }) {
  const chats = useContactChats(accountId, userId)
  const list = chats.data ?? []
  return (
    <Card>
      <CardHeader>
        <CardTitle>会话</CardTitle>
        <CardDescription>私聊和共同所在的群。</CardDescription>
      </CardHeader>
      <CardContent>
        {chats.isLoading && <Skeleton className="h-16" />}
        {chats.isSuccess && list.length === 0 && <p className="text-sm text-muted-foreground">还没有会话。</p>}
        <ul className="flex flex-col gap-1">
          {list.map(ch => (
            <li key={ch.id}>
              <Link
                to="/accounts/$accountId/chats/$chatId"
                params={{ accountId, chatId: ch.id }}
                className="flex items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
              >
                <span className="flex min-w-0 items-center gap-1.5">
                  <Badge variant="secondary">{kindLabels[ch.kind] ?? ch.kind}</Badge>
                  <span className="truncate">{displayChat(ch)}</span>
                </span>
                <span className="shrink-0 text-xs text-muted-foreground">{formatTime(ch.last_message_at)}</span>
              </Link>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}

function RequestsCard({ accountId, userId }: { accountId: string, userId: string }) {
  const requests = useContactRequests(accountId, userId)
  if (!requests.data?.length)
    return null
  return (
    <Card>
      <CardHeader>
        <CardTitle>请求</CardTitle>
        <CardDescription>来电和邀请。</CardDescription>
      </CardHeader>
      <CardContent>
        <RequestList requests={requests.data} loading={false} />
      </CardContent>
    </Card>
  )
}

function Timeline({ accountId, userId }: { accountId: string, userId: string }) {
  const [scope, setScope] = useState<'direct' | 'all'>('direct')
  const messages = useContactMessages(accountId, userId, scope)
  const chats = useContactChats(accountId, userId)
  const chatNames = new Map((chats.data ?? []).map(ch => [ch.id, displayChat(ch)]))
  const list = messages.data?.pages.flatMap(pg => pg.messages) ?? []
  const byKey = new Map(list.map(m => [`${m.chat_id}/${m.id}`, m]))
  return (
    <Card>
      <CardHeader>
        <CardTitle>消息</CardTitle>
        <CardDescription>按时间倒序；含群聊时加上对方在群里发的消息。</CardDescription>
        <CardAction>
          <Tabs value={scope} onValueChange={v => setScope(v === 'all' ? 'all' : 'direct')}>
            <TabsList>
              <TabsTrigger value="direct">私聊</TabsTrigger>
              <TabsTrigger value="all">含群聊</TabsTrigger>
            </TabsList>
          </Tabs>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {messages.isLoading && <Skeleton className="h-24" />}
        {messages.error && (
          <Alert variant="destructive">
            <AlertDescription>{errorMessage(messages.error)}</AlertDescription>
          </Alert>
        )}
        {messages.isSuccess && list.length === 0 && <p className="text-sm text-muted-foreground">没有消息</p>}
        {list.map(m => (
          <div key={`${m.chat_id}/${m.id}`} className="flex flex-col gap-1 border-b pb-3 last:border-b-0">
            <Link
              to="/accounts/$accountId/chats/$chatId"
              params={{ accountId, chatId: m.chat_id }}
              className="text-xs text-muted-foreground underline-offset-4 hover:underline"
            >
              {chatNames.get(m.chat_id) ?? m.chat_id}
            </Link>
            <MessageItem accountId={accountId} message={m} replyTarget={m.reply_to ? byKey.get(`${m.chat_id}/${m.reply_to}`) : undefined} showSender capabilities={[]} connected={false} onReply={() => {}} />
          </div>
        ))}
        {messages.hasNextPage && (
          <Button variant="ghost" size="sm" className="self-center" disabled={messages.isFetchingNextPage} onClick={() => void messages.fetchNextPage()}>
            加载更多
          </Button>
        )}
      </CardContent>
    </Card>
  )
}
