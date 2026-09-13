import type { FormEvent } from 'react'
import type { Account } from '@/shared/api/types'
import { useRef, useState } from 'react'
import { toast } from 'sonner'
import { useUpdateSelf, useUpload } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Spinner } from '@/shared/components/ui/spinner'
import { Textarea } from '@/shared/components/ui/textarea'
import { errorMessage } from '@/shared/lib/http'

export function SelfEditor({ account: a }: { account: Account }) {
  const canEdit = a.capabilities.includes('self.update')
  const update = useUpdateSelf(a.id)
  const upload = useUpload(a.id)
  const [name, setName] = useState(a.self?.names.profile ?? '')
  const [bio, setBio] = useState(a.self?.bio ?? '')
  const fileRef = useRef<HTMLInputElement>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    try {
      const picked = fileRef.current?.files?.[0]
      const avatar = picked ? await upload.mutateAsync(picked) : undefined
      await update.mutateAsync({
        name: name !== (a.self?.names.profile ?? '') ? name : undefined,
        bio: bio !== (a.self?.bio ?? '') ? bio : undefined,
        avatar_media_id: avatar?.media_id,
      })
      if (fileRef.current)
        fileRef.current.value = ''
      toast.success('资料已更新')
    }
    catch (err) {
      toast.error(errorMessage(err))
    }
  }

  const busy = update.isPending || upload.isPending
  return (
    <Card>
      <CardHeader>
        <CardTitle>个人资料</CardTitle>
        <CardDescription>{canEdit ? '修改会同步到平台。' : '该平台不支持从这里修改资料。'}</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="self-name">显示名</FieldLabel>
              <Input id="self-name" value={name} disabled={!canEdit} onChange={e => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="self-bio">简介</FieldLabel>
              <Textarea id="self-bio" value={bio} disabled={!canEdit} rows={2} onChange={e => setBio(e.target.value)} />
              {a.platform === 'matrix' && <FieldDescription>Matrix 没有简介字段。</FieldDescription>}
            </Field>
            <Field>
              <FieldLabel htmlFor="self-avatar">头像</FieldLabel>
              <Input id="self-avatar" ref={fileRef} type="file" accept="image/*" disabled={!canEdit} />
            </Field>
          </FieldGroup>
          <Button type="submit" disabled={!canEdit || busy} className="self-start">
            {busy && <Spinner />}
            保存
          </Button>
        </form>
      </CardContent>
    </Card>
  )
}
