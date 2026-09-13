import type { Contact } from '@/shared/api/types'
import { useState } from 'react'
import { toast } from 'sonner'
import { usePersonActions, usePersons } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select'
import { Spinner } from '@/shared/components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '@/shared/components/ui/tabs'
import { displayContact } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'

/** Links one contact to an existing person or to a new one named after the contact. */
export function LinkContactDialog({ accountId, contact, onClose }: { accountId: string, contact: Contact, onClose: () => void }) {
  const persons = usePersons('')
  const list = persons.data ?? []
  const [mode, setMode] = useState<'existing' | 'new'>(list.length > 0 ? 'existing' : 'new')
  const [personId, setPersonId] = useState('')
  const [name, setName] = useState(() => displayContact(contact))
  const { create, link } = usePersonActions()
  const busy = create.isPending || link.isPending
  const error = create.error ?? link.error
  const ref = { account_id: accountId, user_id: contact.id }
  const done = () => {
    toast.success('已关联')
    onClose()
  }

  return (
    <Dialog open onOpenChange={o => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{`关联「${displayContact(contact)}」`}</DialogTitle>
          <DialogDescription>把这个联系人归到一个人名下，方便跨平台查看。</DialogDescription>
        </DialogHeader>
        <Tabs value={mode} onValueChange={v => setMode(v === 'new' ? 'new' : 'existing')}>
          <TabsList className="w-full">
            <TabsTrigger value="existing" disabled={list.length === 0}>已有的人</TabsTrigger>
            <TabsTrigger value="new">新建一个人</TabsTrigger>
          </TabsList>
        </Tabs>
        {mode === 'existing'
          ? (
              <Field>
                <FieldLabel htmlFor="link-person">人</FieldLabel>
                <Select value={personId} items={list.map(p => ({ value: p.id, label: p.name || p.id }))} onValueChange={v => setPersonId(String(v ?? ''))}>
                  <SelectTrigger id="link-person" className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {list.map(p => <SelectItem key={p.id} value={p.id}>{p.name || p.id}</SelectItem>)}
                  </SelectContent>
                </Select>
              </Field>
            )
          : (
              <Field>
                <FieldLabel htmlFor="link-new-name">名字</FieldLabel>
                <Input id="link-new-name" value={name} onChange={e => setName(e.target.value)} />
                <FieldDescription>之后可以在“人”页面修改。</FieldDescription>
              </Field>
            )}
        {error && <FieldError>{errorMessage(error)}</FieldError>}
        <DialogFooter>
          <Button
            disabled={busy || (mode === 'existing' && !personId)}
            onClick={() => mode === 'existing'
              ? link.mutate({ id: personId, ...ref }, { onSuccess: done })
              : create.mutate({ name: name.trim(), links: [ref] }, { onSuccess: done })}
          >
            {busy && <Spinner />}
            关联
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
