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

// expandShortcodes replaces every :shortcode: whose name is in EMOJI_SHORTCODES
// with the corresponding emoji. Unknown or incomplete shortcodes are left as-is.
export function expandShortcodes(text: string): string {
  return text.replace(SHORTCODE_RE, (whole, name: string) => EMOJI_SHORTCODES[name.toLowerCase()] ?? whole)
}
