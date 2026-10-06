import type { FormEvent } from 'react'
import type { Token, TokenScope } from '@/shared/api/types'
import { XIcon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useAccounts, useChats, useContacts, usePersons, useTokenActions } from '@/shared/api/queries'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select'
import { Spinner } from '@/shared/components/ui/spinner'
import { Switch } from '@/shared/components/ui/switch'
import { displayChat, displayContact } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { emptyScope } from './token-utils'

interface Props {
  /** The token to edit; absent creates one. */
  token?: Token
  onClose: () => void
  /** Receives the new token with its secret, which the server shows once. */
  onCreated: (t: Token) => void
}

/** Creates or edits a scoped token: name, read-only switch and the allowlist. */
export function TokenDialog({ token, onClose, onCreated }: Props) {
  const { create, patch } = useTokenActions()
  const [name, setName] = useState(token?.name ?? '')
  const [scope, setScope] = useState<TokenScope>(token?.scope ?? emptyScope)
  const persons = usePersons('')
  const accounts = useAccounts()
  const [account, setAccount] = useState('')
  const accountId = account || accounts.data?.[0]?.id || ''
  const busy = create.isPending || patch.isPending
  const error = create.error ?? patch.error

  function submit(e: FormEvent) {
    e.preventDefault()
    const body = { name: name.trim(), scope }
    if (token) {
      patch.mutate({ id: token.id, ...body }, {
        onSuccess: () => {
          toast.success('已保存')
          onClose()
        },
      })
    }
    else {
      create.mutate(body, {
        onSuccess: (t) => {
          onClose()
          onCreated(t)
        },
      })
    }
  }

  const togglePerson = (id: string) => setScope(s => ({ ...s, persons: s.persons.includes(id) ? s.persons.filter(p => p !== id) : [...s.persons, id] }))
  const hasContact = (a: string, u: string) => scope.contacts.some(c => c.account_id === a && c.user_id === u)
  const hasChat = (a: string, c: string) => scope.chats.some(x => x.account_id === a && x.chat_id === c)

  return (
    <Dialog open onOpenChange={o => !o && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{token ? `编辑令牌「${token.name}」` : '新建受限令牌'}</DialogTitle>
            <DialogDescription>范围是白名单：令牌只能看到列出的人、联系人和会话。群聊必须单独加入“会话”才可见。</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="tok-name">名称</FieldLabel>
              <Input id="tok-name" required placeholder="例如 family-assistant" value={name} onChange={e => setName(e.target.value)} />
            </Field>
            <Field orientation="horizontal">
              <Switch id="tok-ro" checked={scope.read_only} onCheckedChange={v => setScope(s => ({ ...s, read_only: v }))} />
              <div>
                <FieldLabel htmlFor="tok-ro">只读</FieldLabel>
                <FieldDescription>只能读取和搜索；不能发送、编辑、删除、回应、标记已读、上传或发起会话。</FieldDescription>
              </div>
            </Field>

            <Field>
              <FieldLabel>人</FieldLabel>
              <FieldDescription>选中的人名下的所有联系人及其私聊都在范围内，之后新关联的也会自动包含。</FieldDescription>
              {(persons.data ?? []).length === 0
                ? <p className="text-sm text-muted-foreground">还没有“人”</p>
                : (
                    <div className="flex flex-wrap gap-1.5">
                      {(persons.data ?? []).map(p => (
                        <Button
                          key={p.id}
                          type="button"
                          size="xs"
                          variant={scope.persons.includes(p.id) ? 'default' : 'outline'}
                          aria-pressed={scope.persons.includes(p.id)}
                          onClick={() => togglePerson(p.id)}
                        >
                          {p.name || p.id}
                        </Button>
                      ))}
                    </div>
                  )}
            </Field>

            <Field>
              <FieldLabel htmlFor="tok-account">从账号添加联系人和会话</FieldLabel>
              <Select value={accountId} items={(accounts.data ?? []).map(a => ({ value: a.id, label: `${a.id} · ${a.platform}` }))} onValueChange={v => setAccount(String(v ?? ''))}>
                <SelectTrigger id="tok-account" className="w-full"><SelectValue placeholder="没有账号" /></SelectTrigger>
                <SelectContent>
                  {(accounts.data ?? []).map(a => <SelectItem key={a.id} value={a.id}>{`${a.id} · ${a.platform}`}</SelectItem>)}
                </SelectContent>
              </Select>
            </Field>
            {accountId && (
              <>
                <ContactPicker
                  key={`c-${accountId}`}
                  accountId={accountId}
                  has={u => hasContact(accountId, u)}
                  onAdd={u => setScope(s => ({ ...s, contacts: [...s.contacts, { account_id: accountId, user_id: u }] }))}
                />
                <ChatPicker
                  key={`h-${accountId}`}
                  accountId={accountId}
                  has={c => hasChat(accountId, c)}
                  onAdd={c => setScope(s => ({ ...s, chats: [...s.chats, { account_id: accountId, chat_id: c }] }))}
                />
              </>
            )}

            <Field>
              <FieldLabel>已选的联系人和会话</FieldLabel>
              {scope.contacts.length + scope.chats.length === 0
                ? <p className="text-sm text-muted-foreground">无</p>
                : (
                    <div className="flex flex-wrap gap-1.5">
                      {scope.contacts.map(c => (
                        <Chip
                          key={`c:${c.account_id}:${c.user_id}`}
                          label={`联系人 ${c.account_id} · ${c.user_id}`}
                          onRemove={() => setScope(s => ({ ...s, contacts: s.contacts.filter(x => x !== c) }))}
                        />
                      ))}
                      {scope.chats.map(c => (
                        <Chip
                          key={`h:${c.account_id}:${c.chat_id}`}
                          label={`会话 ${c.account_id} · ${c.chat_id}`}
                          onRemove={() => setScope(s => ({ ...s, chats: s.chats.filter(x => x !== c) }))}
                        />
                      ))}
                    </div>
                  )}
            </Field>
            {error && <FieldError>{errorMessage(error)}</FieldError>}
          </FieldGroup>
          <DialogFooter>
            <Button type="submit" disabled={busy || name.trim() === ''}>
              {busy && <Spinner />}
              {token ? '保存' : '创建'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function Chip({ label, onRemove }: { label: string, onRemove: () => void }) {
  return (
    <Badge variant="secondary" className="gap-1 pr-1">
      <span className="max-w-64 truncate">{label}</span>
      <button type="button" aria-label={`移除 ${label}`} className="rounded-sm opacity-60 hover:opacity-100" onClick={onRemove}>
        <XIcon className="size-3" />
      </button>
    </Badge>
  )
}

interface PickerProps {
  accountId: string
  has: (id: string) => boolean
  onAdd: (id: string) => void
}

/** Search field plus a short result list; each row adds one entry to the scope. */
function PickList({ id, label, noun, placeholder, q, onQuery, rows, loading, has, onAdd }: {
  id: string
  label: string
  /** Names the kind of row in button labels: a contact and its direct chat share a title. */
  noun: string
  placeholder: string
  q: string
  onQuery: (q: string) => void
  rows: { id: string, title: string, hint: string }[]
  loading: boolean
  has: (id: string) => boolean
  onAdd: (id: string) => void
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input id={id} type="search" placeholder={placeholder} value={q} onChange={e => onQuery(e.target.value)} />
      <ul className="max-h-36 overflow-y-auto rounded-md border text-sm">
        {rows.length === 0 && <li className="px-2 py-1.5 text-muted-foreground">{loading ? '加载中…' : '没有匹配项'}</li>}
        {rows.map(r => (
          <li key={r.id} className="flex items-center justify-between gap-2 px-2 py-1">
            <span className="min-w-0 truncate">
              {r.title}
              <span className="ml-2 text-xs text-muted-foreground">{r.hint}</span>
            </span>
            <Button type="button" size="xs" variant="outline" disabled={has(r.id)} aria-label={`添加${noun} ${r.title}`} onClick={() => onAdd(r.id)}>
              {has(r.id) ? '已添加' : '添加'}
            </Button>
          </li>
        ))}
      </ul>
    </Field>
  )
}

function ContactPicker({ accountId, has, onAdd }: PickerProps) {
  const [q, setQ] = useState('')
  const contacts = useContacts(accountId, q)
  const rows = (contacts.data?.pages.flatMap(p => p.contacts) ?? []).filter(c => !c.is_self).map(c => ({ id: c.id, title: displayContact(c), hint: c.handle || c.id }))
  return <PickList id="tok-contact-q" label="联系人" noun="联系人" placeholder="搜索名字、手机号或 ID" q={q} onQuery={setQ} rows={rows} loading={contacts.isLoading} has={has} onAdd={onAdd} />
}

function ChatPicker({ accountId, has, onAdd }: PickerProps) {
  const [q, setQ] = useState('')
  const chats = useChats(accountId, false)
  const needle = q.trim().toLowerCase()
  const rows = (chats.data?.pages.flatMap(p => p.chats) ?? [])
    .filter(c => needle === '' || displayChat(c).toLowerCase().includes(needle) || c.id.toLowerCase().includes(needle))
    .map(c => ({ id: c.id, title: displayChat(c), hint: c.kind === 'group' ? '群聊' : c.kind === 'channel' ? '频道' : '私聊' }))
  return <PickList id="tok-chat-q" label="会话（群聊需要在这里加入）" noun="会话" placeholder="按名称筛选" q={q} onQuery={setQ} rows={rows} loading={chats.isLoading} has={has} onAdd={onAdd} />
}
