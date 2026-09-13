import type { PersonSuggestion } from '@/shared/api/types'
import { SparklesIcon } from 'lucide-react'
import { toast } from 'sonner'
import { usePersonActions } from '@/shared/api/queries'
import { Button } from '@/shared/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { errorMessage } from '@/shared/lib/http'
import { linkLabel } from './person-utils'

/** Unlinked contacts on different accounts that share a phone, one click to group them. */
export function SuggestionsCard({ suggestions }: { suggestions: PersonSuggestion[] }) {
  const { create } = usePersonActions()
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <SparklesIcon className="size-4" aria-hidden />
          可能是同一个人
        </CardTitle>
        <CardDescription>这些联系人在不同账号上手机号相同，还没有归到任何人。</CardDescription>
      </CardHeader>
      <CardContent>
        <ul className="flex flex-col gap-2">
          {suggestions.map(s => (
            <li key={s.contacts.map(c => `${c.account_id}/${c.user_id}`).join('|')} className="flex flex-wrap items-center gap-2 rounded-md border p-2">
              <span className="text-sm">{s.contacts.map(linkLabel).join('、')}</span>
              {s.contacts[0]?.phone && <span className="text-xs text-muted-foreground">{s.contacts[0].phone}</span>}
              <Button
                size="sm"
                variant="outline"
                className="ml-auto"
                disabled={create.isPending}
                onClick={() => create.mutate(
                  { name: s.contacts[0]?.name ?? '', links: s.contacts.map(c => ({ account_id: c.account_id, user_id: c.user_id })) },
                  { onSuccess: p => toast.success(`已归为「${p.name}」`), onError: e => toast.error(errorMessage(e)) },
                )}
              >
                归为一个人
              </Button>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
