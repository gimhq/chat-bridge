import type { Person } from '@/shared/api/types'
import { UnlinkIcon } from 'lucide-react'
import { toast } from 'sonner'
import { usePersonActions } from '@/shared/api/queries'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { errorMessage } from '@/shared/lib/http'
import { sourceLabels } from './person-utils'

export function LinksCard({ person }: { person: Person }) {
  const { unlink } = usePersonActions()
  return (
    <Card>
      <CardHeader>
        <CardTitle>平台身份</CardTitle>
        <CardDescription>在联系人页可以把更多联系人关联进来。</CardDescription>
      </CardHeader>
      <CardContent>
        {person.links.length === 0 && <p className="text-sm text-muted-foreground">还没有关联任何联系人。</p>}
        <ul className="flex flex-col divide-y">
          {person.links.map(l => (
            <li key={`${l.account_id}/${l.user_id}`} className="flex items-center gap-2 py-2">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                  <Badge variant="secondary">{l.platform || l.account_id}</Badge>
                  <span className="truncate text-sm font-medium">{l.name || l.user_id}</span>
                </div>
                <p className="truncate text-xs text-muted-foreground">
                  {[l.account_id, l.phone || l.handle, sourceLabels[l.source]].filter(Boolean).join(' · ')}
                </p>
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`解除关联 ${l.name || l.user_id}`}
                disabled={unlink.isPending}
                onClick={() => unlink.mutate({ id: person.id, account_id: l.account_id, user_id: l.user_id }, {
                  onSuccess: () => toast.success('已解除关联'),
                  onError: e => toast.error(errorMessage(e)),
                })}
              >
                <UnlinkIcon />
              </Button>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
