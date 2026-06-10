import { describe, it, expect } from 'vitest'
import { initialPath, childrenAt, type TreeTitle } from '../app/lib/libraryTree'

const t = (rootLabel: string, relDir: string, displayTitle: string): TreeTitle => ({ rootLabel, relDir, displayTitle })

describe('initialPath', () => {
  it('auto-enters the single root', () => {
    expect(initialPath([t('media', 'Movies', 'A'), t('media', '', 'B')])).toEqual(['media'])
  })
  it('stays at top with multiple roots', () => {
    expect(initialPath([t('media', '', 'A'), t('films', '', 'B')])).toEqual([])
  })
  it('returns [] for empty', () => {
    expect(initialPath([])).toEqual([])
  })
})

describe('childrenAt', () => {
  const titles = [
    t('media', 'Movies/Action', 'Mad Max'),
    t('media', 'Movies/Action', 'Die Hard'),
    t('media', 'Movies', 'Top Gun'),
    t('media', '', 'Home Video'),
  ]
  it('lists root-level folders and titles at [media]', () => {
    const { folders, titles: here } = childrenAt(titles, ['media'])
    expect(folders).toEqual(['Movies'])
    expect(here.map((x) => x.displayTitle)).toEqual(['Home Video'])
  })
  it('shows subfolder once and the title at [media, Movies]', () => {
    const { folders, titles: here } = childrenAt(titles, ['media', 'Movies'])
    expect(folders).toEqual(['Action'])
    expect(here.map((x) => x.displayTitle)).toEqual(['Top Gun'])
  })
  it('descends and sorts titles alpha at [media, Movies, Action]', () => {
    const { folders, titles: here } = childrenAt(titles, ['media', 'Movies', 'Action'])
    expect(folders).toEqual([])
    expect(here.map((x) => x.displayTitle)).toEqual(['Die Hard', 'Mad Max'])
  })
  it('lists distinct roots alpha-sorted at []', () => {
    const multi = [t('media', '', 'A'), t('films', 'X', 'B')]
    expect(childrenAt(multi, []).folders).toEqual(['films', 'media'])
  })
})
