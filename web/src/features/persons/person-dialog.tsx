import type { FormEvent, ReactElement } from 'react'
import type { LinkInput } from '@/shared/api/queries'
import type { Person } from '@/shared/api/types'
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { toast } from 'sonner'
import { usePersonActions } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/shared/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Spinner } from '@/shared/components/ui/spinner'
import { Textarea } from '@/shared/components/ui/textarea'
import { errorMessage } from '@/shared/lib/http'
import { parseTags } from './person-utils'

interface Props {
  trigger: ReactElement
  person?: Person
  /** Contacts to link when creating. */
  links?: LinkInput[]
  defaultName?: string
}

/** Creates a person (then opens it) or edits name, tags and notes of an existing one. */
export function PersonDialog({ trigger, person, links, defaultName }: Props) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(person?.name ?? defaultName ?? '')
  const [tags, setTags] = useState(person?.tags.join(', ') ?? '')
  const [notes, setNotes] = useState(person?.notes ?? '')
  const { create, patch } = usePersonActions()
  const navigate = useNavigate()
  const busy = create.isPending || patch.isPending
  const error = create.error ?? patch.error

  function submit(e: FormEvent) {
    e.preventDefault()
    const body = { name: name.trim(), tags: parseTags(tags), notes }
    if (person) {
      patch.mutate({ id: person.id, ...body }, {
        onSuccess: () => {
          toast.success('已保存')
          setOpen(false)
        },
      })
      return
    }
    create.mutate({ ...body, links }, {
      onSuccess: (p) => {
        toast.success(`已创建「${p.name || '未命名'}」`)
        setOpen(false)
        void navigate({ to: '/persons/$personId', params: { personId: p.id } })
      },
    })
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={trigger} />
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{person ? '编辑' : '新建一个人'}</DialogTitle>
            <DialogDescription>名字、标签和备注只保存在本地。</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="person-name">名字</FieldLabel>
              <Input id="person-name" autoFocus value={name} onChange={e => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="person-tags">标签</FieldLabel>
              <Input id="person-tags" value={tags} placeholder="工作, 家人" onChange={e => setTags(e.target.value)} />
              <FieldDescription>用逗号分隔。</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="person-notes">备注</FieldLabel>
              <Textarea id="person-notes" rows={3} value={notes} onChange={e => setNotes(e.target.value)} />
            </Field>
            {error && <FieldError>{errorMessage(error)}</FieldError>}
          </FieldGroup>
          <DialogFooter>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner />}
              {person ? '保存' : '创建'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
