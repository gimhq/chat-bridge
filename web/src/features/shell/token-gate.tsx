import type { FormEvent } from 'react'
import { useState } from 'react'
import { Button } from '@/shared/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { Field, FieldDescription, FieldError, FieldLabel } from '@/shared/components/ui/field'
import { Input } from '@/shared/components/ui/input'
import { Spinner } from '@/shared/components/ui/spinner'
import { buildUrl, errorMessage } from '@/shared/lib/http'
import { setToken } from '@/shared/lib/token'

/** Asks for server.token and checks it against /v1/status before storing it. */
export function TokenGate() {
  const [value, setValue] = useState('')
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      const res = await fetch(buildUrl('/status'), { headers: { Authorization: `Bearer ${value.trim()}` } })
      if (res.status === 401)
        setError('令牌无效')
      else if (!res.ok)
        setError(`服务器返回 ${res.status}`)
      else
        setToken(value)
    }
    catch (err) {
      setError(errorMessage(err))
    }
    finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-svh items-center justify-center bg-muted/40 p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>chat-bridge 管理</CardTitle>
          <CardDescription>输入服务端配置的访问令牌（server.token）。</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="flex flex-col gap-4">
            <Field data-invalid={Boolean(error) || undefined}>
              <FieldLabel htmlFor="token">访问令牌</FieldLabel>
              <Input
                id="token"
                type="password"
                autoComplete="current-password"
                autoFocus
                value={value}
                aria-invalid={Boolean(error) || undefined}
                onChange={e => setValue(e.target.value)}
              />
              {error ? <FieldError>{error}</FieldError> : <FieldDescription>令牌只保存在当前浏览器。</FieldDescription>}
            </Field>
            <Button type="submit" disabled={busy || value.trim() === ''}>
              {busy && <Spinner />}
              进入
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
