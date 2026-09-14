import type { Contact } from '@/shared/api/types'
import { Link } from '@tanstack/react-router'
import { BanIcon, EllipsisIcon, MessageSquareIcon, PencilIcon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useAccount, useContacts, usePatchContact } from '@/shared/api/queries'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/shared/components/ui/dropdown-menu'
import { Input } from '@/shared/components/ui/input'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/components/ui/table'
import { displayContact } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { AliasDialog } from './alias-dialog'
import { useOpenChat } from './use-open-chat'

export function ContactsPage({ accountId }: { accountId: string }) {
  const [q, setQ] = useState('')
  const [editing, setEditing] = useState<Contact>()
  const account = useAccount(accountId)
  const contacts = useContacts(accountId, q)
  const patch = usePatchContact(accountId)
  const caps = account.data?.capabilities ?? []
  const openChat = useOpenChat(accountId, caps)
  const connected = account.data?.status === 'connected'
  const list = contacts.data?.pages.flatMap(p => p.contacts) ?? []

  return (
    <div className="flex flex-col gap-4 p-6">
      <Input value={q} onChange={e => setQ(e.target.value)} placeholder="按名称、号码或 ID 搜索" aria-label="搜索联系人" className="max-w-sm" />
      {contacts.isLoading && <Skeleton className="h-40" />}
      {contacts.isSuccess && list.length === 0 && <p className="text-sm text-muted-foreground">没有联系人</p>}
      {list.length > 0 && (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>号码 / 句柄</TableHead>
                <TableHead>ID</TableHead>
                <TableHead>标记</TableHead>
                <TableHead className="w-10"><span className="sr-only">操作</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map(c => (
                <TableRow key={c.id}>
                  <TableCell className="font-medium">
                    <Link to="/accounts/$accountId/contacts/$userId" params={{ accountId, userId: c.id }} className="underline-offset-4 hover:underline">
                      {displayContact(c)}
                    </Link>
                    {c.names.alias && c.names.profile && c.names.alias !== c.names.profile && (
                      <span className="ml-1 text-xs text-muted-foreground">
                        （
                        {c.names.profile}
                        ）
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{c.phone || c.handle || '—'}</TableCell>
                  <TableCell className="max-w-48 truncate text-xs text-muted-foreground">{c.id}</TableCell>
                  <TableCell className="flex gap-1">
                    {c.is_self && <Badge variant="secondary">自己</Badge>}
                    {c.is_contact && <Badge variant="outline">通讯录</Badge>}
                    {c.blocked && <Badge variant="destructive">已拉黑</Badge>}
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={`${displayContact(c)} 的操作`} />}>
                        <EllipsisIcon />
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem disabled={!connected} onClick={() => void openChat(c)}>
                          <MessageSquareIcon />
                          发消息
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => setEditing(c)}>
                          <PencilIcon />
                          设置备注
                        </DropdownMenuItem>
                        {!c.is_self && (
                          <DropdownMenuItem
                            variant={c.blocked ? 'default' : 'destructive'}
                            disabled={!connected}
                            onClick={() => patch.mutate({ user: c.id, blocked: !c.blocked }, {
                              onSuccess: () => toast.success(c.blocked ? '已取消拉黑' : '已拉黑'),
                              onError: e => toast.error(errorMessage(e)),
                            })}
                          >
                            <BanIcon />
                            {c.blocked ? '取消拉黑' : '拉黑'}
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {contacts.hasNextPage && <Button variant="ghost" className="self-center" onClick={() => void contacts.fetchNextPage()}>加载更多</Button>}
      {editing && <AliasDialog key={editing.id} contact={editing} onClose={() => setEditing(undefined)} onSave={alias => patch.mutate({ user: editing.id, alias }, { onSuccess: () => setEditing(undefined), onError: e => toast.error(errorMessage(e)) })} />}
    </div>
  )
}
