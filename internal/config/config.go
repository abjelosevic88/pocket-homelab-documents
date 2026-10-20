// Package config loads runtime configuration from environment variables.
//
// Every option has a sensible default so that `docker run` with a single
// volume works out of the box. See docs/configuration.md for the full list.
package config

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Host           string
	Port           int
	BaseURL        string
	DataDir        string
	DBPath         string
	OriginalsDir   string // uploaded files as they came
	ArchiveDir     string // the OCR'd PDF/A twin
	ThumbsDir      string
	PagesDir       string   // page images rendered on demand
	ConsumeDir     string   // a watched folder: drop a file, it becomes a document
	MirrorDir      string   // the default server folder offered as a documents folder
	FolderRoots    []string // server folders people may choose as their documents folder (FOLDER_ROOTS)
	FolderPoll     time.Duration
	TmpDir         string
	LogLevel       string
	LogFormat      string // text | json
	TrustProxy     bool
	MetricsEnabled bool

	MaxUploadBytes int64

	// OCR
	OCRLanguage   string        // tesseract codes joined with +, e.g. eng+deu
	OCRMode       string        // skip | redo | force
	OCRWorkers    int           // documents processed at once
	OCRThreads    int           // threads ocrmypdf may use per document
	OCRTimeout    time.Duration // per document
	OCRDeskew     bool
	OCRRotate     bool
	OCRClean      bool
	OCRMaxPages   int    // 0 = all
	OCROutputType string // pdfa | pdf
	OCRDPI        int    // for images without one
	GotenbergURL  string // office documents become PDFs here when set
	ConsumePoll   time.Duration
	ConsumeDelete bool // remove the file from the consume folder once it is in

	AuthMode             string // local | proxy | none
	AuthProxyHeader      string
	AuthProxyEmailHeader string
	SessionTTL           time.Duration
	SecureCookies        bool
	AdminEmail           string
	AdminPassword        string
	AdminName            string

	// Sign in with an OpenID Connect provider. On a box with Pocket Core none
	// of these are needed: the app uses the client Core issued at registration.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCName         string // button label
	OIDCAutoCreate   bool   // unknown (verified) emails get a member account

	// Pocket Core: when CORE_URL is set the app registers itself on start,
	// publishes events and accepts the tokens Core hands to paired phones.
	CoreURL             string
	CoreRegistrationKey string
	CoreAppURL          string // how Core reaches this app; defaults to BASE_URL

	DemoData bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if v := env(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := env(key, ""); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	dataDir := env("DATA_DIR", "./data")
	threads := runtime.NumCPU()
	if threads > 4 {
		threads = 4
	}
	c := &Config{
		Host:           env("HOST", "0.0.0.0"),
		Port:           envInt("PORT", 8080),
		BaseURL:        strings.TrimRight(env("BASE_URL", ""), "/"),
		DataDir:        dataDir,
		DBPath:         env("DB_PATH", dataDir+"/pocket-documents.db"),
		OriginalsDir:   env("ORIGINALS_DIR", dataDir+"/originals"),
		ArchiveDir:     env("ARCHIVE_DIR", dataDir+"/archive"),
		ThumbsDir:      env("THUMBS_DIR", dataDir+"/thumbs"),
		PagesDir:       env("PAGES_DIR", dataDir+"/pages"),
		ConsumeDir:     env("CONSUME_DIR", dataDir+"/consume"),
		MirrorDir:      env("MIRROR_DIR", dataDir+"/mirror"),
		TmpDir:         env("TMP_DIR", dataDir+"/tmp"),
		LogLevel:       strings.ToLower(env("LOG_LEVEL", "info")),
		LogFormat:      strings.ToLower(env("LOG_FORMAT", "text")),
		TrustProxy:     envBool("TRUST_PROXY", true),
		MetricsEnabled: envBool("METRICS_ENABLED", true),

		MaxUploadBytes: envInt64("MAX_UPLOAD_BYTES", 200<<20),

		OCRLanguage:   env("OCR_LANGUAGE", "eng"),
		OCRMode:       strings.ToLower(env("OCR_MODE", "skip")),
		OCRWorkers:    envInt("OCR_WORKERS", 1),
		OCRThreads:    envInt("OCR_THREADS", threads),
		OCRTimeout:    envDuration("OCR_TIMEOUT", 30*time.Minute),
		OCRDeskew:     envBool("OCR_DESKEW", true),
		OCRRotate:     envBool("OCR_ROTATE", true),
		OCRClean:      envBool("OCR_CLEAN", false),
		OCRMaxPages:   envInt("OCR_MAX_PAGES", 0),
		OCROutputType: strings.ToLower(env("OCR_OUTPUT_TYPE", "pdfa")),
		OCRDPI:        envInt("OCR_IMAGE_DPI", 300),
		GotenbergURL:  strings.TrimRight(env("GOTENBERG_URL", ""), "/"),
		ConsumePoll:   envDuration("CONSUME_POLL", 10*time.Second),
		FolderPoll:    envDuration("FOLDER_INBOX_POLL", 2*time.Minute),
		ConsumeDelete: envBool("CONSUME_DELETE", true),

		AuthMode:             strings.ToLower(env("AUTH_MODE", "local")),
		AuthProxyHeader:      env("AUTH_PROXY_HEADER", "Remote-User"),
		AuthProxyEmailHeader: env("AUTH_PROXY_EMAIL_HEADER", "Remote-Email"),
		SessionTTL:           envDuration("SESSION_TTL", 720*time.Hour),
		SecureCookies:        envBool("SECURE_COOKIES", false),
		AdminEmail:           strings.ToLower(env("ADMIN_EMAIL", "")),
		AdminPassword:        env("ADMIN_PASSWORD", ""),
		AdminName:            env("ADMIN_NAME", "Admin"),

		OIDCIssuer:       strings.TrimRight(env("OIDC_ISSUER", ""), "/"),
		OIDCClientID:     env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: env("OIDC_CLIENT_SECRET", ""),
		OIDCName:         env("OIDC_NAME", "single sign-on"),
		OIDCAutoCreate:   envBool("OIDC_AUTO_CREATE", true),

		CoreURL:             strings.TrimRight(env("CORE_URL", ""), "/"),
		CoreRegistrationKey: env("CORE_REGISTRATION_KEY", ""),
		CoreAppURL:          strings.TrimRight(env("CORE_APP_URL", ""), "/"),

		DemoData: envBool("DEMO_DATA", false),
	}
	if c.CoreAppURL == "" {
		c.CoreAppURL = c.BaseURL
	}
	for _, r := range strings.Split(env("FOLDER_ROOTS", ""), ",") {
		if r = strings.TrimSpace(r); r != "" {
			c.FolderRoots = append(c.FolderRoots, r)
		}
	}
	if len(c.FolderRoots) == 0 && c.MirrorDir != "" {
		c.FolderRoots = []string{c.MirrorDir}
	}
	if c.FolderPoll < 15*time.Second {
		c.FolderPoll = 15 * time.Second
	}
	switch c.AuthMode {
	case "local", "proxy", "none":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be one of local, proxy, none (got %q)", c.AuthMode)
	}
	switch c.OCRMode {
	case "skip", "redo", "force":
	default:
		return nil, fmt.Errorf("OCR_MODE must be one of skip, redo, force (got %q)", c.OCRMode)
	}
	if c.OCROutputType != "pdf" {
		c.OCROutputType = "pdfa"
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("PORT out of range: %d", c.Port)
	}
	if c.OCRWorkers < 1 {
		c.OCRWorkers = 1
	}
	if c.OCRThreads < 1 {
		c.OCRThreads = 1
	}
	if c.MaxUploadBytes < 1<<20 {
		return nil, fmt.Errorf("MAX_UPLOAD_BYTES too small: %d", c.MaxUploadBytes)
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || c.BaseURL == "") {
		return nil, fmt.Errorf("OIDC_ISSUER needs OIDC_CLIENT_ID and BASE_URL (the callback is BASE_URL/api/v1/auth/oidc/callback)")
	}
	return c, nil
}

// Addr returns host:port.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }
