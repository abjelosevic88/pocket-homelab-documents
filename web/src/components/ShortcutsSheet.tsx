import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['g', 'd'], 'Dashboard'], [['g', 'l'], 'Documents'], [['g', 'i'], 'Inbox'], [['g', 'm'], 'Manage'], [['g', 'j'], 'Processing'], [['g', 't'], 'Trash'], [['g', 's'], 'Settings'], [[','], 'Settings']] },
  { title: 'Search & add', keys: [[['⌘', 'K'], 'Search & commands'], [['/'], 'Search & commands'], [['u'], 'Upload']] },
  { title: 'A document', keys: [[['←'], 'Newer document'], [['→'], 'Older document'], [['⌘', 'S'], 'Save'], [['esc'], 'Back to the list']] },
  { title: 'Lists', keys: [[['⇧', 'click'], 'Select a range']] },
  { title: 'Window', keys: [[['['], 'Show or hide the sidebar'], [['esc'], 'Close a dialog or menu']] },
  { title: 'Help', keys: [[['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
      <p className="muted small mt">Drop files anywhere in the app to upload them. “g” then a letter jumps to a page.</p>
    </Modal>
  )
}
