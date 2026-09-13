import { useSyncExternalStore } from 'react'

const KEY = 'chat-bridge.token'
const listeners = new Set<() => void>()

function read(): string {
  try {
    return window.localStorage.getItem(KEY) ?? ''
  }
  catch {
    return ''
  }
}

let current = typeof window === 'undefined' ? '' : read()

function notify() {
  for (const l of listeners)
    l()
}

/** The bearer token for /v1, kept in localStorage so a reload stays signed in. */
export function getToken(): string {
  return current
}

export function setToken(token: string) {
  current = token.trim()
  try {
    window.localStorage.setItem(KEY, current)
  }
  catch {}
  notify()
}

export function clearToken() {
  if (!current)
    return
  current = ''
  try {
    window.localStorage.removeItem(KEY)
  }
  catch {}
  notify()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function useToken(): string {
  return useSyncExternalStore(subscribe, getToken, () => '')
}
