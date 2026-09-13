import { Link } from '@tanstack/react-router'
import { ContactRoundIcon, PlusIcon, XIcon } from 'lucide-react'
import { useState } from 'react'
import { usePersons, usePersonSuggestions } from '@/shared/api/queries'
import { PageHeader } from '@/shared/components/page-header'
import { Alert, AlertDescription } from '@/shared/components/ui/alert'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/shared/components/ui/empty'
import { Input } from '@/shared/components/ui/input'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { formatTime } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { PersonDialog } from './person-dialog'
import { linkLabel } from './person-utils'
import { SuggestionsCard } from './suggestions-card'

export function PersonsPage() {
  const [q, setQ] = useState('')
  const [tag, setTag] = useState<string>()
  const persons = usePersons(q, tag)
  const suggestions = usePersonSuggestions()
  const list = persons.data ?? []
  return (
    <div className="flex h-full flex-col overflow-auto">
      <PageHeader
        title="人"
        description="把同一个人在不同平台上的联系人归到一起。只保存在本地，回复仍走原来的账号和会话。"
        actions={(
          <PersonDialog trigger={(
            <Button>
              <PlusIcon />
              新建
            </Button>
          )}
          />
        )}
      />
      <div className="flex flex-col gap-4 p-6">
        {suggestions.data && suggestions.data.length > 0 && <SuggestionsCard suggestions={suggestions.data} />}
        <div className="flex flex-wrap items-center gap-2">
          <Input value={q} onChange={e => setQ(e.target.value)} placeholder="按名字、备注、联系人名称或号码搜索" aria-label="搜索人" className="max-w-sm" />
          {tag && (
            <Badge variant="secondary" className="gap-1">
              标签：
              {tag}
              <button type="button" aria-label="清除标签筛选" onClick={() => setTag(undefined)}><XIcon className="size-3" /></button>
            </Badge>
          )}
        </div>
        {persons.error && (
          <Alert variant="destructive">
            <AlertDescription>{errorMessage(persons.error)}</AlertDescription>
          </Alert>
        )}
        {persons.isLoading && <Skeleton className="h-32" />}
        {persons.isSuccess && list.length === 0 && (
          <Empty className="border">
            <EmptyHeader>
              <EmptyMedia variant="icon"><ContactRoundIcon /></EmptyMedia>
              <EmptyTitle>{q || tag ? '没有匹配的人' : '还没有人'}</EmptyTitle>
              <EmptyDescription>手机号相同的联系人会自动归到一起，也可以在联系人页手动关联。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
        <ul className="grid gap-3 lg:grid-cols-2">
          {list.map(p => (
            <li key={p.id} className="flex flex-col gap-2 rounded-lg border p-4">
              <div className="flex items-start justify-between gap-2">
                <Link to="/persons/$personId" params={{ personId: p.id }} className="font-medium underline-offset-4 hover:underline">
                  {p.name || '未命名'}
                </Link>
                <span className="shrink-0 text-xs text-muted-foreground">{formatTime(p.updated_at)}</span>
              </div>
              {p.tags.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {p.tags.map(t => (
                    <button key={t} type="button" onClick={() => setTag(t)}>
                      <Badge variant="outline">{t}</Badge>
                    </button>
                  ))}
                </div>
              )}
              <div className="flex flex-wrap gap-1">
                {p.links.map(l => <Badge key={`${l.account_id}/${l.user_id}`} variant="secondary">{linkLabel(l)}</Badge>)}
                {p.links.length === 0 && <span className="text-xs text-muted-foreground">没有关联联系人</span>}
              </div>
              {p.notes && <p className="line-clamp-2 text-sm text-muted-foreground">{p.notes}</p>}
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
