package store

import "encoding/json"

// User is an account that can sign in. The first user is the admin; admins
// manage other users. Every user has their own documents and taxonomy.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Matching algorithms, numbered as Paperless numbers them.
const (
	MatchNone    = 0
	MatchAny     = 1
	MatchAll     = 2
	MatchLiteral = 3
	MatchRegex   = 4
	MatchFuzzy   = 5
	MatchAuto    = 6
)

// Matchable is what tags, correspondents, types and paths share.
type Matchable struct {
	Match             string `json:"match"`
	MatchingAlgorithm int    `json:"matching_algorithm"`
	IsInsensitive     bool   `json:"is_insensitive"`
}

// Tag labels documents; an inbox tag marks what still needs looking at.
type Tag struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"-"`
	Name   string `json:"name"`
	Color  string `json:"color"`
	Matchable
	IsInbox       bool   `json:"is_inbox"`
	DocumentCount int    `json:"document_count"`
	CreatedAt     string `json:"created_at"`
}

// Correspondent is who a document is from or to.
type Correspondent struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"-"`
	Name   string `json:"name"`
	Matchable
	DocumentCount int    `json:"document_count"`
	LastCreated   string `json:"last_created,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// DocumentType is what kind of paper it is (invoice, contract, letter…).
type DocumentType struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"-"`
	Name   string `json:"name"`
	Matchable
	DocumentCount int    `json:"document_count"`
	CreatedAt     string `json:"created_at"`
}

// StoragePath is a folder layout for the mirror folder and exports.
type StoragePath struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"-"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Matchable
	DocumentCount int    `json:"document_count"`
	CreatedAt     string `json:"created_at"`
}

// CustomField is a typed field documents may carry.
type CustomField struct {
	ID            int64           `json:"id"`
	UserID        int64           `json:"-"`
	Name          string          `json:"name"`
	DataType      string          `json:"data_type"`
	Extra         json.RawMessage `json:"extra"`
	Position      int             `json:"position"`
	DocumentCount int             `json:"document_count"`
	CreatedAt     string          `json:"created_at"`
}

// FieldValue is one custom field on one document.
type FieldValue struct {
	FieldID int64  `json:"field"`
	Value   string `json:"value"`
}

// Document is one piece of paper (or file) with what the app knows about it.
type Document struct {
	ID              int64   `json:"id"`
	UserID          int64   `json:"-"`
	Title           string  `json:"title"`
	Content         string  `json:"content,omitempty"`
	CorrespondentID *int64  `json:"correspondent"`
	DocumentTypeID  *int64  `json:"document_type"`
	StoragePathID   *int64  `json:"storage_path"`
	ASN             *int64  `json:"asn"`
	Created         string  `json:"created"`
	AddedAt         string  `json:"added_at"`
	ModifiedAt      string  `json:"modified_at"`
	OriginalName    string  `json:"original_name"`
	OriginalExt     string  `json:"-"`
	Mime            string  `json:"mime"`
	Checksum        string  `json:"checksum"`
	Size            int64   `json:"size"`
	ArchiveChecksum string  `json:"archive_checksum,omitempty"`
	ArchiveSize     int64   `json:"archive_size"`
	HasArchive      bool    `json:"has_archive"`
	HasThumb        bool    `json:"has_thumb"`
	PageCount       int     `json:"page_count"`
	Language        string  `json:"language"`
	Status          string  `json:"status"`
	StatusMessage   string  `json:"status_message"`
	Source          string  `json:"source"`
	Favorite        bool    `json:"favorite"`
	ContentEdited   bool    `json:"content_edited"`
	MirrorPath      string  `json:"mirror_path,omitempty"`
	ExternalID      string  `json:"external_id,omitempty"`
	DeletedAt       *string `json:"deleted_at,omitempty"`

	Tags       []int64      `json:"tags"`
	Fields     []FieldValue `json:"custom_fields"`
	NotesCount int          `json:"notes_count"`
	// Filled by searches
	Snippet string  `json:"snippet,omitempty"`
	Rank    float64 `json:"rank,omitempty"`
	// Filled by list for the cards
	CorrespondentName string `json:"correspondent_name,omitempty"`
	DocumentTypeName  string `json:"document_type_name,omitempty"`
	// Filled for documents another account shared
	Access     string   `json:"access,omitempty"` // owner | view | edit
	OwnerName  string   `json:"owner_name,omitempty"`
	SharedTags []string `json:"shared_tags,omitempty"`
}

