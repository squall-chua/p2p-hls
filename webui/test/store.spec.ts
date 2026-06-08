import { describe, it, expect } from 'vitest'
import { refetchFor } from '../app/lib/store'

describe('refetchFor', () => {
  it('maps event types to the snapshots to refetch', () => {
    expect(refetchFor('presence')).toContain('presence')
    expect(refetchFor('request')).toContain('requests')
    expect(refetchFor('audience')).toContain('audience')
    expect(refetchFor('party-ended')).toContain('audience')
    expect(refetchFor('unknown')).toEqual([])
  })
})
