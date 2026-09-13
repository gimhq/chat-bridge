import type { FormEvent } from 'react'
import type { Chat } from '@/shared/api/types'
import { useState } from 'react'
import { toast } from 'sonner'
import { usePatchChat } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog'
import { Field, FieldError, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Spinner } from '@/shared/components/ui/spinner'
import { errorMessage } from '@/shared/lib/http'

export function RenameDialog({ open, onOpenChange, accountId, chat }: { open: boolean, onOpenChange: (o: boolean) => void, accountId: string, chat: Chat }) {
  const [name, setName] = useState(chat.name ?? '')
  const patch = usePatchChat(accountId, chat.id)
  function submit(e: FormEvent) {
    e.preventDefault()
    patch.mutate({ name }, {
      onSuccess: () => {
        toast.success('群名已修改')
        onOpenChange(false)
      },
    })
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>重命名群组</DialogTitle>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="rename">群名</FieldLabel>
            <Input id="rename" autoFocus required value={name} onChange={e => setName(e.target.value)} />
            {patch.error && <FieldError>{errorMessage(patch.error)}</FieldError>}
          </Field>
          <DialogFooter>
            <Button type="submit" disabled={name.trim() === '' || patch.isPending}>
              {patch.isPending && <Spinner />}
              保存
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
