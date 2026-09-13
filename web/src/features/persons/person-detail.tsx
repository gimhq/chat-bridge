import type { Person } from '@/shared/api/types'
import { Link, useNavigate } from '@tanstack/react-router'
import { GitMergeIcon, PencilIcon, Trash2Icon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { MessageItem } from '@/features/chats/message-item'
import { usePerson, usePersonActions, usePersonMessages, usePersons } from '@/shared/api/queries'
import { PageHeader } from '@/shared/components/page-header'
import { Alert, AlertDescription } from '@/shared/components/ui/alert'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from '@/shared/components/ui/alert-dialog'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/shared/components/ui/dialog'
import { Field, FieldLabel } from '@/shared/components/ui/field'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/shared/components/ui/tabs'
import { formatTime } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { LinksCard } from './links-card'
import { PersonDialog } from './person-dialog'

export function PersonDetail({ personId }: { personId: string }) {
  const person = usePerson(personId)
  if (person.isLoading)
    return <div className="p-6"><Skeleton className="h-48" /></div>
  if (person.error || !person.data) {
    return (
      <div className="p-6">
        <Alert variant="destructive">
          <AlertDescription>{person.error ? errorMessage(person.error) : '找不到这个人'}</AlertDescription>
        </Alert>
      </div>
    )
  }
  const p = person.data
  return (
    <div className="flex h-full flex-col overflow-auto">
      <PageHeader
        title={p.name || '未命名'}
        description={(
          <span className="flex flex-wrap items-center gap-1">
            {p.tags.map(t => <Badge key={t} variant="outline">{t}</Badge>)}
            <span>{`${p.links.length} 个平台身份 · 更新于 ${formatTime(p.updated_at)}`}</span>
          </span>
        )}
        actions={(
          <>
            <PersonDialog
              person={p}
              trigger={(
                <Button variant="outline" size="sm">
                  <PencilIcon />
                  编辑
                </Button>
              )}
            />
            <MergeDialog person={p} />
            <DeletePerson person={p} />
          </>
        )}
      />
      <div className="grid gap-4 p-6 xl:grid-cols-[24rem_1fr]">
        <div className="flex flex-col gap-4">
          <LinksCard person={p} />
          <ChannelsCard person={p} />
          {p.notes && (
            <Card>
              <CardHeader><CardTitle>备注</CardTitle></CardHeader>
              <CardContent><p className="whitespace-pre-wrap text-sm">{p.notes}</p></CardContent>
            </Card>
          )}
        </div>
        <Timeline personId={p.id} />
      </div>
    </div>
  )
}

function ChannelsCard({ person }: { person: Person }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>私聊渠道</CardTitle>
        <CardDescription>回复时选一个渠道，消息从对应账号发出。</CardDescription>
      </CardHeader>
      <CardContent>
        {person.channels.length === 0 && <p className="text-sm text-muted-foreground">还没有和这些联系人的私聊。</p>}
        <ul className="flex flex-col gap-1">
          {person.channels.map(ch => (
            <li key={`${ch.account_id}/${ch.chat_id}`}>
              <Link
                to="/accounts/$accountId/chats/$chatId"
                params={{ accountId: ch.account_id, chatId: ch.chat_id }}
                className="flex items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
              >
                <span className="truncate">{`${ch.account_id} · ${ch.name || ch.chat_id}`}</span>
                <span className="shrink-0 text-xs text-muted-foreground">{formatTime(ch.last_message_at)}</span>
              </Link>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}

function Timeline({ personId }: { personId: string }) {
  const [scope, setScope] = useState<'direct' | 'all'>('direct')
  const messages = usePersonMessages(personId, scope)
  const list = messages.data?.pages.flatMap(pg => pg.messages) ?? []
  const byKey = new Map(list.map(m => [`${m.account_id}/${m.chat_id}/${m.id}`, m]))
  return (
    <Card>
      <CardHeader>
        <CardTitle>消息</CardTitle>
        <CardDescription>所有账号合并，按时间倒序。</CardDescription>
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
        {messages.isSuccess && list.length === 0 && <p className="text-sm text-muted-foreground">没有消息</p>}
        {list.map(m => (
          <div key={`${m.account_id}/${m.chat_id}/${m.id}`} className="flex flex-col gap-1 border-b pb-3 last:border-b-0">
            <Link
              to="/accounts/$accountId/chats/$chatId"
              params={{ accountId: m.account_id, chatId: m.chat_id }}
              className="text-xs text-muted-foreground underline-offset-4 hover:underline"
            >
              {`${m.account_id} · ${m.chat_id}`}
            </Link>
            <MessageItem accountId={m.account_id} message={m} replyTarget={m.reply_to ? byKey.get(`${m.account_id}/${m.chat_id}/${m.reply_to}`) : undefined} showSender capabilities={[]} connected={false} onReply={() => {}} />
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

function MergeDialog({ person }: { person: Person }) {
  const [open, setOpen] = useState(false)
  const [other, setOther] = useState('')
  const persons = usePersons('')
  const { merge } = usePersonActions()
  const candidates = (persons.data ?? []).filter(x => x.id !== person.id)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant="outline" size="sm" disabled={candidates.length === 0} />}>
        <GitMergeIcon />
        合并
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{`把另一个人合并到「${person.name || '未命名'}」`}</DialogTitle>
          <DialogDescription>对方的平台身份和标签会移过来，然后删除对方。</DialogDescription>
        </DialogHeader>
        <Field>
          <FieldLabel htmlFor="merge-from">合并谁</FieldLabel>
          <Select value={other} items={candidates.map(x => ({ value: x.id, label: x.name || x.id }))} onValueChange={v => setOther(String(v ?? ''))}>
            <SelectTrigger id="merge-from" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              {candidates.map(x => <SelectItem key={x.id} value={x.id}>{x.name || x.id}</SelectItem>)}
            </SelectContent>
          </Select>
        </Field>
        <DialogFooter>
          <Button
            disabled={!other || merge.isPending}
            onClick={() => merge.mutate({ id: person.id, from: [other] }, {
              onSuccess: () => {
                toast.success('已合并')
                setOpen(false)
                setOther('')
              },
              onError: e => toast.error(errorMessage(e)),
            })}
          >
            合并
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DeletePerson({ person }: { person: Person }) {
  const { remove } = usePersonActions()
  const navigate = useNavigate()
  return (
    <AlertDialog>
      <AlertDialogTrigger render={<Button variant="destructive" size="sm" />}>
        <Trash2Icon />
        删除
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{`删除「${person.name || '未命名'}」？`}</AlertDialogTitle>
          <AlertDialogDescription>只解除关联，联系人和消息都保留。</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={() => remove.mutate(person.id, {
              onSuccess: () => {
                toast.success('已删除')
                void navigate({ to: '/persons' })
              },
              onError: e => toast.error(errorMessage(e)),
            })}
          >
            删除
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
