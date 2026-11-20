import { useCallback, useEffect, useRef, useState } from 'react'
import type { SourceFailure, SourceFailurePage } from '../types'

export type SourceFailureLoad = 'loading' | 'ready' | 'failed'

export interface SourceFailureList {
  items: SourceFailure[]
  load: SourceFailureLoad
  // A read failed while an earlier list is still shown: that list may be out of date.
  stale: boolean
  hasMore: boolean
  loadingMore: boolean
  // Set by each successful "load more": how many entries it added and the first of them.
  added: { count: number; firstId: number } | null
  reload: () => void
  loadMore: () => void
}

// Reads a source failure list newest first, in pages by cursor. A failed read
// is never an empty list: it keeps what was loaded and says so (design: "Channel
// scan history and coverage block"). refreshKey re-reads the first page without
// discarding the list on screen.
export function useSourceFailures(
  fetchPage: (beforeId?: number) => Promise<SourceFailurePage>,
  refreshKey: string,
): SourceFailureList {
  const [items, setItems] = useState<SourceFailure[]>([])
  const [next, setNext] = useState<number | null>(null)
  const [load, setLoad] = useState<SourceFailureLoad>('loading')
  const [loadingMore, setLoadingMore] = useState(false)
  const [added, setAdded] = useState<SourceFailureList['added']>(null)
  const fetchRef = useRef(fetchPage)
  const generation = useRef(0)
  useEffect(() => {
    fetchRef.current = fetchPage
  })

  const readFirst = useCallback(() => {
    const mine = ++generation.current
    fetchRef.current().then(
      (page) => {
        if (mine !== generation.current) return
        setItems(page.items)
        setNext(page.next_before_id)
        setLoad('ready')
      },
      () => {
        if (mine !== generation.current) return
        setLoad('failed')
      },
    )
  }, [])

  useEffect(() => {
    const gen = generation
    readFirst()
    return () => {
      gen.current++
    }
  }, [readFirst, refreshKey])

  const loadMore = useCallback(() => {
    if (next === null) return
    const mine = ++generation.current
    setLoadingMore(true)
    fetchRef.current(next).then(
      (page) => {
        if (mine !== generation.current) return
        setItems((prev) => [...prev, ...page.items])
        setNext(page.next_before_id)
        setLoad('ready')
        setLoadingMore(false)
        if (page.items.length > 0) setAdded({ count: page.items.length, firstId: page.items[0].id })
      },
      () => {
        if (mine !== generation.current) return
        setLoad('failed')
        setLoadingMore(false)
      },
    )
  }, [next])

  const reload = useCallback(() => {
    setLoad('loading')
    readFirst()
  }, [readFirst])

  return {
    items,
    load,
    stale: load === 'failed' && items.length > 0,
    hasMore: next !== null,
    loadingMore,
    added,
    reload,
    loadMore,
  }
}
