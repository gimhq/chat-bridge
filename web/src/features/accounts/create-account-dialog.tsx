import type { FormEvent } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { PlusIcon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useCreateAccount, usePlatforms } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/shared/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/components/ui/select'
import { Spinner } from '@/shared/components/ui/spinner'
import { Switch } from '@/shared/components/ui/switch'
import { errorMessage } from '@/shared/lib/http'
import { buildConfig } from './account-utils'

const ID_PATTERN = /^[\w.-]{1,64}$/

/** Turns form strings into the JSON types the platform's config schema declares. */

export function CreateAccountDialog() {
  const [open, setOpen] = useState(false)
  const platforms = usePlatforms()
  const create = useCreateAccount()
  const navigate = useNavigate()
  const [id, setId] = useState('')
  const [platformId, setPlatformId] = useState('')
  const [instance, setInstance] = useState('')
  const [values, setValues] = useState<Record<string, string | boolean>>({})

  const list = platforms.data ?? []
  const platform = list.find(p => p.id === platformId) ?? list[0]
  const props = Object.entries(platform?.config_schema?.properties ?? {})
  const required = new Set(platform?.config_schema?.required ?? [])
  const idError = id !== '' && !ID_PATTERN.test(id) ? '只能包含字母、数字、点、下划线和连字符' : undefined

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!platform || idError)
      return
    create.mutate(
      { id, platform: platform.id, adapter: instance || undefined, config: buildConfig(platform, values) },
      {
        onSuccess: (a) => {
          toast.success(`已创建账号 ${a.id}`)
          setOpen(false)
          setId('')
          setValues({})
          void navigate({ to: '/accounts/$accountId', params: { accountId: a.id } })
        },
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button />}>
        <PlusIcon />
        新建账号
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>新建账号</DialogTitle>
            <DialogDescription>创建后在账号页完成登录。</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={Boolean(idError) || undefined}>
              <FieldLabel htmlFor="account-id">账号 ID</FieldLabel>
              <Input id="account-id" value={id} placeholder="wa-main" required aria-invalid={Boolean(idError) || undefined} onChange={e => setId(e.target.value)} />
              {idError ? <FieldError>{idError}</FieldError> : <FieldDescription>本地唯一标识，之后出现在 API 路径里。</FieldDescription>}
            </Field>
            <Field>
              <FieldLabel htmlFor="account-platform">平台</FieldLabel>
              <Select
                value={platform?.id ?? ''}
                items={list.map(p => ({ value: p.id, label: p.name }))}
                onValueChange={(v) => {
                  setPlatformId(String(v))
                  setInstance('')
                  setValues({})
                }}
              >
                <SelectTrigger id="account-platform" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {list.map(p => <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>)}
                </SelectContent>
              </Select>
            </Field>
            {platform && platform.instances.length > 1 && (
              <Field>
                <FieldLabel htmlFor="account-instance">适配器实例</FieldLabel>
                <Select value={instance || platform.instances[0]!.id} onValueChange={v => setInstance(String(v))}>
                  <SelectTrigger id="account-instance" className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {platform.instances.map(i => (
                      <SelectItem key={i.id} value={i.id}>
                        {i.id}
                        {i.remote ? '（远程）' : ''}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
            {props.map(([key, prop]) => (
              <Field key={key} orientation={prop.type === 'boolean' ? 'horizontal' : 'vertical'}>
                <FieldLabel htmlFor={`cfg-${key}`}>
                  {key}
                  {required.has(key) ? ' *' : ''}
                </FieldLabel>
                {prop.type === 'boolean'
                  ? <Switch id={`cfg-${key}`} checked={Boolean(values[key])} onCheckedChange={c => setValues(v => ({ ...v, [key]: c }))} />
                  : (
                      <Input
                        id={`cfg-${key}`}
                        required={required.has(key)}
                        type={prop['x-secret'] ? 'password' : prop.type === 'integer' || prop.type === 'number' ? 'number' : 'text'}
                        autoComplete="off"
                        placeholder={prop.default !== undefined ? String(prop.default) : undefined}
                        value={String(values[key] ?? '')}
                        onChange={e => setValues(v => ({ ...v, [key]: e.target.value }))}
                      />
                    )}
                {prop.description && <FieldDescription>{prop.description}</FieldDescription>}
              </Field>
            ))}
            {create.error && <FieldError>{errorMessage(create.error)}</FieldError>}
          </FieldGroup>
          <DialogFooter>
            <Button type="submit" disabled={!platform || id === '' || Boolean(idError) || create.isPending}>
              {create.isPending && <Spinner />}
              创建
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