// Note is a remark a person wrote on a document.
type Note struct {
	ID         int64  `json:"id"`
	DocumentID int64  `json:"document_id"`
	UserID     int64  `json:"user_id"`
	Text       string `json:"text"`
	CreatedAt  string `json:"created_at"`
}

// HistoryEntry is one thing that happened to a document.
type HistoryEntry struct {
	ID         int64           `json:"id"`
	DocumentID int64           `json:"document_id"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	Changes    json.RawMessage `json:"changes"`
	CreatedAt  string          `json:"created_at"`
}

// Job is one unit of background work.
type Job struct {
	ID         int64           `json:"id"`
	UserID     int64           `json:"user_id"`
	DocumentID *int64          `json:"document_id"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Attempts   int             `json:"attempts"`
	Progress   string          `json:"progress"`
	Log        string          `json:"log,omitempty"`
	Error      string          `json:"error"`
	Params     json.RawMessage `json:"params"`
	CreatedAt  string          `json:"created_at"`
	StartedAt  *string         `json:"started_at"`
	FinishedAt *string         `json:"finished_at"`
	// For the queue page
	DocumentTitle string `json:"document_title,omitempty"`
}

// SavedView is a named search.
type SavedView struct {
	ID          int64           `json:"id"`
	UserID      int64           `json:"-"`
	Name        string          `json:"name"`
	Query       json.RawMessage `json:"query"`
	OnDashboard bool            `json:"on_dashboard"`
	InSidebar   bool            `json:"in_sidebar"`
	Position    int             `json:"position"`
	CreatedAt   string          `json:"created_at"`
}

// Rule applies actions to incoming documents that match its filters.
type Rule struct {
	ID        int64       `json:"id"`
	UserID    int64       `json:"-"`
	Name      string      `json:"name"`
	Enabled   bool        `json:"enabled"`
	Trigger   string      `json:"trigger"`
	Position  int         `json:"position"`
	Filters   RuleFilters `json:"filters"`
	Actions   RuleActions `json:"actions"`
	CreatedAt string      `json:"created_at"`
}

// RuleFilters say which documents a rule is for. Empty means "any".
type RuleFilters struct {
	Filename      string  `json:"filename"`      // glob on the original file name, e.g. *invoice*.pdf
	Path          string  `json:"path"`          // glob on the consume sub-folder
	Source        string  `json:"source"`        // web | api | consume | import | ""
	Mime          string  `json:"mime"`          // prefix: application/pdf, image/
	Content       string  `json:"content"`       // a word or phrase in the text
	Correspondent *int64  `json:"correspondent"` // already assigned this correspondent
	DocumentType  *int64  `json:"document_type"` //
	Tags          []int64 `json:"tags"`          // has every one of these
	// Scheduled rules (trigger "scheduled"): fire once per document when
	// today is ScheduleOffsetDays after (negative: before) the date in
	// ScheduleField: "created", "added" or "cf:<custom field id>" (a date field).
	ScheduleField      string `json:"schedule_field"`
	ScheduleOffsetDays int    `json:"schedule_offset_days"`
}

