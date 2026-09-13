import type { FormEvent } from 'react'
import type { Account, LoginStep } from '@/shared/api/types'
import { CheckCircle2Icon, CopyIcon, XIcon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useLogin, useLoginStep, usePlatforms } from '@/shared/api/queries'
import { QrCode } from '@/shared/components/qr-code'
import { Alert, AlertDescription, AlertTitle } from '@/shared/components/ui/alert'
import { Button } from '@/shared/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { Field, FieldGroup, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Spinner } from '@/shared/components/ui/spinner'
import { displayContact, formatTime } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { inputProps, safeHttpUrl } from './account-utils'

const fieldLabels: Record<string, string> = { phone: '手机号', password: '密码', code: '验证码', otp: '验证码', token: '访问令牌', user: '用户名' }

/** Only http(s) links are rendered as links; anything else stays text. */

export function LoginPanel({ account }: { account: Account }) {
  const platforms = usePlatforms()
  const [active, setActive] = useState(account.status === 'logging_in')
  const step = useLoginStep(account.id, active)
  const login = useLogin(account.id)
  const flows = platforms.data?.find(p => p.id === account.platform)?.login_flows ?? []
  const current = active ? step.data : undefined
  const error = login.start.error ?? login.submit.error

  return (
    <Card>
      <CardHeader>
        <CardTitle>登录</CardTitle>
        <CardDescription>按平台提示完成登录，状态会实时刷新。</CardDescription>
        {current && current.step !== 'done' && (
          <CardAction>
            <Button variant="ghost" size="sm" disabled={login.cancel.isPending} onClick={() => login.cancel.mutate(undefined, { onSuccess: () => setActive(false) })}>
              <XIcon />
              取消
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {!current && (
          <div className="flex flex-wrap gap-2">
            {flows.map(f => (
              <Button key={f.id} variant="outline" disabled={login.start.isPending} onClick={() => login.start.mutate(f.id, { onSuccess: () => setActive(true) })}>
                {login.start.isPending && login.start.variables === f.id && <Spinner />}
                {f.name}
              </Button>
            ))}
            {flows.length === 0 && <p className="text-sm text-muted-foreground">该平台没有可用的登录方式（适配器未连接？）。</p>}
          </div>
        )}
        {current && <StepView step={current} busy={login.submit.isPending} onSubmit={fields => login.submit.mutate(fields)} onRetry={() => login.start.mutate(current.flow)} />}
        {error && (
          <Alert variant="destructive">
            <AlertTitle>登录请求失败</AlertTitle>
            <AlertDescription>{errorMessage(error)}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  )
}

function StepView({ step, busy, onSubmit, onRetry }: { step: LoginStep, busy: boolean, onSubmit: (f: Record<string, string>) => void, onRetry: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({})
  if (step.step === 'input' && step.input) {
    const submit = (e: FormEvent) => {
      e.preventDefault()
      onSubmit(values)
      setValues({})
    }
    return (
      <form onSubmit={submit} className="flex flex-col gap-4">
        <FieldGroup>
          {step.input.fields.map((f, i) => (
            <Field key={f.name}>
              <FieldLabel htmlFor={`login-${f.name}`}>{f.label || fieldLabels[f.name] || f.name}</FieldLabel>
              <Input
                id={`login-${f.name}`}
                {...inputProps(f)}
                autoFocus={i === 0}
                required
                pattern={f.pattern}
                value={values[f.name] ?? ''}
                onChange={e => setValues(v => ({ ...v, [f.name]: e.target.value }))}
              />
            </Field>
          ))}
        </FieldGroup>
        <Button type="submit" disabled={busy} className="self-start">
          {busy && <Spinner />}
          继续
        </Button>
      </form>
    )
  }
  if (step.step === 'display' && step.display) {
    const d = step.display
    const link = d.type === 'url' ? safeHttpUrl(d.data) : undefined
    return (
      <div className="flex flex-col items-start gap-3">
        {d.type === 'qr' && <QrCode data={d.data} />}
        {d.type === 'code' && (
          <div className="flex items-center gap-2">
            <span className="rounded-md border bg-muted px-3 py-2 font-mono text-2xl tracking-widest">{d.data}</span>
            <Button variant="ghost" size="icon" aria-label="复制" onClick={() => void navigator.clipboard.writeText(d.data).then(() => toast.success('已复制'))}>
              <CopyIcon />
            </Button>
          </div>
        )}
        {d.type === 'url' && (link
          ? <a href={link} target="_blank" rel="noopener noreferrer" className="break-all text-sm underline underline-offset-4">{link}</a>
          : <code className="break-all text-sm">{d.data}</code>)}
        {d.type !== 'qr' && d.type !== 'code' && d.type !== 'url' && <pre className="whitespace-pre-wrap text-sm">{d.data}</pre>}
        <p className="text-sm text-muted-foreground">
          {d.type === 'qr' ? '用手机上的客户端扫描二维码。' : d.type === 'code' ? '在手机客户端输入这个配对码。' : '按提示完成后此处会自动更新。'}
          {d.expires_at ? ` 有效至 ${formatTime(d.expires_at)}。` : ''}
        </p>
      </div>
    )
  }
  if (step.step === 'done') {
    return (
      <Alert>
        <CheckCircle2Icon />
        <AlertTitle>登录成功</AlertTitle>
        <AlertDescription>{displayContact(step.self) || '已连接'}</AlertDescription>
      </Alert>
    )
  }
  return (
    <Alert variant="destructive">
      <AlertTitle>登录失败</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-2">
        {step.error?.message ?? '未知错误'}
        <Button variant="outline" size="sm" onClick={onRetry}>重试</Button>
      </AlertDescription>
    </Alert>
  )
}
