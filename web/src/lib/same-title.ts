// Titles are not unique (design: "Errors-row Retry name", same-title rule).
// A row whose exact title also appears on another row of the list being shown
// adds its video ID, visibly and at the end of every control name in the row.
// Rows with no title already use their ID, so they never count as duplicates.

export function duplicateTitles(rows: ReadonlyArray<{ video_title?: string }>): Set<string> {
  const seen = new Set<string>()
  const dup = new Set<string>()
  for (const { video_title: t } of rows) {
    if (!t) continue
    if (seen.has(t)) dup.add(t)
    seen.add(t)
  }
  return dup
}

// The target T that every row control names: the title; `video {id}` with no
// title; `{title}, video {id}` when the title is shared.
export function targetName(title: string | undefined, dups: Set<string>, id: string | number): string {
  if (!title) return `video ${id}`
  return `${title}${videoIdSuffix(title, dups, id)}`
}

// ", video {id}" for a row whose title is shared, otherwise "".
export function videoIdSuffix(title: string | undefined, dups: Set<string>, id: string | number): string {
  return title && dups.has(title) ? `, video ${id}` : ''
}
