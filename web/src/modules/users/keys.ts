// The checks the users forms run as you type; the agent runs the same ones
// (internal/modules/users) and has the last word.

export const NAME_RULE = "Use lowercase letters, digits, - and _, starting with a letter or _"
export const NOT_A_KEY = "Not a public key line"
export const RSA_TOO_SHORT = "RSA keys need at least 3072 bits"

const NAME = /^[a-z_][a-z0-9_-]{0,31}$/
const MIN_RSA_BITS = 3072
const KEY_TYPES = new Set([
  "ssh-ed25519",
  "ecdsa-sha2-nistp256",
  "ecdsa-sha2-nistp384",
  "ecdsa-sha2-nistp521",
  "sk-ssh-ed25519@openssh.com",
  "sk-ecdsa-sha2-nistp256@openssh.com",
  "ssh-rsa",
])

// Account is one row of users.user.list.
export interface Account {
  name: string
  uid: number
  home: string
  shell: string
  groups: string[]
  sudo: boolean
  locked: boolean
  passwordSet: boolean
  keys: number
}

// Key is one row of users.key.list.
export interface Key {
  type: string
  bits: number
  fingerprint: string
  comment: string
  options: string
}

export function nameError(name: string): string | null {
  return NAME.test(name) ? null : NAME_RULE
}

// passwordError checks a password and its confirmation; an empty password
// is no password.
export function passwordError(password: string, confirm: string): { password: string | null; confirm: string | null } {
  const length = [...password].length
  let first: string | null = null
  if (password !== "" && (length < 8 || length > 256)) first = "Use 8 to 256 characters"
  // eslint-disable-next-line no-control-regex -- a control character would end chpasswd's line
  else if (/[\u0000-\u001f\u007f]/.test(password)) first = "Use no control characters"
  return { password: first, confirm: confirm === password ? null : "The passwords do not match" }
}

// keyError is why line is not a key the agent authorizes, or null. Like the
// agent it takes one key with no options; RSA keys need 3072 bits.
export function keyError(line: string): string | null {
  const text = line.trim()
  // eslint-disable-next-line no-control-regex -- a key line holds no control character
  if (text === "" || /[\u0000-\u001f\u007f]/.test(text)) return NOT_A_KEY
  const [type = "", data = ""] = text.split(/ +/)
  if (!KEY_TYPES.has(type) || !text.startsWith(`${type} `)) return NOT_A_KEY
  let blob: Uint8Array
  try {
    blob = Uint8Array.from(atob(data), (c) => c.charCodeAt(0))
  } catch {
    return NOT_A_KEY
  }
  const fields = sshStrings(blob)
  if (fields === null || fields.length === 0 || new TextDecoder().decode(fields[0]) !== type) return NOT_A_KEY
  if (type === "ssh-rsa") {
    const modulus = fields[2]
    if (fields.length !== 3 || modulus === undefined) return NOT_A_KEY
    if (bitLength(modulus) < MIN_RSA_BITS) return RSA_TOO_SHORT
  }
  return null
}

// sshStrings splits an SSH wire blob into its length-prefixed fields, or
// null when a length runs past the end.
function sshStrings(blob: Uint8Array): Uint8Array[] | null {
  const out: Uint8Array[] = []
  let at = 0
  while (at < blob.length) {
    if (at + 4 > blob.length) return null
    const n = new DataView(blob.buffer, blob.byteOffset + at, 4).getUint32(0)
    at += 4
    if (at + n > blob.length) return null
    out.push(blob.subarray(at, at + n))
    at += n
  }
  return out
}

function bitLength(mpint: Uint8Array): number {
  let i = 0
  while (i < mpint.length && mpint[i] === 0) i++
  const top = mpint[i]
  if (top === undefined) return 0
  return (mpint.length - i - 1) * 8 + (32 - Math.clz32(top))
}

// keyType is a key's type and size as the sheet shows them, "ED25519 256".
export function keyType(k: Key): string {
  const name = k.type
    .replace(/@openssh\.com$/, "")
    .replace(/^ssh-/, "")
    .replace(/^sk-ssh-/, "sk-")
    .replace(/ecdsa-sha2-nistp\d+$/, "ecdsa")
    .toUpperCase()
  return k.bits > 0 ? `${name} ${k.bits}` : name
}
