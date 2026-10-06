import type { Token } from '@/shared/api/types'
import { CopyIcon, PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useTokenActions, useTokens } from '@/shared/api/queries'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@/shared/components/ui/alert-dialog'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog'
import { Input } from '@/shared/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/components/ui/table'
import { formatTime } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { TokenDialog } from './token-dialog'
import { scopeSummary } from './token-utils'

/** Scoped API tokens: one per consumer, each limited to the persons, contacts and chats it lists. */
export function TokensCard() {
  const tokens = useTokens()
  const { remove } = useTokenActions()
  const [editing, setEditing] = useState<Token | 'new' | null>(null)
  const [created, setCreated] = useState<Token | null>(null)
  const [revoking, setRevoking] = useState<Token | null>(null)
  const list = tokens.data ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle>访问令牌</CardTitle>
        <CardDescription>给每个接入方单独发一个受限令牌：只能读取、搜索和发送范围内的联系人与会话，不能管理账号。</CardDescription>
        <CardAction>
          <Button size="sm" variant="outline" onClick={() => setEditing('new')}>
            <PlusIcon />
            新建
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {tokens.error
          ? <p className="text-sm text-destructive">{errorMessage(tokens.error)}</p>
          : list.length === 0
            ? <p className="text-sm text-muted-foreground">还没有受限令牌</p>
            : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>名称</TableHead>
                      <TableHead>范围</TableHead>
                      <TableHead>创建</TableHead>
                      <TableHead>最近使用</TableHead>
                      <TableHead className="w-20"><span className="sr-only">操作</span></TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {list.map(t => (
                      <TableRow key={t.id}>
                        <TableCell>
                          <div className="flex items-center gap-2 font-medium">
                            {t.name}
                            {t.scope.read_only && <Badge variant="outline">只读</Badge>}
                          </div>
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">{scopeSummary(t.scope)}</TableCell>
                        <TableCell className="text-xs text-muted-foreground">{formatTime(t.created_at)}</TableCell>
                        <TableCell className="text-xs text-muted-foreground">{t.last_used_at ? formatTime(t.last_used_at) : '从未'}</TableCell>
                        <TableCell>
                          <div className="flex justify-end gap-1">
                            <Button variant="ghost" size="icon-sm" aria-label={`编辑 ${t.name}`} onClick={() => setEditing(t)}>
                              <PencilIcon />
                            </Button>
                            <Button variant="ghost" size="icon-sm" aria-label={`吊销 ${t.name}`} onClick={() => setRevoking(t)}>
                              <Trash2Icon />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
      </CardContent>

      {editing && (
        <TokenDialog
          token={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
          onCreated={setCreated}
        />
      )}

      {created && (
        <Dialog open onOpenChange={o => !o && setCreated(null)}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>{`令牌「${created.name}」已创建`}</DialogTitle>
              <DialogDescription>现在就复制保存：密钥只显示这一次，服务端不保存原文，丢失后只能重新创建。</DialogDescription>
            </DialogHeader>
            <div className="flex gap-2">
              <Input readOnly aria-label="令牌密钥" className="font-mono text-xs" value={created.token ?? ''} onFocus={e => e.target.select()} />
              <Button
                variant="outline"
                size="icon"
                aria-label="复制"
                onClick={() => void navigator.clipboard.writeText(created.token ?? '').then(() => toast.success('已复制'), () => toast.error('复制失败，请手动选择复制'))}
              >
                <CopyIcon />
              </Button>
            </div>
            <DialogFooter>
              <Button onClick={() => setCreated(null)}>我已保存</Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      {revoking && (
        <AlertDialog open onOpenChange={o => !o && setRevoking(null)}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{`吊销令牌「${revoking.name}」？`}</AlertDialogTitle>
              <AlertDialogDescription>使用它的请求和事件流会立刻失效，无法恢复。</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>取消</AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                onClick={() => remove.mutate(revoking.id, {
                  onSuccess: () => toast.success('已吊销'),
                  onError: e => toast.error(errorMessage(e)),
                  onSettled: () => setRevoking(null),
                })}
              >
                吊销
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </Card>
  )
}
