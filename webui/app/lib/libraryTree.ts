// A Title's place in the Folder tree, derived from the two raw wire facts. The
// tree is [rootLabel, ...relDir segments]; presentation lives entirely here.
export interface TreeTitle {
  rootLabel: string
  relDir: string
  displayTitle: string
}

// treeDir is the Folder path a Title lives in: its root, then each relDir segment.
function treeDir(t: TreeTitle): string[] {
  return [t.rootLabel, ...(t.relDir ? t.relDir.split('/') : [])]
}

// initialPath auto-enters the single Shared folder (one distinct rootLabel),
// else starts at the top listing each root as a Folder.
export function initialPath(titles: TreeTitle[]): string[] {
  const roots = new Set<string>()
  for (const t of titles) roots.add(t.rootLabel)
  return roots.size === 1 ? [[...roots][0]!] : []
}

// childrenAt returns the immediate child Folders (alpha, de-duplicated) and the
// Titles that sit exactly at `path` (alpha by displayTitle).
export function childrenAt<T extends TreeTitle>(titles: T[], path: string[]): { folders: string[]; titles: T[] } {
  const folders = new Set<string>()
  const here: T[] = []
  for (const t of titles) {
    const dir = treeDir(t)
    if (!startsWith(dir, path)) continue
    if (dir.length === path.length) here.push(t)
    else folders.add(dir[path.length]!)
  }
  return {
    folders: [...folders].sort((a, b) => a.localeCompare(b)),
    titles: here.sort((a, b) => a.displayTitle.localeCompare(b.displayTitle)),
  }
}

// startsWith reports whether dir has path as a prefix (dir at or below path).
function startsWith(dir: string[], path: string[]): boolean {
  if (dir.length < path.length) return false
  for (let i = 0; i < path.length; i++) if (dir[i] !== path[i]) return false
  return true
}
