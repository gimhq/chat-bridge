import type { Contact } from '@/shared/api/types'
import { useState } from 'react'
import { Button } from '@/shared/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/shared/components/ui/dialog'
import { Field, FieldDescription, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'

export function AliasDialog({ contact, onClose, onSave }: { contact: Contact, onClose: () => void, onSave: (alias: string) => void }) {
  const [alias, setAlias] = useState(contact.names.alias ?? '')
  return (
    <Dialog open onOpenChange={o => !o && onClose()}>
      <DialogContent className="sm:max-w-sm">
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            onSave(alias)
          }}
        >
          <DialogHeader>
            <DialogTitle>设置备注</DialogTitle>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="alias">备注名</FieldLabel>
            <Input id="alias" autoFocus value={alias} onChange={e => setAlias(e.target.value)} />
            <FieldDescription>保存在本地；留空则清除。</FieldDescription>
          </Field>
          <DialogFooter>
            <Button type="submit">保存</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
