// Mail accounts and rules, as /api/v1/mail/* returns them.

export type MailSecurity = 'ssl' | 'starttls' | 'none'
export type MailConsume = 'attachments' | 'eml' | 'both'
export type MailTitleFrom = 'subject' | 'filename' | 'none'
export type MailCorrespondentFrom = 'nothing' | 'email' | 'name'
export type MailAction = 'mark_read' | 'move' | 'delete' | 'flag' | 'none'

export interface MailAccount {
  id: number
  name: string
  host: string
  port: number
  security: MailSecurity
  username: string
  /** the password is never returned; this says whether one is stored */
  password_set: boolean
  enabled: boolean
  poll_minutes: number
  last_checked_at: string | null
  last_error: string
  created_at: string
}

/** What POST/PUT /mail/accounts take: the account fields plus a password (empty = keep). */
export type MailAccountInput = Omit<MailAccount, 'id' | 'password_set' | 'last_checked_at' | 'last_error' | 'created_at'> & { password: string }

export interface MailRule {
  id: number
  account_id: number
  name: string
  enabled: boolean
  position: number
  folder: string
  filter_from: string
  filter_to: string
  filter_subject: string
  filter_body: string
  filter_attachment: string
  max_age_days: number
  consume: MailConsume
  title_from: MailTitleFrom
  assign_tags: number[]
  assign_correspondent: number | null
  assign_document_type: number | null
  assign_storage_path: number | null
  correspondent_from: MailCorrespondentFrom
  action: MailAction
  action_folder: string
  only_unseen: boolean
  created_at: string
}

export type MailRuleInput = Omit<MailRule, 'id' | 'created_at'>

export interface MailTestResult { folders: string[] }
export interface MailFetchResult { consumed: number; errors: string[] }
