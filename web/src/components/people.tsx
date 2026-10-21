import type { User } from '../lib/types'
import { colorFor } from '../lib/format'
import { Avatar as Base } from './ui'
export function Avatar({ user, size = 26 }: { user: User; size?: number }) {
  return <span title={user.name || user.email} style={{ display: 'inline-flex' }}><Base name={user.name} email={user.email} size={size} color={colorFor(user.id)} /></span>
}