// RuleActions are what a matching rule does.
type RuleActions struct {
	Title         string            `json:"title"` // template: {original_name} {correspondent} {created} …
	AddTags       []int64           `json:"add_tags"`
	RemoveTags    []int64           `json:"remove_tags"`
	Correspondent *int64            `json:"correspondent"`
	DocumentType  *int64            `json:"document_type"`
	StoragePath   *int64            `json:"storage_path"`
	Fields        map[string]string `json:"fields"` // field id → value
	Favorite      *bool             `json:"favorite"`
	SkipMatching  bool              `json:"skip_matching"` // don't run the tag/correspondent matchers after this
	Notify        bool              `json:"notify"`        // send a message (Settings → Integrations → Notifications)
	NotifyMessage string            `json:"notify_message"`
}

// Share is a public link to a document.
type Share struct {
	ID            int64   `json:"id"`
	UserID        int64   `json:"-"`
	DocumentID    int64   `json:"document_id"`
	DocumentTitle string  `json:"document_title,omitempty"`
	Token         string  `json:"token"`
	File          string  `json:"file"`
	ExpiresAt     *string `json:"expires_at"`
	Views         int     `json:"views"`
	CreatedAt     string  `json:"created_at"`
}

// Settings are the box's settings plus one person's preferences.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	// OCR (box-wide, admins)
	OCRLanguage          string `json:"ocr_language"`
	OCRMode              string `json:"ocr_mode"` // skip | redo | force
	OCRDeskew            bool   `json:"ocr_deskew"`
	OCRRotate            bool   `json:"ocr_rotate"`
	OCRClean             bool   `json:"ocr_clean"`
	OCRMaxPages          int    `json:"ocr_max_pages"`
	OCROutputType        string `json:"ocr_output_type"`
	TrashDays            int    `json:"trash_days"`
	ConsumeSubdirsAsTags bool   `json:"consume_subdirs_as_tags"`
	// Barcodes (box-wide): an ASN label sets the archive serial number; a
	// separator sheet splits a batch scan into documents.
	BarcodesEnabled  bool   `json:"barcodes_enabled"`
	BarcodeASNPrefix string `json:"barcode_asn_prefix"`
	BarcodeSeparator string `json:"barcode_separator"`
	BarcodeKeepSep   bool   `json:"barcode_keep_separator"` // keep the separator page in the next document
	BarcodeMaxPages  int    `json:"barcode_max_pages"`

	// The person's documents folder: a readable copy of the archive laid out
	// by storage path, on this server, a WebDAV server, their Nextcloud or
	// their Pocket Files, whose Inbox sub-folder feeds new files in.
	FolderKind      string `json:"folder_kind"` // none | folder | webdav | nextcloud | files
	FolderPath      string `json:"folder_path"` // folder: absolute path on the server
	FolderURL       string `json:"folder_url"`  // webdav: the folder's address; nextcloud: the server
	FolderUser      string `json:"folder_user"`
	FolderPassword  string `json:"-"`
	FolderName      string `json:"folder_name"`     // nextcloud, files: the folder inside the person's files
	FolderViaCore   bool   `json:"folder_via_core"` // nextcloud: account from Pocket Core (files: always)
	FolderFile      string `json:"folder_file"`     // archive | original
	FolderInbox     bool   `json:"folder_inbox"`    // take new files from <folder>/<inbox name>
	FolderInboxName string `json:"folder_inbox_name"`

	// Per person
	DateFormat    string `json:"date_format"`  // iso | dmy-dot | long
	DefaultView   string `json:"default_view"` // table | cards | list
	PageSize      int    `json:"page_size"`
	DefaultSort   string `json:"default_sort"`
	TitleFromName bool   `json:"title_from_name"` // new documents take the file name as title
	DateFromText  bool   `json:"date_from_text"`  // the created date is read from the text when found
	DateFromName  bool   `json:"date_from_name"`  // … or from the file name (2026-03-14_invoice.pdf) first
	SuggestOnOpen bool   `json:"suggest_on_open"` // show suggestions on the document page
	MoneyMode     string `json:"money_mode"`      // core | direct | off
	MoneyURL      string `json:"money_url"`
	MoneyToken    string `json:"-"`
	MoneyAccount  int64  `json:"money_account"`
	NotifyURL     string `json:"notify_url"` // Apprise / ntfy URL told when a document fails
}
