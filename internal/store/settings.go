package store

import (
	"context"
	"strconv"
	"strings"
)

// DefaultTitle is the app title until someone changes it.
const DefaultTitle = "Pocket Documents"

// Defaults the environment may override at start (set by the server).
var EnvDefaults = Settings{
	Title: DefaultTitle, OCRLanguage: "eng", OCRMode: "skip", OCRDeskew: true, OCRRotate: true, OCROutputType: "pdfa",
	TrashDays: 30, ConsumeSubdirsAsTags: true, BarcodesEnabled: false, BarcodeASNPrefix: "ASN", BarcodeSeparator: "PATCHT", BarcodeMaxPages: 60, DateFromName: true,
	FolderKind: "none", FolderName: "Documents", FolderFile: "archive", FolderInbox: true, FolderInboxName: "Inbox",
	DateFormat: "iso", DefaultView: "table", PageSize: 50, DefaultSort: "-created", TitleFromName: true, DateFromText: true, SuggestOnOpen: true,
	MoneyMode: "core", AIAuto: true,
}

// GetSettings loads the box settings plus the preferences of userID, with
// defaults applied. userID 0 gives the box settings with default preferences.
func (s *Store) GetSettings(ctx context.Context, userID int64) (Settings, error) {
	st := EnvDefaults
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings UNION ALL SELECT key, value FROM user_settings WHERE user_id = ?`, userID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	atoi := func(v string, def int) int {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		return def
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		switch k {
		case "title":
			if v != "" {
				st.Title = v
			}
		case "setup_complete":
			st.SetupComplete = v == "1"
		case "ocr_language":
			if v != "" {
				st.OCRLanguage = v
			}
		case "ocr_mode":
			if v == "skip" || v == "redo" || v == "force" {
				st.OCRMode = v
			}
		case "ocr_deskew":
			st.OCRDeskew = v == "1"
		case "ocr_rotate":
			st.OCRRotate = v == "1"
		case "ocr_clean":
			st.OCRClean = v == "1"
		case "ocr_max_pages":
			st.OCRMaxPages = atoi(v, st.OCRMaxPages)
		case "ocr_output_type":
			if v == "pdf" || v == "pdfa" {
				st.OCROutputType = v
			}
		case "trash_days":
			st.TrashDays = atoi(v, st.TrashDays)
		case "folder_kind":
			if v == "none" || v == "folder" || v == "webdav" || v == "nextcloud" || v == "files" {
				st.FolderKind = v
			}
		case "folder_path":
			st.FolderPath = v
		case "folder_url":
			st.FolderURL = v
		case "folder_user":
			st.FolderUser = v
		case "folder_password":
			st.FolderPassword = v
		case "folder_name":
			st.FolderName = v
		case "folder_via_core":
			st.FolderViaCore = v == "1"
		case "folder_file":
			if v == "original" || v == "archive" {
				st.FolderFile = v
			}
		case "folder_inbox":
			st.FolderInbox = v == "1"
		case "folder_inbox_name":
			if v != "" {
				st.FolderInboxName = v
			}
		case "consume_subdirs_as_tags":
			st.ConsumeSubdirsAsTags = v == "1"
		case "barcodes_enabled":
			st.BarcodesEnabled = v == "1"
		case "barcode_asn_prefix":
			st.BarcodeASNPrefix = v
		case "barcode_separator":
			if v != "" {
				st.BarcodeSeparator = v
			}
		case "barcode_keep_separator":
			st.BarcodeKeepSep = v == "1"
		case "barcode_max_pages":
			st.BarcodeMaxPages = atoi(v, st.BarcodeMaxPages)
		case "date_from_name":
			st.DateFromName = v != "0"
		case "date_format":
			if v == "iso" || v == "dmy-dot" || v == "long" {
				st.DateFormat = v
			}
		case "default_view":
			if v == "table" || v == "cards" || v == "list" {
				st.DefaultView = v
			}
		case "page_size":
			st.PageSize = atoi(v, st.PageSize)
		case "default_sort":
			if v != "" {
				st.DefaultSort = v
			}
		case "title_from_name":
			st.TitleFromName = v != "0"
		case "date_from_text":
			st.DateFromText = v != "0"
		case "suggest_on_open":
			st.SuggestOnOpen = v != "0"
		case "money_mode":
			if v == "core" || v == "direct" || v == "off" {
				st.MoneyMode = v
			}
		case "money_url":
			st.MoneyURL = v
		case "money_token":
			st.MoneyToken = v
		case "money_account":
			n, _ := strconv.ParseInt(v, 10, 64)
			st.MoneyAccount = n
		case "notify_url":
			st.NotifyURL = v
		case "ai_auto":
			st.AIAuto = v != "0"
		case "ai_provider":
			st.AIProvider = v
		case "ai_pocket_model":
			st.AIPocketModel = v
		case "ai_url":
			st.AIURL = v
		case "ai_key":
			st.AIKey = v
		case "ai_model":
			st.AIModel = v
		case "ai_context":
			st.AIContext = v
		}
	}
	return st, rows.Err()
}

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// SaveBoxSettings writes the box-wide part.
func (s *Store) SaveBoxSettings(ctx context.Context, st Settings) error {
	vals := map[string]string{
		"title": st.Title, "setup_complete": b2s(st.SetupComplete),
		"ocr_language": st.OCRLanguage, "ocr_mode": st.OCRMode, "ocr_deskew": b2s(st.OCRDeskew), "ocr_rotate": b2s(st.OCRRotate), "ocr_clean": b2s(st.OCRClean),
		"ocr_max_pages": strconv.Itoa(st.OCRMaxPages), "ocr_output_type": st.OCROutputType, "trash_days": strconv.Itoa(st.TrashDays),
		"consume_subdirs_as_tags": b2s(st.ConsumeSubdirsAsTags),
		"barcodes_enabled":        b2s(st.BarcodesEnabled), "barcode_asn_prefix": st.BarcodeASNPrefix, "barcode_separator": st.BarcodeSeparator, "barcode_keep_separator": b2s(st.BarcodeKeepSep), "barcode_max_pages": strconv.Itoa(st.BarcodeMaxPages),
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range vals {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveSettings writes one person's preferences.
func (s *Store) SaveSettings(ctx context.Context, userID int64, st Settings) error {
	vals := map[string]string{
		"date_format": st.DateFormat, "default_view": st.DefaultView, "page_size": strconv.Itoa(st.PageSize), "default_sort": st.DefaultSort,
		"title_from_name": b2s(st.TitleFromName), "date_from_text": b2s(st.DateFromText), "date_from_name": b2s(st.DateFromName), "suggest_on_open": b2s(st.SuggestOnOpen),
		"money_mode": st.MoneyMode, "money_url": strings.TrimSpace(st.MoneyURL), "money_account": strconv.FormatInt(st.MoneyAccount, 10),
		"notify_url": strings.TrimSpace(st.NotifyURL),
		"ai_auto":    b2s(st.AIAuto), "ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel,
		"ai_url": strings.TrimSpace(st.AIURL), "ai_model": strings.TrimSpace(st.AIModel), "ai_context": strings.TrimSpace(st.AIContext),
	}
	if st.MoneyToken != "" {
		vals["money_token"] = st.MoneyToken
	}
	if st.AIKey != "" {
		vals["ai_key"] = st.AIKey
	}
	for k, v := range map[string]string{"folder_kind": st.FolderKind, "folder_path": st.FolderPath, "folder_url": st.FolderURL, "folder_user": st.FolderUser, "folder_name": st.FolderName,
		"folder_via_core": b2s(st.FolderViaCore), "folder_file": st.FolderFile, "folder_inbox": b2s(st.FolderInbox), "folder_inbox_name": st.FolderInboxName} {
		vals[k] = v
	}
	if st.FolderPassword != "" {
		vals["folder_password"] = st.FolderPassword
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range vals {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?) ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`, userID, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetUserSetting writes one key for one person.
func (s *Store) SetUserSetting(ctx context.Context, userID int64, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?) ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`, userID, key, value)
	return err
}

// SetBoxSetting writes one box-wide key.
func (s *Store) SetBoxSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// UserIDs lists every account id.
func (s *Store) UserIDs(ctx context.Context) []int64 {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM users ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}
