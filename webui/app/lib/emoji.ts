// Curated :shortcode: -> emoji map for danmaku quick-input, Slack/Discord style.
// Small and dependency-free by design; add entries here as needed (or swap for a
// full emoji-shortcode dataset later). Keys are lowercase; lookups are case-folded.
export const EMOJI_SHORTCODES: Record<string, string> = {
  // faces
  smile: '😄', grin: '😁', joy: '😂', rofl: '🤣', sweat_smile: '😅',
  wink: '😉', blush: '😊', heart_eyes: '😍', kiss: '😘', yum: '😋',
  smirk: '😏', thinking: '🤔', neutral: '😐', unamused: '😒', roll_eyes: '🙄',
  cry: '😢', sob: '😭', rage: '😡', triumph: '😤', sunglasses: '😎',
  scream: '😱', flushed: '😳', sleeping: '😴', dizzy_face: '😵', nerd: '🤓',
  party: '🥳', cold_sweat: '😰', skull: '💀', clown: '🤡', ghost: '👻',
  // gestures / people
  thumbsup: '👍', '+1': '👍', thumbsdown: '👎', '-1': '👎', ok_hand: '👌',
  clap: '👏', pray: '🙏', muscle: '💪', wave: '👋', raised_hands: '🙌',
  point_up: '☝️', v: '✌️', crossed_fingers: '🤞', handshake: '🤝',
  // symbols / objects
  heart: '❤️', broken_heart: '💔', sparkling_heart: '💖', fire: '🔥',
  tada: '🎉', sparkles: '✨', star: '⭐', star2: '🌟', zap: '⚡',
  boom: '💥', '100': '💯', eyes: '👀', poop: '💩', rocket: '🚀',
  trophy: '🏆', gift: '🎁', musical_note: '🎵', warning: '⚠️', check: '✅',
  x: '❌', question: '❓', bulb: '💡', moneybag: '💰', crown: '👑',
}

const SHORTCODE_RE = /:([a-z0-9_+-]+):/gi
const SHORTCODE_CHAR = /[a-z0-9_+-]/i

// expandShortcodes replaces every :shortcode: whose name is in EMOJI_SHORTCODES
// with the corresponding emoji. Unknown or incomplete shortcodes are left as-is.
export function expandShortcodes(text: string): string {
  return text.replace(SHORTCODE_RE, (whole, name: string) => EMOJI_SHORTCODES[name.toLowerCase()] ?? whole)
}

// matchShortcodes returns up to `limit` emoji whose shortcode name starts with
// `query` (case-insensitive), ranked alphabetically. Empty query -> no matches.
export function matchShortcodes(query: string, limit = 8): Array<{ name: string; emoji: string }> {
  const q = query.toLowerCase()
  if (q === '') return []
  return Object.keys(EMOJI_SHORTCODES)
    .filter(name => name.startsWith(q))
    .sort()
    .slice(0, limit)
    .map(name => ({ name, emoji: EMOJI_SHORTCODES[name]! }))
}

// activeShortcodeToken finds the in-progress :shortcode being typed at `caret`: a
// run of shortcode characters immediately preceding the caret, opened by a ':'.
// Returns its query (the chars after ':') and the ':' index, or null if none.
export function activeShortcodeToken(text: string, caret: number): { query: string; start: number } | null {
  let i = caret - 1
  while (i >= 0 && SHORTCODE_CHAR.test(text[i]!)) i--
  if (i >= 0 && text[i] === ':') return { query: text.slice(i + 1, caret), start: i }
  return null
}
