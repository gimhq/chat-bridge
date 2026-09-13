import type { ApiEvent } from '@/shared/api/types'
import { useSyncExternalStore } from 'react'

const MAX = 500

let events: ApiEvent[] = []
let connected = false
const listeners = new Set<() => void>()

function notify() {
  for (const l of listeners)
    l()
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

/** Records a live event for the Events page (newest first, bounded). */
export function recordEvent(e: ApiEvent) {
  events = [e, ...events].slice(0, MAX)
  notify()
}

export function setStreamConnected(value: boolean) {
  if (connected === value)
    return
  connected = value
  notify()
}

export function clearEventLog() {
  events = []
  notify()
}

export function useEventLog(): ApiEvent[] {
  return useSyncExternalStore(subscribe, () => events, () => events)
}

export function useStreamConnected(): boolean {
  return useSyncExternalStore(subscribe, () => connected, () => false)
}
