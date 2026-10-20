package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// MailAccount is an IMAP mailbox polled for documents. The password is
// stored but never written out; PasswordSet says whether there is one.
type MailAccount struct {
	ID            int64   `json:"id"`
	UserID        int64   `json:"-"`
	Name          string  `json:"name"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	Security      string  `json:"security"` // ssl | starttls | none
	Username      string  `json:"username"`
	Password      string  `json:"-"`
	PasswordSet   bool    `json:"password_set"`
	Enabled       bool    `json:"enabled"`
	PollMinutes   int     `json:"poll_minutes"`
	LastCheckedAt *string `json:"last_checked_at"`
	LastError     string  `json:"last_error"`
	CreatedAt     string  `json:"created_at"`
}

// MailRule says which messages of an account become documents and how.
type MailRule struct {
	ID                  int64   `json:"id"`
	UserID              int64   `json:"-"`
	AccountID           int64   `json:"account_id"`
	Name                string  `json:"name"`
	Enabled             bool    `json:"enabled"`
	Position            int     `json:"position"`
	Folder              string  `json:"folder"`
	FilterFrom          string  `json:"filter_from"`
	FilterTo            string  `json:"filter_to"`
	FilterSubject       string  `json:"filter_subject"`
	FilterBody          string  `json:"filter_body"`
	FilterAttachment    string  `json:"filter_attachment"`
	MaxAgeDays          int     `json:"max_age_days"`
	Consume             string  `json:"consume"`    // attachments | eml | both
	TitleFrom           string  `json:"title_from"` // subject | filename | none
	AssignTags          []int64 `json:"assign_tags"`
	AssignCorrespondent *int64  `json:"assign_correspondent"`
	AssignDocumentType  *int64  `json:"assign_document_type"`
	AssignStoragePath   *int64  `json:"assign_storage_path"`
	CorrespondentFrom   string  `json:"correspondent_from"` // nothing | email | name
	Action              string  `json:"action"`             // mark_read | move | delete | flag | none
	ActionFolder        string  `json:"action_folder"`
	OnlyUnseen          bool    `json:"only_unseen"`
	CreatedAt           string  `json:"created_at"`
}

const mailAccountCols = `id, user_id, name, host, port, security, username, password, enabled, poll_minutes, last_checked_at, last_error, created_at`

func scanMailAccount(row interface{ Scan(...any) error }) (*MailAccount, error) {
	var a MailAccount
	var en int
	var last sql.NullString
	if err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.Host, &a.Port, &a.Security, &a.Username, &a.Password, &en, &a.PollMinutes, &last, &a.LastError, &a.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	a.Enabled = en == 1
	a.PasswordSet = a.Password != ""
	if last.Valid && last.String != "" {
		v := last.String
		a.LastCheckedAt = &v
	}
	return &a, nil
}

func (a *MailAccount) check() error {
	a.Name = strings.TrimSpace(a.Name)
	a.Host = strings.TrimSpace(a.Host)
	a.Username = strings.TrimSpace(a.Username)
	if a.Name == "" {
		return errors.New("a mail account needs a name")
	}
	if a.Host == "" {
		return errors.New("a mail account needs a server")
	}
	switch a.Security {
	case "ssl", "starttls", "none":
	case "":
		a.Security = "ssl"
	default:
		return errors.New("security must be ssl, starttls or none")
	}
	if a.Port <= 0 || a.Port > 65535 {
		if a.Security == "ssl" {
			a.Port = 993
		} else {
			a.Port = 143
		}
	}
	if a.PollMinutes <= 0 {
		a.PollMinutes = 10
	}
	if a.PollMinutes > 1440 {
		a.PollMinutes = 1440
	}
	return nil
}

// ListMailAccounts lists a person's mail accounts.
func (s *Store) ListMailAccounts(ctx context.Context, userID int64) ([]MailAccount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+mailAccountCols+` FROM mail_accounts WHERE user_id = ? ORDER BY name COLLATE NOCASE, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MailAccount{}
	for rows.Next() {
		a, err := scanMailAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetMailAccount returns one account of a person.
func (s *Store) GetMailAccount(ctx context.Context, userID, id int64) (*MailAccount, error) {
	return scanMailAccount(s.DB.QueryRowContext(ctx, `SELECT `+mailAccountCols+` FROM mail_accounts WHERE id = ? AND user_id = ?`, id, userID))
}

// CreateMailAccount inserts an account.
func (s *Store) CreateMailAccount(ctx context.Context, a *MailAccount) error {
	if err := a.check(); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO mail_accounts (user_id, name, host, port, security, username, password, enabled, poll_minutes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.UserID, a.Name, a.Host, a.Port, a.Security, a.Username, a.Password, boolInt(a.Enabled), a.PollMinutes)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	a.CreatedAt = Now()
	a.PasswordSet = a.Password != ""
	return nil
}

// UpdateMailAccount saves an account. An empty Password keeps the stored one.
func (s *Store) UpdateMailAccount(ctx context.Context, a *MailAccount) error {
	if err := a.check(); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE mail_accounts SET name = ?, host = ?, port = ?, security = ?, username = ?, password = CASE WHEN ? = '' THEN password ELSE ? END, enabled = ?, poll_minutes = ? WHERE id = ? AND user_id = ?`,
		a.Name, a.Host, a.Port, a.Security, a.Username, a.Password, a.Password, boolInt(a.Enabled), a.PollMinutes, a.ID, a.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	fresh, err := s.GetMailAccount(ctx, a.UserID, a.ID)
	if err != nil {
		return err
	}
	*a = *fresh
	return nil
}

// DeleteMailAccount removes an account, its rules and its seen set.
func (s *Store) DeleteMailAccount(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM mail_accounts WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DueMailAccounts returns every enabled account (of everyone) whose last
// check is older than its poll interval, or that was never checked.
func (s *Store) DueMailAccounts(ctx context.Context, now time.Time) ([]MailAccount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+mailAccountCols+` FROM mail_accounts WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailAccount
	for rows.Next() {
		a, err := scanMailAccount(rows)
		if err != nil {
			return nil, err
		}
		if a.LastCheckedAt != nil {
			if t, err := time.Parse(time.RFC3339Nano, *a.LastCheckedAt); err == nil && now.Sub(t) < time.Duration(a.PollMinutes)*time.Minute {
				continue
			}
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// MailAccountChecked records a check: when, and what went wrong ("" = nothing).
func (s *Store) MailAccountChecked(ctx context.Context, id int64, errMsg string) error {
	if len(errMsg) > 1000 {
		errMsg = errMsg[:1000]
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE mail_accounts SET last_checked_at = ?, last_error = ? WHERE id = ?`, Now(), errMsg, id)
	return err
}

// ---- rules ----

const mailRuleCols = `id, user_id, account_id, name, enabled, position, folder, filter_from, filter_to, filter_subject, filter_body, filter_attachment, max_age_days, consume, title_from, assign_tags, assign_correspondent, assign_document_type, assign_storage_path, correspondent_from, action, action_folder, only_unseen, created_at`

func scanMailRule(row interface{ Scan(...any) error }) (*MailRule, error) {
	var r MailRule
	var en, unseen int
	var tags string
	var corr, typ, sp sql.NullInt64
	if err := row.Scan(&r.ID, &r.UserID, &r.AccountID, &r.Name, &en, &r.Position, &r.Folder, &r.FilterFrom, &r.FilterTo, &r.FilterSubject, &r.FilterBody, &r.FilterAttachment, &r.MaxAgeDays, &r.Consume, &r.TitleFrom, &tags, &corr, &typ, &sp, &r.CorrespondentFrom, &r.Action, &r.ActionFolder, &unseen, &r.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	r.Enabled, r.OnlyUnseen = en == 1, unseen == 1
	_ = json.Unmarshal([]byte(tags), &r.AssignTags)
	if r.AssignTags == nil {
		r.AssignTags = []int64{}
	}
	if corr.Valid {
		v := corr.Int64
		r.AssignCorrespondent = &v
	}
	if typ.Valid {
		v := typ.Int64
		r.AssignDocumentType = &v
	}
	if sp.Valid {
		v := sp.Int64
		r.AssignStoragePath = &v
	}
	return &r, nil
}

func oneOf(v string, def string, allowed ...string) (string, bool) {
	if v == "" {
		return def, true
	}
	for _, a := range allowed {
		if v == a {
			return v, true
		}
	}
	return v, false
}

func (r *MailRule) check() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errors.New("a mail rule needs a name")
	}
	if r.AccountID <= 0 {
		return errors.New("a mail rule needs an account")
	}
	r.Folder = strings.TrimSpace(r.Folder)
	if r.Folder == "" {
		r.Folder = "INBOX"
	}
	var ok bool
	if r.Consume, ok = oneOf(r.Consume, "attachments", "attachments", "eml", "both"); !ok {
		return errors.New("consume must be attachments, eml or both")
	}
	if r.TitleFrom, ok = oneOf(r.TitleFrom, "subject", "subject", "filename", "none"); !ok {
		return errors.New("title_from must be subject, filename or none")
	}
	if r.CorrespondentFrom, ok = oneOf(r.CorrespondentFrom, "nothing", "nothing", "email", "name"); !ok {
		return errors.New("correspondent_from must be nothing, email or name")
	}
	if r.Action, ok = oneOf(r.Action, "mark_read", "mark_read", "move", "delete", "flag", "none"); !ok {
		return errors.New("action must be mark_read, move, delete, flag or none")
	}
	r.ActionFolder = strings.TrimSpace(r.ActionFolder)
	if r.Action == "move" && r.ActionFolder == "" {
		return errors.New("moving needs a folder to move to")
	}
	if r.MaxAgeDays < 0 {
		r.MaxAgeDays = 0
	}
	if r.AssignTags == nil {
		r.AssignTags = []int64{}
	}
	for _, p := range []**int64{&r.AssignCorrespondent, &r.AssignDocumentType, &r.AssignStoragePath} {
		if *p != nil && **p <= 0 {
			*p = nil
		}
	}
	return nil
}

func nullID(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// ListMailRules lists a person's rules in order.
func (s *Store) ListMailRules(ctx context.Context, userID int64) ([]MailRule, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+mailRuleCols+` FROM mail_rules WHERE user_id = ? ORDER BY position, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MailRule{}
	for rows.Next() {
		r, err := scanMailRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// AccountMailRules lists the enabled rules of one account in order.
func (s *Store) AccountMailRules(ctx context.Context, accountID int64) ([]MailRule, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+mailRuleCols+` FROM mail_rules WHERE account_id = ? AND enabled = 1 ORDER BY position, id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MailRule{}
	for rows.Next() {
		r, err := scanMailRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetMailRule returns one rule of a person.
func (s *Store) GetMailRule(ctx context.Context, userID, id int64) (*MailRule, error) {
	return scanMailRule(s.DB.QueryRowContext(ctx, `SELECT `+mailRuleCols+` FROM mail_rules WHERE id = ? AND user_id = ?`, id, userID))
}

// CreateMailRule inserts a rule. The account must belong to the same person.
func (s *Store) CreateMailRule(ctx context.Context, r *MailRule) error {
	if err := r.check(); err != nil {
		return err
	}
	if _, err := s.GetMailAccount(ctx, r.UserID, r.AccountID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return errors.New("unknown mail account")
		}
		return err
	}
	tags, _ := json.Marshal(r.AssignTags)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO mail_rules (user_id, account_id, name, enabled, position, folder, filter_from, filter_to, filter_subject, filter_body, filter_attachment, max_age_days, consume, title_from, assign_tags, assign_correspondent, assign_document_type, assign_storage_path, correspondent_from, action, action_folder, only_unseen) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.UserID, r.AccountID, r.Name, boolInt(r.Enabled), r.Position, r.Folder, r.FilterFrom, r.FilterTo, r.FilterSubject, r.FilterBody, r.FilterAttachment, r.MaxAgeDays, r.Consume, r.TitleFrom, string(tags), nullID(r.AssignCorrespondent), nullID(r.AssignDocumentType), nullID(r.AssignStoragePath), r.CorrespondentFrom, r.Action, r.ActionFolder, boolInt(r.OnlyUnseen))
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	r.CreatedAt = Now()
	return nil
}

// UpdateMailRule saves a rule.
func (s *Store) UpdateMailRule(ctx context.Context, r *MailRule) error {
	if err := r.check(); err != nil {
		return err
	}
	if _, err := s.GetMailAccount(ctx, r.UserID, r.AccountID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return errors.New("unknown mail account")
		}
		return err
	}
	tags, _ := json.Marshal(r.AssignTags)
	res, err := s.DB.ExecContext(ctx, `UPDATE mail_rules SET account_id = ?, name = ?, enabled = ?, position = ?, folder = ?, filter_from = ?, filter_to = ?, filter_subject = ?, filter_body = ?, filter_attachment = ?, max_age_days = ?, consume = ?, title_from = ?, assign_tags = ?, assign_correspondent = ?, assign_document_type = ?, assign_storage_path = ?, correspondent_from = ?, action = ?, action_folder = ?, only_unseen = ? WHERE id = ? AND user_id = ?`,
		r.AccountID, r.Name, boolInt(r.Enabled), r.Position, r.Folder, r.FilterFrom, r.FilterTo, r.FilterSubject, r.FilterBody, r.FilterAttachment, r.MaxAgeDays, r.Consume, r.TitleFrom, string(tags), nullID(r.AssignCorrespondent), nullID(r.AssignDocumentType), nullID(r.AssignStoragePath), r.CorrespondentFrom, r.Action, r.ActionFolder, boolInt(r.OnlyUnseen), r.ID, r.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMailRule removes a rule and what it has seen.
func (s *Store) DeleteMailRule(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM mail_rules WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- seen ----

// MailSeenUIDs returns the UIDs a rule has already consumed in a folder
// generation (UIDVALIDITY).
func (s *Store) MailSeenUIDs(ctx context.Context, accountID, ruleID int64, uidValidity uint32) (map[uint32]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT uid FROM mail_seen WHERE account_id = ? AND rule_id = ? AND uid_validity = ?`, accountID, ruleID, int64(uidValidity))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint32]bool{}
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out[uint32(uid)] = true
	}
	return out, rows.Err()
}

// MailSeen reports whether a rule has consumed a message.
func (s *Store) MailSeen(ctx context.Context, accountID, ruleID int64, uidValidity, uid uint32) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM mail_seen WHERE account_id = ? AND rule_id = ? AND uid_validity = ? AND uid = ?`, accountID, ruleID, int64(uidValidity), int64(uid)).Scan(&n)
	return n > 0
}

// MarkMailSeen records that a rule consumed a message.
func (s *Store) MarkMailSeen(ctx context.Context, accountID, ruleID int64, uidValidity, uid uint32, messageID string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO mail_seen (account_id, rule_id, uid_validity, uid, message_id) VALUES (?, ?, ?, ?, ?)`, accountID, ruleID, int64(uidValidity), int64(uid), messageID)
	return err
}
