import type { FormEvent } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { UsersRoundIcon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useCreateChat } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/shared/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Spinner } from '@/shared/components/ui/spinner'
import { Textarea } from '@/shared/components/ui/textarea'
import { errorMessage } from '@/shared/lib/http'
import { parseMembers } from './chat-utils'

export function CreateGroupDialog({ accountId }: { accountId: string }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [members, setMembers] = useState('')
  const create = useCreateChat(accountId)
  const navigate = useNavigate()

  function submit(e: FormEvent) {
    e.preventDefault()
    create.mutate({ name, members: parseMembers(members) }, {
      onSuccess: (chat) => {
        toast.success(`已创建群组「${chat.name ?? name}」`)
        setOpen(false)
        setName('')
        setMembers('')
        void navigate({ to: '/accounts/$accountId/chats/$chatId', params: { accountId, chatId: chat.id } })
      },
    })
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant="outline" size="icon-sm" aria-label="新建群组" />}>
        <UsersRoundIcon />
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>新建群组</DialogTitle>
            <DialogDescription>成员填写平台用户 ID（WhatsApp JID、Telegram 用户 ID、Matrix @user:server）。</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="group-name">群名</FieldLabel>
              <Input id="group-name" required value={name} onChange={e => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="group-members">成员</FieldLabel>
              <Textarea id="group-members" rows={3} value={members} onChange={e => setMembers(e.target.value)} />
              <FieldDescription>用空格、逗号或换行分隔；自己不用填。</FieldDescription>
            </Field>
            {create.error && <FieldError>{errorMessage(create.error)}</FieldError>}
          </FieldGroup>
          <DialogFooter>
            <Button type="submit" disabled={name.trim() === '' || create.isPending}>
              {create.isPending && <Spinner />}
              创建
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
