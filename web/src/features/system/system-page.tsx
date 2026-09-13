import type { FormEvent } from 'react'
import { PlusIcon, Trash2Icon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { usePlatforms, useStatus, useWebhookActions, useWebhooks } from '@/shared/api/queries'
import { PageHeader } from '@/shared/components/page-header'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/shared/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Spinner } from '@/shared/components/ui/spinner'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/components/ui/table'
import { formatTime, formatUptime } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'

export function SystemPage() {
  const status = useStatus()
  const platforms = usePlatforms()
  const s = status.data
  return (
    <div className="flex h-full flex-col overflow-auto">
      <PageHeader title="系统" description="服务状态、已注册的平台适配器和 webhook 订阅。" />
      <div className="grid gap-4 p-6">
        <Card>
          <CardHeader>
            <CardTitle>服务</CardTitle>
            <CardDescription>{s ? `版本 ${s.version} · 已运行 ${formatUptime(s.uptime_s)}` : status.error ? errorMessage(status.error) : '加载中…'}</CardDescription>
          </CardHeader>
          {s && (
            <CardContent className="flex flex-wrap gap-6 text-sm">
              <span>
                账号
                {s.accounts.length}
              </span>
              <span>
                已连接
                {s.accounts.filter(a => a.status === 'connected').length}
              </span>
              <span className="text-muted-foreground">
                事件游标
                {s.events_cursor}
              </span>
            </CardContent>
          )}
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>平台</CardTitle>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>平台</TableHead>
                  <TableHead>实例</TableHead>
                  <TableHead>登录方式</TableHead>
                  <TableHead>能力</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(platforms.data ?? []).map(p => (
                  <TableRow key={p.id}>
                    <TableCell className="align-top">
                      <div className="font-medium">{p.id}</div>
                      <div className="text-xs text-muted-foreground">{p.name}</div>
                    </TableCell>
                    <TableCell className="align-top">
                      {p.instances.map(i => (
                        <div key={i.id} className="flex items-center gap-1 text-xs">
                          {i.id}
                          {i.remote && <Badge variant="outline">远程</Badge>}
                        </div>
                      ))}
                    </TableCell>
                    <TableCell className="align-top text-xs">{p.login_flows.map(f => f.id).join(', ')}</TableCell>
                    <TableCell className="whitespace-normal">
                      <div className="flex flex-wrap gap-1">{p.capabilities.map(c => <Badge key={c} variant="secondary">{c}</Badge>)}</div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
        <WebhooksCard />
      </div>
    </div>
  )
}

function WebhooksCard() {
  const hooks = useWebhooks()
  const { create, remove } = useWebhookActions()
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ url: '', secret: '', account: '', types: '' })

  function submit(e: FormEvent) {
    e.preventDefault()
    create.mutate(
      { url: form.url, secret: form.secret, account: form.account || undefined, types: form.types ? form.types.split(',').map(t => t.trim()).filter(Boolean) : undefined },
      {
        onSuccess: () => {
          toast.success('已添加 webhook')
          setOpen(false)
          setForm({ url: '', secret: '', account: '', types: '' })
        },
      },
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Webhooks</CardTitle>
        <CardDescription>事件以 HMAC 签名的 POST 批量推送，失败按退避重试。</CardDescription>
        <CardAction>
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger render={<Button size="sm" variant="outline" />}>
              <PlusIcon />
              添加
            </DialogTrigger>
            <DialogContent className="sm:max-w-md">
              <form onSubmit={submit} className="flex flex-col gap-4">
                <DialogHeader>
                  <DialogTitle>添加 webhook</DialogTitle>
                  <DialogDescription>从当前事件游标开始推送。</DialogDescription>
                </DialogHeader>
                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="wh-url">URL</FieldLabel>
                    <Input id="wh-url" type="url" required value={form.url} onChange={e => setForm(f => ({ ...f, url: e.target.value }))} />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="wh-secret">签名密钥</FieldLabel>
                    <Input id="wh-secret" type="password" autoComplete="new-password" required value={form.secret} onChange={e => setForm(f => ({ ...f, secret: e.target.value }))} />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="wh-account">账号（可选）</FieldLabel>
                    <Input id="wh-account" value={form.account} onChange={e => setForm(f => ({ ...f, account: e.target.value }))} />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="wh-types">事件类型（可选）</FieldLabel>
                    <Input id="wh-types" placeholder="message.new, request.new" value={form.types} onChange={e => setForm(f => ({ ...f, types: e.target.value }))} />
                    <FieldDescription>逗号分隔；留空接收全部。</FieldDescription>
                  </Field>
                  {create.error && <FieldError>{errorMessage(create.error)}</FieldError>}
                </FieldGroup>
                <DialogFooter>
                  <Button type="submit" disabled={create.isPending}>
                    {create.isPending && <Spinner />}
                    添加
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        </CardAction>
      </CardHeader>
      <CardContent>
        {(hooks.data ?? []).length === 0
          ? <p className="text-sm text-muted-foreground">没有订阅</p>
          : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>URL</TableHead>
                    <TableHead>范围</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>创建</TableHead>
                    <TableHead className="w-10"><span className="sr-only">操作</span></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(hooks.data ?? []).map(w => (
                    <TableRow key={w.id}>
                      <TableCell className="max-w-64 truncate">{w.url}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">{[w.account_id, w.types?.join(', ')].filter(Boolean).join(' · ') || '全部'}</TableCell>
                      <TableCell>
                        {w.paused_at
                          ? <Badge variant="destructive">已暂停</Badge>
                          : w.failures > 0
                            ? (
                                <Badge variant="outline">
                                  失败
                                  {w.failures}
                                </Badge>
                              )
                            : <Badge variant="secondary">正常</Badge>}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">{formatTime(w.created_at)}</TableCell>
                      <TableCell>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          aria-label="删除 webhook"
                          disabled={remove.isPending}
                          onClick={() => remove.mutate(w.id, { onSuccess: () => toast.success('已删除'), onError: e => toast.error(errorMessage(e)) })}
                        >
                          <Trash2Icon />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
      </CardContent>
    </Card>
  )
}
