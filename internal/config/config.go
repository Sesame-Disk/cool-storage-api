package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for SesameFS
type Config struct {
	Server              ServerConfig                  `yaml:"server"`
	Database            DatabaseConfig                `yaml:"database"`
	Storage             StorageConfig                 `yaml:"storage"`
	WebUploads          WebUploadsConfig              `yaml:"web_uploads"`
	Billing             BillingConfig                 `yaml:"billing"`
	Accounts            AccountsConfig                `yaml:"accounts"`
	Organizations       OrganizationsConfig           `yaml:"organizations"`
	Auth                AuthConfig                    `yaml:"auth"`
	Chunking            ChunkingConfig                `yaml:"chunking"`
	Versioning          VersioningConfig              `yaml:"versioning"`
	GC                  GCConfig                      `yaml:"gc"`
	SeafHTTP            SeafHTTPConfig                `yaml:"seafhttp"`
	CORS                CORSConfig                    `yaml:"cors"`
	OnlyOffice          OnlyOfficeConfig              `yaml:"onlyoffice"`
	Elasticsearch       ElasticsearchConfig           `yaml:"elasticsearch"`
	Monitoring          MonitoringConfig              `yaml:"monitoring"`
	FileView            FileViewConfig                `yaml:"fileview"`
	EnforcementProfiles map[string]EnforcementProfile `yaml:"enforcement_profiles"`
	envOverrideErrors   []string
}

// WebUploadsConfig holds browser upload behavior exposed to the web frontend.
// Size values are expressed in MB to match the legacy frontend page-option contract.
type WebUploadsConfig struct {
	EnableUploadFolder        bool `yaml:"enable_upload_folder"`
	EnableResumableFileUpload bool `yaml:"enable_resumable_file_upload"`
	// EnableWebBlockUpload server-side-gates the web content-addressed (block)
	// upload flow: block-upload-session, file-from-blocks, and the session mode
	// of /blocks/check and /blocks/upload. Default false so the backend surface
	// stays closed even though the routes are always registered.
	EnableWebBlockUpload bool  `yaml:"enable_web_block_upload"`
	ResumableChunkSizeMB int64 `yaml:"resumable_chunk_size_mb"`
	// WebBlockUploadBlockSizeMB is the content-addressed (CAS) block size used by
	// the web block-upload flow: the size each file is split and SHA-256 hashed
	// into, validated exactly on commit (file-from-blocks). It is NOT the resumable
	// transport chunk size — keep it equal to the system block size (8 MB) so web
	// blocks dedup against blocks produced by the rest of the system. The server is
	// the single source of truth: it is echoed in the block-upload-session response
	// and the web client hashes/slices to it instead of hardcoding a size.
	WebBlockUploadBlockSizeMB int64 `yaml:"web_block_upload_block_size_mb"`
	// MaxConcurrentBlockUploadsPerUser caps how many concurrent session-mode
	// /blocks/upload requests a single user may have in flight at once. Each such
	// request buffers one CAS block (the configured web_block_upload_block_size_mb,
	// default 8 MB — the server bounds a session upload to that size, not
	// chunking.absolute_max), so this bounds the instantaneous RAM one
	// authenticated user can force the server to hold to roughly
	// cap × block_size (default 8 × 8 MB = 64 MB) — an anti-abuse backstop, not the
	// staging-bytes cap. A value <= 0 disables the cap (unlimited).
	// See docs/WEB-BLOCK-UPLOAD.md item 18.
	MaxConcurrentBlockUploadsPerUser int `yaml:"max_concurrent_block_uploads_per_user"`
	// MaxUncommittedBlockSessionsPerUser caps how many concurrent *uncommitted*
	// web block-upload sessions one user may hold (claimed atomically via LWT slots
	// at session creation). Together with MaxStagedBytesPerSessionMB it bounds the
	// uncommitted backend bytes a user can force (≈ sessions × per-session ceiling)
	// without a drifting aggregate counter. A value <= 0 disables the cap.
	// See docs/WEB-BLOCK-UPLOAD.md item 1.
	MaxUncommittedBlockSessionsPerUser int `yaml:"max_uncommitted_block_sessions_per_user"`
	// MaxStagedBytesPerSessionMB is the per-session staging ceiling — in effect the
	// MAXIMUM web-block file size, because one session uploads one file. The EXACT
	// per-file bound is enforced by the declared-size fail-fast at session creation
	// plus the commit's manifest.size == expected_size guard; the per-block
	// staged-block ledger derived from this value is a deliberately LOOSE anti-abuse
	// backstop (roughly 2x per-bucket headroom plus slack; up to 5x for a one-block
	// ceiling), not an exact byte limit. > 0 = explicit MB; 0 = derive from the
	// resolved max file size × 1.25 (falling back to a documented operational
	// default when max file size is unlimited); < 0 = disable the per-session cap.
	// See EffectiveMaxStagedBytesPerSession().
	MaxStagedBytesPerSessionMB int64 `yaml:"max_staged_bytes_per_session_mb"`
	MaxFileSizeMB              int64 `yaml:"max_file_size_mb"`
	MaxFilesPerBatch           int   `yaml:"max_files_per_batch"`
	SimultaneousUploads        int   `yaml:"simultaneous_uploads"`
}

// ResolvedMaxFileSizeMB returns the effective browser upload file-size cap.
// A non-positive web_uploads.max_file_size_mb falls back to server.max_upload_mb.
func (c *Config) ResolvedMaxFileSizeMB() int64 {
	if c == nil {
		return 0
	}
	if c.WebUploads.MaxFileSizeMB > 0 {
		return c.WebUploads.MaxFileSizeMB
	}
	if c.Server.MaxUploadMB > 0 {
		return c.Server.MaxUploadMB
	}
	return 0
}

// DefaultWebBlockUploadBlockSizeMB is the fallback CAS block size (MiB) when the
// config is missing or non-positive. It must match the system block size so web
// blocks dedup against blocks produced by the rest of the system.
const DefaultWebBlockUploadBlockSizeMB int64 = 8

// WebBlockUploadBlockSize returns the content-addressed (CAS) block size in bytes
// for the web block-upload flow, sourced from web_uploads.web_block_upload_block_size_mb.
// A nil/non-positive config falls back to the default so callers never split on 0.
func (c *Config) WebBlockUploadBlockSize() int64 {
	mb := DefaultWebBlockUploadBlockSizeMB
	if c != nil && c.WebUploads.WebBlockUploadBlockSizeMB > 0 {
		mb = c.WebUploads.WebBlockUploadBlockSizeMB
	}
	return mb * 1024 * 1024
}

// DefaultMaxStagedBytesPerSession is the operational fallback ceiling (bytes) for
// the per-session staged cap when web_uploads.max_staged_bytes_per_session_mb is 0
// (derive) AND the resolved max file size is unlimited. It is effectively the
// maximum web-block file size in that configuration; operators must raise the knob
// to allow larger web block uploads. 12 GiB.
const DefaultMaxStagedBytesPerSession int64 = 12 * 1024 * 1024 * 1024

// stagedBytesPerSessionDeriveSlackNum/Den apply a 1.25× slack when deriving the
// per-session ceiling from the max file size, so a legit file that hashes/dedups
// slightly imperfectly is not rejected at its last blocks.
const stagedBytesPerSessionDeriveSlackNum = 5
const stagedBytesPerSessionDeriveSlackDen = 4

// EffectiveMaxStagedBytesPerSession returns the per-session staged-bytes ceiling in
// bytes (in effect the maximum web-block file size, since one session uploads one
// file). Semantics of web_uploads.max_staged_bytes_per_session_mb:
//   - > 0 : explicit MB.
//   - 0   : derive from ResolvedMaxFileSizeMB() × 1.25; if max file size is
//     unlimited, fall back to DefaultMaxStagedBytesPerSession.
//   - < 0 : disabled — returns 0, which callers treat as "no per-session cap".
func (c *Config) EffectiveMaxStagedBytesPerSession() int64 {
	if c == nil {
		return DefaultMaxStagedBytesPerSession
	}
	v := c.WebUploads.MaxStagedBytesPerSessionMB
	if v > 0 {
		return v * 1024 * 1024
	}
	if v < 0 {
		return 0 // disabled
	}
	maxFileMB := c.ResolvedMaxFileSizeMB()
	if maxFileMB <= 0 {
		return DefaultMaxStagedBytesPerSession
	}
	return maxFileMB * 1024 * 1024 * stagedBytesPerSessionDeriveSlackNum / stagedBytesPerSessionDeriveSlackDen
}

var cassandraDCNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// EnforcementProfile defines feature flags and numeric limits for a quota_policy.
// Keyed by quota_policy value ("hard", "soft") in the config map.
type EnforcementProfile struct {
	Features EnforcementFeatures `yaml:"features"`
	Limits   EnforcementLimits   `yaml:"limits"`
}

// EnforcementFeatures are boolean feature flags per enforcement profile.
type EnforcementFeatures struct {
	CanAddRepo                    bool `yaml:"can_add_repo" json:"can_add_repo"`
	CanShareRepo                  bool `yaml:"can_share_repo" json:"can_share_repo"`
	CanAddGroup                   bool `yaml:"can_add_group" json:"can_add_group"`
	CanGenerateShareLink          bool `yaml:"can_generate_share_link" json:"can_generate_share_link"`
	CanGenerateUploadLink         bool `yaml:"can_generate_upload_link" json:"can_generate_upload_link"`
	CanSendShareLinkMail          bool `yaml:"can_send_share_link_mail" json:"can_send_share_link_mail"`
	CanPublishRepo                bool `yaml:"can_publish_repo" json:"can_publish_repo"`
	CanUseGlobalAddressBook       bool `yaml:"can_use_global_address_book" json:"can_use_global_address_book"`
	CanConnectWithDesktopClients  bool `yaml:"can_connect_with_desktop_clients" json:"can_connect_with_desktop_clients"`
	CanConnectWithAndroidClients  bool `yaml:"can_connect_with_android_clients" json:"can_connect_with_android_clients"`
	CanConnectWithIOSClients      bool `yaml:"can_connect_with_ios_clients" json:"can_connect_with_ios_clients"`
	CanExportFilesViaMobileClient bool `yaml:"can_export_files_via_mobile_client" json:"can_export_files_via_mobile_client"`
}

// EnforcementLimits are numeric limits per enforcement profile. -1 or 0 = unlimited.
type EnforcementLimits struct {
	MaxLibraries            int `yaml:"max_libraries" json:"max_libraries"`
	MaxShareLinks           int `yaml:"max_share_links" json:"max_share_links"`
	MaxUploadLinks          int `yaml:"max_upload_links" json:"max_upload_links"`
	ShareLinkExpireDaysMax  int `yaml:"share_link_expire_days_max" json:"share_link_expire_days_max"`
	UploadLinkExpireDaysMax int `yaml:"upload_link_expire_days_max" json:"upload_link_expire_days_max"`
}

// GetEnforcementProfile returns the profile for a quota_policy value.
// Falls back to "hard" defaults if the policy is not configured.
func (c *Config) GetEnforcementProfile(quotaPolicy string) EnforcementProfile {
	if quotaPolicy == "" {
		quotaPolicy = "hard"
	}
	if profile, ok := c.EnforcementProfiles[quotaPolicy]; ok {
		return profile
	}
	// Return hard defaults if not configured
	return DefaultHardProfile()
}

// DefaultHardProfile returns the built-in free tier enforcement profile.
func DefaultHardProfile() EnforcementProfile {
	return EnforcementProfile{
		Features: EnforcementFeatures{
			CanAddRepo:                    true,
			CanShareRepo:                  true,
			CanAddGroup:                   false,
			CanGenerateShareLink:          true,
			CanGenerateUploadLink:         true,
			CanSendShareLinkMail:          false,
			CanPublishRepo:                false,
			CanUseGlobalAddressBook:       false,
			CanConnectWithDesktopClients:  true,
			CanConnectWithAndroidClients:  true,
			CanConnectWithIOSClients:      true,
			CanExportFilesViaMobileClient: true,
		},
		Limits: EnforcementLimits{
			MaxLibraries:            3,
			MaxShareLinks:           3,
			MaxUploadLinks:          1,
			ShareLinkExpireDaysMax:  3,
			UploadLinkExpireDaysMax: 3,
		},
	}
}

// DefaultSoftProfile returns the built-in paid tier enforcement profile.
func DefaultSoftProfile() EnforcementProfile {
	return EnforcementProfile{
		Features: EnforcementFeatures{
			CanAddRepo:                    true,
			CanShareRepo:                  true,
			CanAddGroup:                   true,
			CanGenerateShareLink:          true,
			CanGenerateUploadLink:         true,
			CanSendShareLinkMail:          true,
			CanPublishRepo:                true,
			CanUseGlobalAddressBook:       true,
			CanConnectWithDesktopClients:  true,
			CanConnectWithAndroidClients:  true,
			CanConnectWithIOSClients:      true,
			CanExportFilesViaMobileClient: true,
		},
		Limits: EnforcementLimits{
			MaxLibraries:            -1, // unlimited
			MaxShareLinks:           -1,
			MaxUploadLinks:          -1,
			ShareLinkExpireDaysMax:  0, // 0 = no forced limit
			UploadLinkExpireDaysMax: 0,
		},
	}
}

// MonitoringConfig holds observability settings (metrics, health checks)
type MonitoringConfig struct {
	MetricsEnabled bool          `yaml:"metrics_enabled"` // default: true
	MetricsPath    string        `yaml:"metrics_path"`    // default: /metrics
	HealthTimeout  time.Duration `yaml:"health_timeout"`  // default: 3s
}

// BillingConfig holds external billing portal integration settings.
type BillingConfig struct {
	URL string `yaml:"url"`
}

// AccountsConfig holds external account-management integration settings.
type AccountsConfig struct {
	DeleteAccountURL     string `yaml:"delete_account_url"`
	OrgUserManagementURL string `yaml:"org_user_management_url"`
	DisableOrgUserWrites bool   `yaml:"disable_org_user_writes"`
}

// ResolveAccountsOrgUserManagementURL returns the configured external Accounts
// URL for org membership/role management. The optional {org_id} placeholder is
// replaced when present.
func (c *Config) ResolveAccountsOrgUserManagementURL(orgID string) string {
	if c == nil {
		return ""
	}
	target := strings.TrimSpace(c.Accounts.OrgUserManagementURL)
	if target == "" {
		return ""
	}
	return strings.ReplaceAll(target, "{org_id}", strings.TrimSpace(orgID))
}

// OrganizationsConfig holds reusable organization plan/template defaults.
type OrganizationsConfig struct {
	DefaultTemplate string                          `yaml:"default_template"`
	Templates       map[string]OrganizationTemplate `yaml:"templates"`
}

// OrganizationTemplate defines the persisted defaults for newly created orgs.
type OrganizationTemplate struct {
	Plan                 string            `yaml:"plan"`
	QuotaPolicy          string            `yaml:"quota_policy"`
	BillingCycle         string            `yaml:"billing_cycle"`
	StorageQuota         int64             `yaml:"storage_quota"`
	TrafficQuota         int64             `yaml:"traffic_quota"`
	TrafficUploadQuota   int64             `yaml:"traffic_upload_quota"`
	TrafficDownloadQuota int64             `yaml:"traffic_download_quota"`
	MaxUsers             int               `yaml:"max_users"`
	Settings             map[string]string `yaml:"settings"`
	StorageConfig        map[string]string `yaml:"storage_config"`
	ChunkingPolynomial   int64             `yaml:"chunking_polynomial"`
}

// DefaultFreeOrganizationTemplate returns the built-in free plan defaults.
func DefaultFreeOrganizationTemplate() OrganizationTemplate {
	return OrganizationTemplate{
		Plan:                 "free",
		QuotaPolicy:          "hard",
		BillingCycle:         "monthly",
		StorageQuota:         2 * 1000 * 1000 * 1000,  // 2 GB (decimal, matches frontend)
		TrafficQuota:         10 * 1000 * 1000 * 1000, // 10 GB (decimal, matches frontend)
		TrafficUploadQuota:   -1,
		TrafficDownloadQuota: -1,
		MaxUsers:             1,
		Settings: map[string]string{
			"theme":    "default",
			"features": "all",
		},
		ChunkingPolynomial: 17592186044415,
	}
}

func mergeOrganizationTemplate(base, override OrganizationTemplate) OrganizationTemplate {
	merged := base
	if strings.TrimSpace(override.Plan) != "" {
		merged.Plan = strings.TrimSpace(override.Plan)
	}
	if strings.TrimSpace(override.QuotaPolicy) != "" {
		merged.QuotaPolicy = strings.TrimSpace(override.QuotaPolicy)
	}
	if strings.TrimSpace(override.BillingCycle) != "" {
		merged.BillingCycle = strings.TrimSpace(override.BillingCycle)
	}
	if override.StorageQuota != 0 {
		merged.StorageQuota = override.StorageQuota
	}
	if override.TrafficQuota != 0 {
		merged.TrafficQuota = override.TrafficQuota
	}
	if override.TrafficUploadQuota != 0 {
		merged.TrafficUploadQuota = override.TrafficUploadQuota
	}
	if override.TrafficDownloadQuota != 0 {
		merged.TrafficDownloadQuota = override.TrafficDownloadQuota
	}
	if override.MaxUsers != 0 {
		merged.MaxUsers = override.MaxUsers
	}
	if len(override.Settings) > 0 {
		merged.Settings = override.Settings
	}
	if len(override.StorageConfig) > 0 {
		merged.StorageConfig = override.StorageConfig
	}
	if override.ChunkingPolynomial != 0 {
		merged.ChunkingPolynomial = override.ChunkingPolynomial
	}
	return merged
}

// GetOrganizationTemplate returns the configured template for new organizations.
// Custom templates inherit sane free-tier defaults unless explicitly overridden.
func (c *Config) GetOrganizationTemplate(name string) OrganizationTemplate {
	templateName := strings.TrimSpace(name)
	if templateName == "" {
		templateName = strings.TrimSpace(c.Organizations.DefaultTemplate)
	}
	if templateName == "" {
		templateName = "free"
	}

	tpl := DefaultFreeOrganizationTemplate()
	if configured, ok := c.Organizations.Templates[templateName]; ok {
		tpl = mergeOrganizationTemplate(tpl, configured)
	}
	if strings.TrimSpace(tpl.Plan) == "" {
		tpl.Plan = templateName
	}
	if tpl.Settings == nil {
		tpl.Settings = map[string]string{}
	}
	if tpl.StorageConfig == nil {
		tpl.StorageConfig = map[string]string{}
	}
	if strings.TrimSpace(tpl.StorageConfig["default_backend"]) == "" {
		tpl.StorageConfig["default_backend"] = c.Storage.DefaultClass
	}
	return tpl
}

// QuotaPeriodEnd returns the next org quota-period boundary.
//
// Traffic quota periods are always monthly regardless of billing_cycle. This
// helper is shared by org creation defaults and rollover so both paths use the
// same monthly clamped-month semantics.
func QuotaPeriodEnd(start time.Time) time.Time {
	return addClampedMonth(start.UTC())
}

// PeriodEnd returns the persisted org quota-period end for a template starting at start.
func (t OrganizationTemplate) PeriodEnd(start time.Time) time.Time {
	return QuotaPeriodEnd(start)
}

func addClampedMonth(t time.Time) time.Time {
	y, m, d := t.Date()

	targetM := m + 1
	targetY := y
	if targetM > 12 {
		targetM = 1
		targetY++
	}

	maxDay := time.Date(targetY, targetM+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if d > maxDay {
		d = maxDay
	}

	return time.Date(targetY, targetM, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// FileViewConfig holds file preview and streaming settings
type FileViewConfig struct {
	MaxPreviewBytes      int64    `yaml:"max_preview_bytes"`       // Maximum file size for general inline preview (default: 1GB)
	MaxVideoBytes        int64    `yaml:"max_video_bytes"`         // Maximum file size for video preview (default: 10GB)
	MaxTextBytes         int64    `yaml:"max_text_bytes"`          // Maximum file size for text preview (default: 50MB)
	MaxIWorkPreviewBytes int64    `yaml:"max_iwork_preview_bytes"` // Maximum size for extracted iWork preview (default: 50MB)
	PreviewExtensions    []string `yaml:"preview_extensions"`      // Extensions that should route to the frontend preview shell
}

var supportedFileViewPreviewExtensions = []string{
	"pdf",
	"png", "jpg", "jpeg", "gif", "bmp", "webp", "svg", "ico", "tiff", "tif",
	"mp4", "webm", "ogg", "mov",
	"mp3", "wav", "flac", "aac",
	"txt", "md", "markdown", "json", "yaml", "yml", "xml", "csv",
	"html", "htm", "css", "js", "ts", "jsx", "tsx",
	"py", "go", "rs", "java", "c", "cpp", "h", "hpp",
	"sh", "bash", "zsh", "fish",
	"toml", "ini", "cfg", "conf", "env",
	"sql", "graphql", "proto",
	"dockerfile", "makefile",
	"rb", "php", "swift", "kt", "scala", "r", "lua", "pl",
	"log", "diff", "patch",
}

func defaultFileViewPreviewExtensions() []string {
	return append([]string(nil), supportedFileViewPreviewExtensions...)
}

func normalizeFileViewPreviewExtensions(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{}, nil
	}

	allowed := make(map[string]struct{}, len(supportedFileViewPreviewExtensions))
	for _, ext := range supportedFileViewPreviewExtensions {
		allowed[ext] = struct{}{}
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		ext := strings.ToLower(strings.TrimSpace(raw))
		if ext == "" {
			continue
		}
		if _, ok := allowed[ext]; !ok {
			return nil, fmt.Errorf("fileview.preview_extensions contains unsupported extension %q", raw)
		}
		if _, ok := seen[ext]; ok {
			continue
		}
		seen[ext] = struct{}{}
		normalized = append(normalized, ext)
	}

	return normalized, nil
}

// OnlyOfficeConfig holds OnlyOffice Document Server integration settings
// See: https://manual.seafile.com/deploy/only_office/
type OnlyOfficeConfig struct {
	Enabled           bool     `yaml:"enabled"`
	APIJSURL          string   `yaml:"api_js_url"`         // URL to api.js loaded by browser (e.g., http://localhost:8088/web-apps/apps/api/documents/api.js)
	JWTSecret         string   `yaml:"jwt_secret"`         // JWT secret for signing tokens
	VerifyCertificate bool     `yaml:"verify_certificate"` // Whether to verify OnlyOffice SSL cert
	ForceSave         bool     `yaml:"force_save"`         // Enable force save on user action
	ViewExtensions    []string `yaml:"view_extensions"`    // Extensions that can be viewed (doc, docx, ppt, etc.)
	EditExtensions    []string `yaml:"edit_extensions"`    // Extensions that can be edited (docx, pptx, xlsx)
	ServerURL         string   `yaml:"server_url"`         // URL for OnlyOffice to reach SesameFS (e.g., http://sesamefs:8080)
	InternalURL       string   `yaml:"internal_url"`       // URL for SesameFS to reach OnlyOffice internally (e.g., http://onlyoffice:80)
	MaxDocumentBytes  int64    `yaml:"max_document_bytes"` // Maximum OnlyOffice callback download size before rejecting the save
	JWTTTLSeconds     int      `yaml:"jwt_ttl_seconds"`    // JWT token lifetime in seconds (default 3600 = 1 hour)
}

// ElasticsearchConfig holds Elasticsearch search backend settings
type ElasticsearchConfig struct {
	Enabled bool     `yaml:"enabled"`
	URLs    []string `yaml:"urls"`  // Elasticsearch cluster URLs
	Index   string   `yaml:"index"` // Index name (default: sesamefs-files)
}

// CORSConfig holds CORS settings for frontend access
type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// SeafHTTPConfig holds Seafile-compatible file transfer settings
type SeafHTTPConfig struct {
	TokenTTL               time.Duration `yaml:"token_ttl"`                 // How long upload/download tokens are valid
	ZipMaxEntries          int           `yaml:"zip_max_entries"`           // Maximum files allowed in a streamed ZIP download
	ZipMaxDepth            int           `yaml:"zip_max_depth"`             // Maximum directory nesting allowed in a streamed ZIP download
	ZipMaxBytes            int64         `yaml:"zip_max_bytes"`             // Maximum total uncompressed bytes allowed in a streamed ZIP download
	ChunkedStagingMaxBytes int64         `yaml:"chunked_staging_max_bytes"` // Maximum declared bytes reserved across active chunked uploads on this node; 0 disables the guard

	// SyncBlockMaxBytes bounds one PUT /seafhttp/repo/:repo_id/block/:block_id
	// body. PutBlock buffers the whole body in memory before hashing, so this is
	// the per-request buffered-body bound for the desktop-sync block route — the
	// dominant term in its memory cost, not a hard ceiling on it: io.ReadAll can
	// over-allocate while growing, and hashing plus HTTP machinery add their own.
	//
	// The default is sized from the traffic this route is expected to carry, not
	// from the web uploader's adaptive ceiling: SesameFS splits at 8 MiB
	// (`uploadBlockSize`, matching Seafile's default CDC block) and the official
	// client's CDC maximum is smaller still. 16 MiB leaves 2x headroom over the
	// 8 MiB block plus room for cipher padding, while cutting the previous
	// 257 MiB bound by 16x. That 8 MiB figure is what the code splits at, not a
	// production measurement — `sync_put_block_body_bytes` exists to measure it.
	//
	// It is NOT an aggregate bound. N concurrent uploads still cost N x this
	// value; capping total in-flight readers is X10 / subcontract B of
	// ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01 and is still open.
	//
	// Zero is rejected rather than meaning "unlimited": an unbounded body on this
	// route is the defect (F12), so there is no configuration that restores it.
	SyncBlockMaxBytes int64 `yaml:"sync_block_max_bytes"`

	// UploadLinkWritesPerMinute bounds anonymous writes through a public upload
	// link, keyed on client IP *and* upload token. This is subcontract A1 of
	// ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01: the upload-link *routes* under
	// /api/v2.1 already carry a per-IP limiter, but the seafhttp upload endpoint
	// they hand off to carries none, so the actual write was unbounded.
	//
	// It applies ONLY to tokens whose Source is "link". The same endpoint serves
	// authenticated web uploads, and a limiter applied by URL rather than by token
	// origin would throttle those too — they are not the anonymous surface this
	// bounds. Zero disables it.
	//
	// Keyed on (IP, token) rather than IP alone because a single address is
	// routinely a whole office, school or mobile carrier NAT. Keyed on IP alone,
	// one person uploading through one link would throttle everyone else behind
	// that address who is using a *different* link.
	//
	// This is a request-rate bound, not a cost bound: a 20 KiB photo and an 8 MiB
	// chunk each spend one token. Bounding simultaneous in-flight work is A2 and
	// is still open.
	UploadLinkWritesPerMinute int `yaml:"upload_link_writes_per_minute"`

	// UploadLinkWriteBurst is the token-bucket burst for the limiter above. A
	// browser uploading one file issues several requests back to back (chunks),
	// and a dropped folder issues one per small file, so the burst is what decides
	// whether ordinary use ever meets the limiter at all.
	UploadLinkWriteBurst int `yaml:"upload_link_write_burst"`

	// UploadLinkTokenWritesPerMinute bounds writes against a single upload token
	// across *all* client IPs. The (IP, token) bucket above cannot see a leaked
	// upload URL being hit from many addresses at once; this one can.
	//
	// It is deliberately far above any plausible legitimate figure, because a
	// shared link legitimately serves many people at once — a classroom uploading
	// to one link is normal traffic, not abuse. Zero disables it.
	//
	// Note the limit of this defence: an attacker who re-mints a fresh upload
	// token per request gets a fresh bucket. That path is bounded instead by the
	// per-IP limiter already on /api/v2.1/upload-links/:token/upload/.
	UploadLinkTokenWritesPerMinute int `yaml:"upload_link_token_writes_per_minute"`

	// UploadLinkTokenWriteBurst is the token-bucket burst for the per-token limit.
	UploadLinkTokenWriteBurst int `yaml:"upload_link_token_write_burst"`
}

// Sync block body bounds. MaxSyncBlockMaxBytes is a validation ceiling, not a
// default: a value above it is almost certainly derived from the web uploader's
// chunk ceiling by mistake, which is exactly how the 257 MiB bound arose.
const (
	DefaultSyncBlockMaxBytes int64 = 16 * 1024 * 1024
	MaxSyncBlockMaxBytes     int64 = 64 * 1024 * 1024

	// Anonymous upload-link write limits.
	//
	// These are starting values, not measured ones, and they are deliberately
	// generous. The failure mode of a rate limit on a data path is not "an
	// attacker gets through" — it is a real person's upload dying — so the
	// defaults are set well above plausible browser behaviour and left to be
	// tightened from `upload_link_write_throttled_total`. At 8 MiB chunks, 600/min
	// sustains ~80 MiB/s per (IP, token) after a burst covering ~9 GiB or 1200
	// small files.
	//
	// The real bound on this surface is concurrency (A2), not request rate.
	DefaultUploadLinkWritesPerMinute      = 600
	DefaultUploadLinkWriteBurst           = 1200
	DefaultUploadLinkTokenWritesPerMinute = 12000
	DefaultUploadLinkTokenWriteBurst      = 24000

	// MaxUploadLinkWritesPerMinute keeps the configured rate in a range where the
	// per-request interval stays meaningful: `time.Minute / rate` collapses to a
	// zero duration for absurd values, which silently turns the limiter into a
	// no-op instead of a very permissive bound.
	MaxUploadLinkWritesPerMinute = 600000
)

// validateUploadLinkWriteLimit checks one rate/burst pair.
//
// Rate and burst are independent dimensions of a token bucket: rate is how fast
// tokens come back, burst is how many may be spent at once. There is no rule
// requiring one to exceed the other — 600/min with a burst of 1 is a coherent
// (if unfriendly) configuration. So the only things rejected here are values
// that cannot mean anything: a negative rate or burst, a rate so large the
// refill interval degenerates, and a burst of zero while the limiter is on,
// which would refuse every request.
//
// A zero burst is an error rather than being quietly filled in from the rate.
// Deriving it would make `burst: 0` mean something different from what an
// operator writing it plainly intends, and the two readings — "no burst" and
// "burst equal to the rate" — are far apart.
func validateUploadLinkWriteLimit(rateKey, burstKey string, perMinute, burst int) error {
	if perMinute < 0 {
		return fmt.Errorf("seafhttp.%s must be greater than or equal to zero (zero disables the limiter)", rateKey)
	}
	if perMinute > MaxUploadLinkWritesPerMinute {
		return fmt.Errorf("seafhttp.%s is %d, above the %d ceiling, where the refill interval degenerates and the limiter stops bounding anything",
			rateKey, perMinute, MaxUploadLinkWritesPerMinute)
	}
	if burst < 0 {
		return fmt.Errorf("seafhttp.%s must be greater than or equal to zero", burstKey)
	}
	if perMinute > 0 && burst == 0 {
		return fmt.Errorf("seafhttp.%s is zero while seafhttp.%s is %d; a bucket with no capacity refuses every request. Set a burst, or set the rate to zero to disable the limiter",
			burstKey, rateKey, perMinute)
	}
	return nil
}

// ServerConfig holds HTTP server settings
type ServerConfig struct {
	Port               string        `yaml:"port"`
	ReadTimeout        time.Duration `yaml:"read_timeout"`
	ReadHeaderTimeout  time.Duration `yaml:"read_header_timeout"`
	WriteTimeout       time.Duration `yaml:"write_timeout"`
	MaxUploadMB        int64         `yaml:"max_upload_mb"`
	TrustedProxies     []string      `yaml:"trusted_proxies"`
	Region             string        `yaml:"region"`
	URL                string        `yaml:"url"`
	DesktopCustomBrand string        `yaml:"desktop_custom_brand"`
	DesktopCustomLogo  string        `yaml:"desktop_custom_logo"`
	MobileFrontendPath string        `yaml:"mobile_frontend_path"` // Path to mobile frontend dist (default: ./mobile-frontend/dist)
}

// DatabaseConfig holds Cassandra connection settings
type DatabaseConfig struct {
	Hosts             []string       `yaml:"hosts"`
	Keyspace          string         `yaml:"keyspace"`
	Consistency       string         `yaml:"consistency"`
	SerialConsistency string         `yaml:"serial_consistency"`
	ProtoVersion      int            `yaml:"proto_version"` // CQL native protocol version (3, 4, or 5). Pinned to 4 by default to avoid the per-request keyspace flag introduced in v5.
	Timeout           time.Duration  `yaml:"timeout"`
	LocalDC           string         `yaml:"local_dc"`
	Username          string         `yaml:"username"`
	Password          string         `yaml:"password"`
	ReplicationClass  string         `yaml:"replication_class"`
	ReplicationFactor int            `yaml:"replication_factor"`
	ReplicationDCs    map[string]int `yaml:"replication_dcs"`
}

// StorageConfig holds storage backend settings
type StorageConfig struct {
	Mode            string                        `yaml:"mode"`
	DefaultClass    string                        `yaml:"default_class"`
	Classes         map[string]StorageClassConfig `yaml:"classes"`
	EndpointRegions map[string]string             `yaml:"endpoint_regions"` // hostname → region
	RegionClasses   map[string]RegionClassConfig  `yaml:"region_classes"`   // region → {hot, cold}

	// Legacy support (deprecated, use Classes instead)
	Backends map[string]BackendConfig `yaml:"backends"`
}

// StorageClassConfig holds configuration for a storage class (e.g., hot-s3-usa)
type StorageClassConfig struct {
	Label                string `yaml:"label"`                  // Optional user-facing display label
	Type                 string `yaml:"type"`                   // s3, glacier, disk
	Tier                 string `yaml:"tier"`                   // hot, cold
	Endpoint             string `yaml:"endpoint"`               // Primary endpoint
	Bucket               string `yaml:"bucket"`                 // S3 bucket name
	Region               string `yaml:"region"`                 // AWS region
	AccessKey            string `yaml:"access_key"`             // AWS access key (optional, can use env)
	SecretKey            string `yaml:"secret_key"`             // AWS secret key (optional, can use env)
	ServerSideEncryption string `yaml:"server_side_encryption"` // Optional SSE mode: AES256 or aws:kms
	SSEKMSKeyID          string `yaml:"sse_kms_key_id"`         // Optional KMS key ID/ARN when using aws:kms
	UsePathStyle         bool   `yaml:"use_path_style"`         // For MinIO compatibility
	FailoverClass        string `yaml:"failover_class"`         // Fallback class if this one is down
}

// RegionClassConfig maps a region to its hot and cold storage classes
type RegionClassConfig struct {
	Hot  string `yaml:"hot"`
	Cold string `yaml:"cold"`
}

// BackendConfig holds configuration for a storage backend (legacy, deprecated)
type BackendConfig struct {
	Label                string `yaml:"label"`                  // Optional user-facing display label
	Type                 string `yaml:"type"`                   // s3, glacier, filesystem
	Endpoint             string `yaml:"endpoint"`               // S3 endpoint
	Bucket               string `yaml:"bucket"`                 // S3 bucket name
	Region               string `yaml:"region"`                 // AWS region
	AccessKey            string `yaml:"access_key"`             // S3 access key (optional, can use env)
	SecretKey            string `yaml:"secret_key"`             // S3 secret key (optional, can use env)
	ServerSideEncryption string `yaml:"server_side_encryption"` // Optional SSE mode: AES256 or aws:kms
	SSEKMSKeyID          string `yaml:"sse_kms_key_id"`         // Optional KMS key ID/ARN when using aws:kms
	StorageClass         string `yaml:"storage_class"`          // S3 storage class
	Vault                string `yaml:"vault"`                  // Glacier vault name
	Path                 string `yaml:"path"`                   // Filesystem path
}

// AuthConfig holds authentication settings
type AuthConfig struct {
	DevMode              bool            `yaml:"dev_mode"`
	DevTokens            []DevTokenEntry `yaml:"dev_tokens"`
	OIDC                 OIDCConfig      `yaml:"oidc"`
	FirstSuperAdminEmail string          `yaml:"first_superadmin_email"` // Email of the first superadmin to seed in the platform org on first startup
	ShareLinkHMACKey     string          `yaml:"share_link_hmac_key"`    // HMAC key for share link password cookies
}

// DevTokenEntry holds a development token for testing
type DevTokenEntry struct {
	Token  string `yaml:"token"`
	UserID string `yaml:"user_id"`
	OrgID  string `yaml:"org_id"`
	Email  string `yaml:"email"` // Optional friendly email like "admin@sesamefs.local"
	Role   string `yaml:"role"`  // Optional role (superadmin, admin, user, readonly, guest)
}

// OIDCConfig holds OIDC provider settings
type OIDCConfig struct {
	// Enabled toggles OIDC authentication on/off
	Enabled bool `yaml:"enabled"`

	// Provider settings
	Issuer       string `yaml:"issuer"`        // OIDC provider URL (e.g., https://t-accounts.sesamedisk.com/)
	ClientID     string `yaml:"client_id"`     // OAuth client ID
	ClientSecret string `yaml:"client_secret"` // OAuth client secret

	// Redirect URIs - supports multiple for different environments
	// All URIs must also be registered with the OIDC provider
	RedirectURIs []string `yaml:"redirect_uris"`

	// Scopes to request from the OIDC provider
	Scopes []string `yaml:"scopes"`

	// Claim mappings for organization and role extraction
	OrgClaim   string `yaml:"org_claim"`   // Custom claim for organization/tenant ID (e.g., "tenant_id")
	RolesClaim string `yaml:"roles_claim"` // Custom claim for user roles (e.g., "roles")

	// User provisioning
	AutoProvision    bool   `yaml:"auto_provision"`     // Auto-create users on first login
	DefaultRole      string `yaml:"default_role"`       // Default role for new users (user, readonly, guest)
	DefaultOrgID     string `yaml:"default_org_id"`     // Default org for users without org claim
	DefaultOrgName   string `yaml:"default_org_name"`   // Default org name for new orgs
	AllowedOrgClaims string `yaml:"allowed_org_claims"` // Comma-separated list of allowed org claim values (empty = allow all)

	// Platform org settings
	PlatformOrgID         string `yaml:"platform_org_id"`          // UUID for the platform org (default: all zeros)
	PlatformOrgClaimValue string `yaml:"platform_org_claim_value"` // OIDC claim value that maps to the platform org

	// Session settings
	SessionTTL        time.Duration `yaml:"session_ttl"`         // How long web sessions last (default: 24h)
	APITokenTTL       time.Duration `yaml:"api_token_ttl"`       // How long desktop/mobile client tokens last (default: 180 days)
	RefreshTokenTTL   time.Duration `yaml:"refresh_token_ttl"`   // How long refresh tokens last (default: 7d)
	JWTSigningKey     string        `yaml:"jwt_signing_key"`     // Secret key for signing JWT session tokens
	AllowOfflineToken bool          `yaml:"allow_offline_token"` // Allow refresh tokens for offline access

	// Group & Department sync from OIDC claims
	GroupsClaim       string `yaml:"groups_claim"`              // Claim containing group memberships (e.g., "groups")
	DepartmentsClaim  string `yaml:"departments_claim"`         // Claim containing department memberships (e.g., "departments")
	SyncGroupsOnLogin bool   `yaml:"sync_groups_on_login"`      // Sync group membership on each login
	SyncDeptsOnLogin  bool   `yaml:"sync_departments_on_login"` // Sync department membership on each login
	FullSyncGroups    bool   `yaml:"full_sync_groups"`          // Remove from groups not in claims (vs additive only)
	FullSyncDepts     bool   `yaml:"full_sync_departments"`     // Remove from depts not in claims

	// Security settings
	RequirePKCE      bool          `yaml:"require_pkce"`       // Require PKCE for authorization flow
	ValidateAudience bool          `yaml:"validate_audience"`  // Validate token audience claim
	AllowedClockSkew time.Duration `yaml:"allowed_clock_skew"` // Allowed clock skew for token validation
}

// ChunkingConfig holds FastCDC chunking settings
type ChunkingConfig struct {
	Algorithm     string         `yaml:"algorithm"`      // fastcdc
	HashAlgorithm string         `yaml:"hash_algorithm"` // sha256
	Adaptive      AdaptiveConfig `yaml:"adaptive"`       // Adaptive chunk sizing
	Probe         ProbeConfig    `yaml:"probe"`          // Speed probe settings
	Retry         RetryConfig    `yaml:"retry"`          // Retry settings
}

// AdaptiveConfig holds adaptive chunk sizing settings
type AdaptiveConfig struct {
	Enabled       bool  `yaml:"enabled"`        // Enable adaptive chunking
	AbsoluteMin   int64 `yaml:"absolute_min"`   // 2 MB floor (terrible connections)
	AbsoluteMax   int64 `yaml:"absolute_max"`   // 256 MB ceiling (datacenter)
	InitialSize   int64 `yaml:"initial_size"`   // 16 MB starting point (if probe skipped)
	TargetSeconds int   `yaml:"target_seconds"` // Target seconds per chunk (8s default)
}

// ProbeConfig holds speed probe settings
type ProbeConfig struct {
	Size    int64         `yaml:"size"`    // Probe size in bytes (1 MB default)
	Timeout time.Duration `yaml:"timeout"` // Probe timeout (30s default)
}

// RetryConfig holds retry and timeout settings
type RetryConfig struct {
	ChunkTimeout     time.Duration `yaml:"chunk_timeout"`      // Per-chunk timeout (60s default)
	MaxRetries       int           `yaml:"max_retries"`        // Max retry attempts (5 default)
	ReduceOnTimeout  float64       `yaml:"reduce_on_timeout"`  // Reduce to this fraction on timeout (0.5)
	ReduceOnFailure  float64       `yaml:"reduce_on_failure"`  // Reduce to this fraction on failure (0.5)
	BackoffBase      time.Duration `yaml:"backoff_base"`       // Base backoff duration (1s default)
	BackoffMaxJitter time.Duration `yaml:"backoff_max_jitter"` // Max jitter to add (500ms default)
}

// VersioningConfig holds file versioning settings
type VersioningConfig struct {
	DefaultTTLDays int           `yaml:"default_ttl_days"`
	MinTTLDays     int           `yaml:"min_ttl_days"`
	GCInterval     time.Duration `yaml:"gc_interval"`
}

// GCConfig holds garbage collection settings
type GCConfig struct {
	Enabled             bool          `yaml:"enabled"`                // default: false; enable explicitly via GC_ENABLED=true
	WorkerInterval      time.Duration `yaml:"worker_interval"`        // default: 30s (queue poll)
	ScanInterval        time.Duration `yaml:"scan_interval"`          // default: 24h (full scan)
	BatchSize           int           `yaml:"batch_size"`             // default: 100 (items per tick)
	ReconcileBatchSize  int           `yaml:"reconcile_batch_size"`   // default: 256 (dirty orgs per reconcile pass, 0 = all)
	FailedItemsPageSize int           `yaml:"failed_items_page_size"` // default: 100 (admin DLQ listing page size)
	GracePeriod         time.Duration `yaml:"grace_period"`           // default: 1h (delay before S3 delete)
	DryRun              bool          `yaml:"dry_run"`                // default: false

	// Soft-delete grace periods (0 = cascade immediately)
	UserGraceDays      int `yaml:"user_grace_days"`      // default: 7 — days before deleted user is permanently purged
	OrgGraceDays       int `yaml:"org_grace_days"`       // default: 30 — days before deleted org is permanently purged
	TrashRetentionDays int `yaml:"trash_retention_days"` // default: 30 — days deleted libraries stay in trash
	AuditRetentionDays int `yaml:"audit_retention_days"` // default: 365 — days audit log entries are kept
}

// Load reads configuration from config.yaml and environment variables
func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Try to load config file
	configPath := getEnv("CONFIG_PATH", "config.yaml")
	if data, err := os.ReadFile(configPath); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Override with environment variables
	cfg.applyEnvOverrides()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// The per-session staging ceiling is effectively the maximum web-block file
	// size. When the block flow is enabled, the max file size is unlimited, and the
	// ceiling is left to derive (0), it falls back to a fixed operational default —
	// warn so operators know large web block uploads will be capped there.
	if cfg.WebUploads.EnableWebBlockUpload &&
		cfg.WebUploads.MaxStagedBytesPerSessionMB == 0 &&
		cfg.ResolvedMaxFileSizeMB() <= 0 {
		slog.Warn("web block upload: max_staged_bytes_per_session_mb is derive(0) but max file size is unlimited; "+
			"per-session staging (max web-block file size) falls back to a fixed default — set the knob to allow larger uploads",
			"fallback_bytes", DefaultMaxStagedBytesPerSession)
	}

	// A per-IP limiter is only per-IP if the IP is real. With no trusted proxies,
	// ClientIP() is the socket peer, which behind a reverse proxy is the proxy
	// itself — every anonymous uploader then shares one bucket and throttles the
	// others. This is a warning and not a validation error on purpose: running Go
	// directly with no proxy in front is a legitimate deployment, and there is no
	// way to tell the two apart from configuration alone.
	if cfg.SeafHTTP.UploadLinkWritesPerMinute > 0 && len(cfg.Server.TrustedProxies) == 0 {
		slog.Warn("upload-link write limiter is enabled but server.trusted_proxies is empty; "+
			"client IPs will be the direct socket peer. Behind a reverse proxy that collapses every "+
			"anonymous uploader into a single shared bucket — set SERVER_TRUSTED_PROXIES to the proxy CIDR",
			"upload_link_writes_per_minute", cfg.SeafHTTP.UploadLinkWritesPerMinute)
	}

	return cfg, nil
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:               ":8080",
			ReadTimeout:        0,                // No full-body read timeout — large uploads can take minutes
			ReadHeaderTimeout:  10 * time.Second, // Timeout for reading request headers only (Slowloris protection)
			WriteTimeout:       0,                // No write timeout — large downloads/zips can take minutes
			MaxUploadMB:        20480,            // 20 GB
			TrustedProxies:     nil,              // Secure default: do not trust forwarded client IP headers unless explicitly configured
			Region:             "",
			MobileFrontendPath: "./mobile-frontend/dist", // Mobile frontend build directory
		},
		Database: DatabaseConfig{
			Hosts:             []string{"localhost:9042"},
			Keyspace:          "sesamefs",
			Consistency:       "LOCAL_QUORUM",
			SerialConsistency: "SERIAL",
			ProtoVersion:      4,
			Timeout:           10 * time.Second,
			LocalDC:           "datacenter1",
			ReplicationClass:  "NetworkTopologyStrategy",
			ReplicationFactor: 1,
		},
		Storage: StorageConfig{
			Mode:         "",
			DefaultClass: "hot",
			Backends: map[string]BackendConfig{
				"hot": {
					Type:   "s3",
					Bucket: "sesamefs-blocks",
					Region: "us-east-1",
				},
			},
		},
		WebUploads: WebUploadsConfig{
			EnableUploadFolder:                 true,
			EnableResumableFileUpload:          true,
			ResumableChunkSizeMB:               8,
			WebBlockUploadBlockSizeMB:          8,
			MaxConcurrentBlockUploadsPerUser:   8,
			MaxUncommittedBlockSessionsPerUser: 8,
			MaxStagedBytesPerSessionMB:         0, // 0 = derive from max file size × 1.25
			MaxFileSizeMB:                      0,
			MaxFilesPerBatch:                   1000,
			SimultaneousUploads:                1,
		},
		Billing: BillingConfig{},
		Accounts: AccountsConfig{
			DisableOrgUserWrites: true,
		},
		Organizations: OrganizationsConfig{
			DefaultTemplate: "free",
			Templates: map[string]OrganizationTemplate{
				"free": DefaultFreeOrganizationTemplate(),
			},
		},
		Auth: AuthConfig{
			DevMode:          false,
			ShareLinkHMACKey: "sesamefs-default-share-hmac-key-change-me",
			DevTokens:        []DevTokenEntry{},
			OIDC: OIDCConfig{
				Enabled:          false, // Disabled by default, use dev tokens
				Scopes:           []string{"openid", "profile", "email"},
				AutoProvision:    false,
				DefaultRole:      "user",
				PlatformOrgID:    "00000000-0000-0000-0000-000000000000",
				SessionTTL:       24 * time.Hour,
				APITokenTTL:      180 * 24 * time.Hour, // 180 days — Seafile clients don't support token refresh
				RefreshTokenTTL:  7 * 24 * time.Hour,
				RequirePKCE:      true,
				ValidateAudience: true,
				AllowedClockSkew: 2 * time.Minute,
			},
		},
		Chunking: ChunkingConfig{
			Algorithm:     "fastcdc",
			HashAlgorithm: "sha256",
			Adaptive: AdaptiveConfig{
				Enabled:       true,
				AbsoluteMin:   2 * 1024 * 1024,   // 2 MB
				AbsoluteMax:   256 * 1024 * 1024, // 256 MB
				InitialSize:   16 * 1024 * 1024,  // 16 MB
				TargetSeconds: 8,                 // 8 seconds per chunk
			},
			Probe: ProbeConfig{
				Size:    1 * 1024 * 1024, // 1 MB probe
				Timeout: 30 * time.Second,
			},
			Retry: RetryConfig{
				ChunkTimeout:     60 * time.Second,
				MaxRetries:       5,
				ReduceOnTimeout:  0.5,
				ReduceOnFailure:  0.5,
				BackoffBase:      1 * time.Second,
				BackoffMaxJitter: 500 * time.Millisecond,
			},
		},
		Versioning: VersioningConfig{
			DefaultTTLDays: 90,
			MinTTLDays:     7,
			GCInterval:     24 * time.Hour,
		},
		GC: GCConfig{
			Enabled:             false,
			WorkerInterval:      30 * time.Second,
			ScanInterval:        24 * time.Hour,
			BatchSize:           100,
			ReconcileBatchSize:  256,
			FailedItemsPageSize: 100,
			GracePeriod:         1 * time.Hour,
			DryRun:              false,
			UserGraceDays:       7,
			OrgGraceDays:        30,
			TrashRetentionDays:  30,
			AuditRetentionDays:  365,
		},
		SeafHTTP: SeafHTTPConfig{
			TokenTTL:               1 * time.Hour,
			ZipMaxEntries:          100000,
			ZipMaxDepth:            64,
			ZipMaxBytes:            10 * 1024 * 1024 * 1024,
			ChunkedStagingMaxBytes: 0,
			SyncBlockMaxBytes:      DefaultSyncBlockMaxBytes,

			UploadLinkWritesPerMinute:      DefaultUploadLinkWritesPerMinute,
			UploadLinkWriteBurst:           DefaultUploadLinkWriteBurst,
			UploadLinkTokenWritesPerMinute: DefaultUploadLinkTokenWritesPerMinute,
			UploadLinkTokenWriteBurst:      DefaultUploadLinkTokenWriteBurst,
		},
		OnlyOffice: OnlyOfficeConfig{
			Enabled:           false,
			VerifyCertificate: true,
			ForceSave:         true,
			ViewExtensions:    []string{"doc", "docx", "ppt", "pptx", "xls", "xlsx", "odt", "fodt", "odp", "fodp", "ods", "fods"},
			EditExtensions:    []string{"docx", "pptx", "xlsx"},
			MaxDocumentBytes:  500 * 1024 * 1024,
			JWTTTLSeconds:     3600, // 1 hour
		},
		Elasticsearch: ElasticsearchConfig{
			Enabled: true,
			URLs:    []string{"http://localhost:9200"},
			Index:   "sesamefs-files",
		},
		Monitoring: MonitoringConfig{
			MetricsEnabled: true,
			MetricsPath:    "/metrics",
			HealthTimeout:  3 * time.Second,
		},
		FileView: FileViewConfig{
			MaxPreviewBytes:      1 * 1024 * 1024 * 1024,  // 1 GB for general files
			MaxVideoBytes:        10 * 1024 * 1024 * 1024, // 10 GB for videos (4K, long recordings)
			MaxTextBytes:         50 * 1024 * 1024,        // 50 MB for text files (prevent browser freeze)
			MaxIWorkPreviewBytes: 50 * 1024 * 1024,        // 50 MB for iWork previews
			PreviewExtensions:    defaultFileViewPreviewExtensions(),
		},
		EnforcementProfiles: map[string]EnforcementProfile{
			"hard": DefaultHardProfile(),
			"soft": DefaultSoftProfile(),
		},
	}
}

// applyEnvOverrides applies environment variable overrides
func (c *Config) applyEnvOverrides() {
	c.envOverrideErrors = nil

	// Server
	if v := os.Getenv("PORT"); v != "" {
		c.Server.Port = ":" + v
	}
	if v := os.Getenv("SERVER_PORT"); v != "" {
		c.Server.Port = v
	}
	if v := os.Getenv("SERVER_TRUSTED_PROXIES"); v != "" {
		c.Server.TrustedProxies = strings.Split(v, ",")
	}
	if v := os.Getenv("SERVER_REGION"); v != "" {
		c.Server.Region = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("SERVER_URL"); v != "" {
		c.Server.URL = strings.TrimSuffix(strings.TrimSpace(v), "/")
	}
	if v := os.Getenv("DESKTOP_CUSTOM_BRAND"); v != "" {
		c.Server.DesktopCustomBrand = strings.TrimSpace(v)
	}
	if v := os.Getenv("DESKTOP_CUSTOM_LOGO"); v != "" {
		c.Server.DesktopCustomLogo = strings.TrimSpace(v)
	}

	// CORS
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		c.CORS.AllowedOrigins = strings.Split(v, ",")
	}

	// Database
	if v := os.Getenv("CASSANDRA_HOSTS"); v != "" {
		c.Database.Hosts = strings.Split(v, ",")
	}
	if v := os.Getenv("CASSANDRA_KEYSPACE"); v != "" {
		c.Database.Keyspace = v
	}
	if v := os.Getenv("CASSANDRA_CONSISTENCY"); v != "" {
		c.Database.Consistency = strings.TrimSpace(v)
	}
	if v := os.Getenv("CASSANDRA_SERIAL_CONSISTENCY"); v != "" {
		c.Database.SerialConsistency = strings.TrimSpace(v)
	}
	if v := os.Getenv("CASSANDRA_PROTO_VERSION"); v != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			c.addEnvOverrideError("CASSANDRA_PROTO_VERSION must be an integer, got %q", v)
		} else {
			c.Database.ProtoVersion = parsed
		}
	}
	if v := os.Getenv("CASSANDRA_TIMEOUT"); v != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			c.addEnvOverrideError("CASSANDRA_TIMEOUT must be a duration, got %q", v)
		} else {
			c.Database.Timeout = parsed
		}
	}
	if v := os.Getenv("CASSANDRA_USERNAME"); v != "" {
		c.Database.Username = v
	}
	if v := os.Getenv("CASSANDRA_PASSWORD"); v != "" {
		c.Database.Password = v
	}
	if v := os.Getenv("CASSANDRA_LOCAL_DC"); v != "" {
		c.Database.LocalDC = v
	}
	if v := os.Getenv("CASSANDRA_REPLICATION_CLASS"); v != "" {
		c.Database.ReplicationClass = strings.TrimSpace(v)
	}
	if v := os.Getenv("CASSANDRA_REPLICATION_FACTOR"); v != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			c.addEnvOverrideError("CASSANDRA_REPLICATION_FACTOR must be an integer, got %q", v)
		} else {
			c.Database.ReplicationFactor = parsed
		}
	}
	if v := os.Getenv("CASSANDRA_REPLICATION_DCS"); v != "" {
		parsed, err := parseCassandraReplicationDCs(v)
		if err != nil {
			c.addEnvOverrideError("CASSANDRA_REPLICATION_DCS is invalid: %v", err)
		} else {
			c.Database.ReplicationDCs = parsed
		}
	}

	// Storage
	if v := os.Getenv("STORAGE_MODE"); v != "" {
		c.Storage.Mode = normalizeStorageMode(v)
	}
	if v := os.Getenv("S3_BUCKET"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.Bucket = v
			c.Storage.Backends["hot"] = hot
		}
	}
	if v := os.Getenv("S3_REGION"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.Region = v
			c.Storage.Backends["hot"] = hot
		}
	}
	if v := os.Getenv("S3_ENDPOINT"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.Endpoint = v
			c.Storage.Backends["hot"] = hot
		}
	}
	if v := os.Getenv("S3_SERVER_SIDE_ENCRYPTION"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.ServerSideEncryption = v
			c.Storage.Backends["hot"] = hot
		}
	}
	if v := os.Getenv("S3_SSE_KMS_KEY_ID"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.SSEKMSKeyID = v
			c.Storage.Backends["hot"] = hot
		}
	}
	if v := os.Getenv("S3_ACCESS_KEY_ID"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.AccessKey = strings.TrimSpace(v)
			c.Storage.Backends["hot"] = hot
		}
	}
	if v := os.Getenv("S3_SECRET_ACCESS_KEY"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.SecretKey = strings.TrimSpace(v)
			c.Storage.Backends["hot"] = hot
		}
	}
	applyStorageClassEnvOverrides(c)
	if c.storageMode() == "single" {
		c.Storage.DefaultClass = "hot"
	}

	// Billing
	if v := os.Getenv("BILLING_URL"); v != "" {
		c.Billing.URL = v
	}
	if v := os.Getenv("ACCOUNTS_DELETE_ACCOUNT_URL"); v != "" {
		c.Accounts.DeleteAccountURL = v
	}
	if v := os.Getenv("ACCOUNTS_ORG_USER_MANAGEMENT_URL"); v != "" {
		c.Accounts.OrgUserManagementURL = v
	}
	if v := os.Getenv("ACCOUNTS_DISABLE_ORG_USER_WRITES"); v != "" {
		c.Accounts.DisableOrgUserWrites = v == "true" || v == "1"
	}

	// Auth
	if v := os.Getenv("AUTH_DEV_MODE"); v != "" {
		c.Auth.DevMode = v == "true" || v == "1"
	}
	if v := os.Getenv("FIRST_SUPERADMIN_EMAIL"); v != "" {
		c.Auth.FirstSuperAdminEmail = v
	}
	if v := os.Getenv("SHARE_LINK_HMAC_KEY"); v != "" {
		c.Auth.ShareLinkHMACKey = v
	}

	// Web uploads
	if v := os.Getenv("WEB_UPLOADS_ENABLE_UPLOAD_FOLDER"); v != "" {
		c.WebUploads.EnableUploadFolder = v == "true" || v == "1"
	}
	if v := os.Getenv("WEB_UPLOADS_ENABLE_RESUMABLE_FILE_UPLOAD"); v != "" {
		c.WebUploads.EnableResumableFileUpload = v == "true" || v == "1"
	}
	if v := os.Getenv("WEB_UPLOADS_ENABLE_WEB_BLOCK_UPLOAD"); v != "" {
		c.WebUploads.EnableWebBlockUpload = v == "true" || v == "1"
	}
	if v := os.Getenv("WEB_UPLOADS_RESUMABLE_CHUNK_SIZE_MB"); v != "" {
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err != nil {
			c.addEnvOverrideError("WEB_UPLOADS_RESUMABLE_CHUNK_SIZE_MB must be an integer, got %q", v)
		} else {
			c.WebUploads.ResumableChunkSizeMB = i
		}
	}
	if v := os.Getenv("WEB_UPLOADS_BLOCK_UPLOAD_BLOCK_SIZE_MB"); v != "" {
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err != nil {
			c.addEnvOverrideError("WEB_UPLOADS_BLOCK_UPLOAD_BLOCK_SIZE_MB must be an integer, got %q", v)
		} else {
			c.WebUploads.WebBlockUploadBlockSizeMB = i
		}
	}
	if v := os.Getenv("WEB_UPLOADS_MAX_FILE_SIZE_MB"); v != "" {
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err != nil {
			c.addEnvOverrideError("WEB_UPLOADS_MAX_FILE_SIZE_MB must be an integer, got %q", v)
		} else {
			c.WebUploads.MaxFileSizeMB = i
		}
	}
	if v := os.Getenv("WEB_UPLOADS_MAX_FILES_PER_BATCH"); v != "" {
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
			c.addEnvOverrideError("WEB_UPLOADS_MAX_FILES_PER_BATCH must be an integer, got %q", v)
		} else {
			c.WebUploads.MaxFilesPerBatch = i
		}
	}
	if v := os.Getenv("WEB_UPLOADS_SIMULTANEOUS_UPLOADS"); v != "" {
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
			c.addEnvOverrideError("WEB_UPLOADS_SIMULTANEOUS_UPLOADS must be an integer, got %q", v)
		} else {
			c.WebUploads.SimultaneousUploads = i
		}
	}
	if v := os.Getenv("WEB_UPLOADS_MAX_CONCURRENT_BLOCK_UPLOADS_PER_USER"); v != "" {
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
			c.addEnvOverrideError("WEB_UPLOADS_MAX_CONCURRENT_BLOCK_UPLOADS_PER_USER must be an integer, got %q", v)
		} else {
			c.WebUploads.MaxConcurrentBlockUploadsPerUser = i
		}
	}
	if v := os.Getenv("WEB_UPLOADS_MAX_UNCOMMITTED_BLOCK_SESSIONS_PER_USER"); v != "" {
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
			c.addEnvOverrideError("WEB_UPLOADS_MAX_UNCOMMITTED_BLOCK_SESSIONS_PER_USER must be an integer, got %q", v)
		} else {
			c.WebUploads.MaxUncommittedBlockSessionsPerUser = i
		}
	}
	if v := os.Getenv("WEB_UPLOADS_MAX_STAGED_BYTES_PER_SESSION_MB"); v != "" {
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err != nil {
			c.addEnvOverrideError("WEB_UPLOADS_MAX_STAGED_BYTES_PER_SESSION_MB must be an integer, got %q", v)
		} else {
			c.WebUploads.MaxStagedBytesPerSessionMB = i
		}
	}

	// SeafHTTP
	if v := os.Getenv("SEAFHTTP_TOKEN_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.SeafHTTP.TokenTTL = d
		}
	}
	if v := os.Getenv("SEAFHTTP_ZIP_MAX_ENTRIES"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.SeafHTTP.ZipMaxEntries = i
		}
	}
	if v := os.Getenv("SEAFHTTP_ZIP_MAX_DEPTH"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.SeafHTTP.ZipMaxDepth = i
		}
	}
	if v := os.Getenv("SEAFHTTP_ZIP_MAX_BYTES"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.SeafHTTP.ZipMaxBytes = i
		}
	}
	// Unlike the neighbours above, a malformed value is reported rather than
	// silently dropped: falling back to the default would leave an operator who
	// deliberately raised the cap running the lower one with no signal.
	if v := os.Getenv("SEAFHTTP_SYNC_BLOCK_MAX_BYTES"); v != "" {
		i, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			c.addEnvOverrideError("SEAFHTTP_SYNC_BLOCK_MAX_BYTES is invalid: %v", err)
		} else {
			c.SeafHTTP.SyncBlockMaxBytes = i
		}
	}
	for _, override := range []struct {
		env    string
		target *int
	}{
		{"SEAFHTTP_UPLOAD_LINK_WRITES_PER_MINUTE", &c.SeafHTTP.UploadLinkWritesPerMinute},
		{"SEAFHTTP_UPLOAD_LINK_WRITE_BURST", &c.SeafHTTP.UploadLinkWriteBurst},
		{"SEAFHTTP_UPLOAD_LINK_TOKEN_WRITES_PER_MINUTE", &c.SeafHTTP.UploadLinkTokenWritesPerMinute},
		{"SEAFHTTP_UPLOAD_LINK_TOKEN_WRITE_BURST", &c.SeafHTTP.UploadLinkTokenWriteBurst},
	} {
		v := os.Getenv(override.env)
		if v == "" {
			continue
		}
		i, err := strconv.Atoi(v)
		if err != nil {
			c.addEnvOverrideError("%s is invalid: %v", override.env, err)
			continue
		}
		*override.target = i
	}
	// OIDC settings
	if v := os.Getenv("OIDC_ENABLED"); v != "" {
		c.Auth.OIDC.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("OIDC_ISSUER"); v != "" {
		c.Auth.OIDC.Issuer = v
	}
	if v := os.Getenv("OIDC_CLIENT_ID"); v != "" {
		c.Auth.OIDC.ClientID = v
	}
	if v := os.Getenv("OIDC_CLIENT_SECRET"); v != "" {
		c.Auth.OIDC.ClientSecret = v
	}
	if v := os.Getenv("OIDC_REDIRECT_URIS"); v != "" {
		c.Auth.OIDC.RedirectURIs = strings.Split(v, ",")
	}
	if v := os.Getenv("OIDC_SCOPES"); v != "" {
		c.Auth.OIDC.Scopes = strings.Split(v, ",")
	}
	if v := os.Getenv("OIDC_ORG_CLAIM"); v != "" {
		c.Auth.OIDC.OrgClaim = v
	}
	if v := os.Getenv("OIDC_ROLES_CLAIM"); v != "" {
		c.Auth.OIDC.RolesClaim = v
	}
	if v := os.Getenv("OIDC_AUTO_PROVISION"); v != "" {
		c.Auth.OIDC.AutoProvision = v == "true" || v == "1"
	}
	if v := os.Getenv("OIDC_DEFAULT_ROLE"); v != "" {
		c.Auth.OIDC.DefaultRole = v
	}
	if v := os.Getenv("OIDC_DEFAULT_ORG_ID"); v != "" {
		c.Auth.OIDC.DefaultOrgID = v
	}
	if v := os.Getenv("OIDC_DEFAULT_ORG_NAME"); v != "" {
		c.Auth.OIDC.DefaultOrgName = v
	}
	if v := os.Getenv("OIDC_ALLOWED_ORG_CLAIMS"); v != "" {
		c.Auth.OIDC.AllowedOrgClaims = v
	}
	if v := os.Getenv("OIDC_SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Auth.OIDC.SessionTTL = d
		}
	}
	if v := os.Getenv("OIDC_API_TOKEN_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Auth.OIDC.APITokenTTL = d
		}
	}
	if v := os.Getenv("OIDC_REFRESH_TOKEN_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Auth.OIDC.RefreshTokenTTL = d
		}
	}
	if v := os.Getenv("OIDC_JWT_SIGNING_KEY"); v != "" {
		c.Auth.OIDC.JWTSigningKey = v
	}
	if v := os.Getenv("OIDC_ALLOW_OFFLINE_TOKEN"); v != "" {
		c.Auth.OIDC.AllowOfflineToken = v == "true" || v == "1"
	}
	if v := os.Getenv("OIDC_REQUIRE_PKCE"); v != "" {
		c.Auth.OIDC.RequirePKCE = v == "true" || v == "1"
	}
	if v := os.Getenv("OIDC_VALIDATE_AUDIENCE"); v != "" {
		c.Auth.OIDC.ValidateAudience = v == "true" || v == "1"
	}
	if v := os.Getenv("OIDC_ALLOWED_CLOCK_SKEW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Auth.OIDC.AllowedClockSkew = d
		}
	}
	if v := os.Getenv("OIDC_PLATFORM_ORG_ID"); v != "" {
		c.Auth.OIDC.PlatformOrgID = v
	}
	if v := os.Getenv("OIDC_PLATFORM_ORG_CLAIM_VALUE"); v != "" {
		c.Auth.OIDC.PlatformOrgClaimValue = v
	}
	if v := os.Getenv("OIDC_GROUPS_CLAIM"); v != "" {
		c.Auth.OIDC.GroupsClaim = v
	}
	if v := os.Getenv("OIDC_DEPARTMENTS_CLAIM"); v != "" {
		c.Auth.OIDC.DepartmentsClaim = v
	}
	if v := os.Getenv("OIDC_SYNC_GROUPS_ON_LOGIN"); v != "" {
		c.Auth.OIDC.SyncGroupsOnLogin = v == "true" || v == "1"
	}
	if v := os.Getenv("OIDC_SYNC_DEPARTMENTS_ON_LOGIN"); v != "" {
		c.Auth.OIDC.SyncDeptsOnLogin = v == "true" || v == "1"
	}
	if v := os.Getenv("OIDC_FULL_SYNC_GROUPS"); v != "" {
		c.Auth.OIDC.FullSyncGroups = v == "true" || v == "1"
	}
	if v := os.Getenv("OIDC_FULL_SYNC_DEPARTMENTS"); v != "" {
		c.Auth.OIDC.FullSyncDepts = v == "true" || v == "1"
	}

	// OnlyOffice
	if v := os.Getenv("ONLYOFFICE_ENABLED"); v != "" {
		c.OnlyOffice.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("ONLYOFFICE_API_JS_URL"); v != "" {
		c.OnlyOffice.APIJSURL = v
	}
	if v := os.Getenv("ONLYOFFICE_JWT_SECRET"); v != "" {
		c.OnlyOffice.JWTSecret = v
	}
	if v := os.Getenv("ONLYOFFICE_MAX_DOCUMENT_BYTES"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.OnlyOffice.MaxDocumentBytes = i
		}
	}
	if v := os.Getenv("ONLYOFFICE_JWT_TTL_SECONDS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.OnlyOffice.JWTTTLSeconds = i
		}
	}

	// Elasticsearch
	if v := os.Getenv("ELASTICSEARCH_ENABLED"); v != "" {
		c.Elasticsearch.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("ELASTICSEARCH_URL"); v != "" {
		c.Elasticsearch.URLs = []string{v}
	}
	if v := os.Getenv("ELASTICSEARCH_INDEX"); v != "" {
		c.Elasticsearch.Index = v
	}

	// Monitoring
	if v := os.Getenv("METRICS_ENABLED"); v != "" {
		c.Monitoring.MetricsEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("METRICS_PATH"); v != "" {
		c.Monitoring.MetricsPath = v
	}
	if v := os.Getenv("HEALTH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Monitoring.HealthTimeout = d
		}
	}

	// FileView
	if v := os.Getenv("FILEVIEW_MAX_PREVIEW_BYTES"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.FileView.MaxPreviewBytes = i
		}
	}
	if v := os.Getenv("FILEVIEW_MAX_VIDEO_BYTES"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.FileView.MaxVideoBytes = i
		}
	}
	if v := os.Getenv("FILEVIEW_MAX_TEXT_BYTES"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.FileView.MaxTextBytes = i
		}
	}
	if v := os.Getenv("FILEVIEW_MAX_IWORK_PREVIEW_BYTES"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.FileView.MaxIWorkPreviewBytes = i
		}
	}
	if v := os.Getenv("FILEVIEW_PREVIEW_EXTENSIONS"); v != "" {
		c.FileView.PreviewExtensions = strings.Split(v, ",")
	}

	// GC settings
	if v := os.Getenv("GC_ENABLED"); v != "" {
		c.GC.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("GC_RECONCILE_BATCH_SIZE"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.GC.ReconcileBatchSize = i
		}
	}
	if v := os.Getenv("GC_FAILED_ITEMS_PAGE_SIZE"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.GC.FailedItemsPageSize = i
		}
	}

	// GC grace periods
	if v := os.Getenv("GC_USER_GRACE_DAYS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.GC.UserGraceDays = i
		}
	}
	if v := os.Getenv("GC_ORG_GRACE_DAYS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.GC.OrgGraceDays = i
		}
	}
	if v := os.Getenv("GC_TRASH_RETENTION_DAYS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.GC.TrashRetentionDays = i
		}
	}
	if v := os.Getenv("GC_AUDIT_RETENTION_DAYS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			c.GC.AuditRetentionDays = i
		}
	}
}

func normalizeStorageMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

// StorageMode returns the effective storage mode after applying inference.
func (c *Config) StorageMode() string {
	return c.storageMode()
}

func (c *Config) storageMode() string {
	mode := normalizeStorageMode(c.Storage.Mode)
	if mode == "single" || mode == "multi" {
		return mode
	}
	if c.shouldUseLegacySingleRegionStorage() {
		return "single"
	}
	return "multi"
}

func (c *Config) shouldUseLegacySingleRegionStorage() bool {
	if c == nil || strings.TrimSpace(c.Server.Region) != "" {
		return false
	}
	hot, ok := c.Storage.Backends["hot"]
	if !ok {
		return false
	}
	return strings.TrimSpace(hot.Bucket) != ""
}

func (c *Config) configuredStorageClass(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || c == nil {
		return false
	}
	if _, ok := c.Storage.Classes[name]; ok {
		return true
	}
	if backend, ok := c.Storage.Backends[name]; ok {
		if strings.EqualFold(backend.Type, "s3") {
			return strings.TrimSpace(backend.Bucket) != ""
		}
		return true
	}
	return false
}

func (c *Config) regionClassConfig(region string) (RegionClassConfig, bool) {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" || c == nil {
		return RegionClassConfig{}, false
	}
	for configuredRegion, regionConfig := range c.Storage.RegionClasses {
		if strings.EqualFold(configuredRegion, region) {
			return regionConfig, true
		}
	}
	return RegionClassConfig{}, false
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if len(c.envOverrideErrors) > 0 {
		return fmt.Errorf("invalid environment override: %s", strings.Join(c.envOverrideErrors, "; "))
	}
	if c.Server.Port == "" {
		return fmt.Errorf("server port is required")
	}
	if len(c.Database.Hosts) == 0 {
		return fmt.Errorf("at least one database host is required")
	}
	if c.Database.Keyspace == "" {
		return fmt.Errorf("database keyspace is required")
	}
	switch normalizedConsistency := normalizeCassandraConsistency(c.Database.Consistency); normalizedConsistency {
	case "ONE", "QUORUM", "LOCAL_QUORUM", "EACH_QUORUM", "ALL":
		c.Database.Consistency = normalizedConsistency
	default:
		return fmt.Errorf("database consistency must be ONE, QUORUM, LOCAL_QUORUM, EACH_QUORUM, or ALL")
	}
	switch normalizedSerialConsistency := normalizeCassandraSerialConsistency(c.Database.SerialConsistency); normalizedSerialConsistency {
	case "SERIAL", "LOCAL_SERIAL":
		c.Database.SerialConsistency = normalizedSerialConsistency
	default:
		return fmt.Errorf("database serial_consistency must be SERIAL or LOCAL_SERIAL")
	}
	if c.Database.Timeout <= 0 {
		return fmt.Errorf("database timeout must be greater than zero")
	}
	switch c.Database.ProtoVersion {
	case 3, 4, 5:
		// supported CQL native protocol versions
	default:
		return fmt.Errorf("database proto_version must be 3, 4, or 5")
	}
	switch normalizedClass := normalizeCassandraReplicationClass(c.Database.ReplicationClass); normalizedClass {
	case "SimpleStrategy":
		c.Database.ReplicationClass = normalizedClass
		if c.Database.ReplicationFactor <= 0 {
			return fmt.Errorf("database replication_factor must be greater than zero when using SimpleStrategy")
		}
		c.Database.ReplicationDCs = nil
	case "NetworkTopologyStrategy":
		c.Database.ReplicationClass = normalizedClass
		if len(c.Database.ReplicationDCs) == 0 {
			localDC := strings.TrimSpace(c.Database.LocalDC)
			if localDC == "" {
				return fmt.Errorf("database local_dc is required when using NetworkTopologyStrategy")
			}
			c.Database.ReplicationDCs = map[string]int{localDC: 1}
		}
		for dc, rf := range c.Database.ReplicationDCs {
			if strings.TrimSpace(dc) == "" {
				return fmt.Errorf("database replication_dcs contains an empty datacenter name")
			}
			if !isValidCassandraDCName(dc) {
				return fmt.Errorf("database replication_dcs.%s contains unsupported characters", dc)
			}
			if rf <= 0 {
				return fmt.Errorf("database replication_dcs.%s must be greater than zero", dc)
			}
		}
	default:
		return fmt.Errorf("database replication_class must be SimpleStrategy or NetworkTopologyStrategy")
	}
	defaultTemplate := strings.TrimSpace(c.Organizations.DefaultTemplate)
	if defaultTemplate == "" {
		defaultTemplate = "free"
	}
	if _, ok := c.Organizations.Templates[defaultTemplate]; !ok {
		return fmt.Errorf("organizations.default_template %q is not defined in organizations.templates", defaultTemplate)
	}
	const insecureDefaultHMACKey = "sesamefs-default-share-hmac-key-change-me"
	if !c.Auth.DevMode && (c.Auth.ShareLinkHMACKey == "" || c.Auth.ShareLinkHMACKey == insecureDefaultHMACKey) {
		return fmt.Errorf("auth.share_link_hmac_key must be set to a secure secret in production (set SHARE_LINK_HMAC_KEY env var)")
	}
	if !c.Auth.DevMode && !hasConfiguredStrings(c.CORS.AllowedOrigins) {
		return fmt.Errorf("cors.allowed_origins must contain at least one origin in production (set CORS_ALLOWED_ORIGINS env var)")
	}
	if !c.Auth.DevMode {
		for _, origin := range c.CORS.AllowedOrigins {
			if strings.TrimSpace(origin) == "*" {
				return fmt.Errorf("cors.allowed_origins contains wildcard \"*\" which is insecure in production; set specific origins via CORS_ALLOWED_ORIGINS env var")
			}
		}
	}
	for name, classCfg := range c.Storage.Classes {
		if err := validateStorageEncryptionConfig("storage.classes."+name, classCfg.Type, classCfg.ServerSideEncryption, classCfg.SSEKMSKeyID); err != nil {
			return err
		}
	}
	for name, backendCfg := range c.Storage.Backends {
		if err := validateStorageEncryptionConfig("storage.backends."+name, backendCfg.Type, backendCfg.ServerSideEncryption, backendCfg.SSEKMSKeyID); err != nil {
			return err
		}
	}
	explicitStorageMode := normalizeStorageMode(c.Storage.Mode)
	if explicitStorageMode != "" && explicitStorageMode != "single" && explicitStorageMode != "multi" {
		return fmt.Errorf("storage.mode must be one of: single, multi")
	}
	storageMode := c.storageMode()
	for region, regionConfig := range c.Storage.RegionClasses {
		if strings.TrimSpace(regionConfig.Hot) != "" && !c.configuredStorageClass(regionConfig.Hot) {
			return fmt.Errorf("storage.region_classes.%s.hot references unknown or unconfigured storage class %q", region, regionConfig.Hot)
		}
		if strings.TrimSpace(regionConfig.Cold) != "" && !c.configuredStorageClass(regionConfig.Cold) {
			return fmt.Errorf("storage.region_classes.%s.cold references unknown or unconfigured storage class %q", region, regionConfig.Cold)
		}
	}
	if !c.Auth.DevMode && storageMode == "multi" {
		if len(c.Storage.RegionClasses) == 0 {
			return fmt.Errorf("storage.mode=multi requires storage.region_classes to be configured")
		}
		if strings.TrimSpace(c.Server.Region) == "" {
			return fmt.Errorf("storage.mode=multi requires server.region to be set")
		}
		hot, ok := c.Storage.Backends["hot"]
		if ok && (strings.TrimSpace(hot.Bucket) != "" || strings.TrimSpace(hot.Region) != "" || strings.TrimSpace(hot.Endpoint) != "") {
			return fmt.Errorf("storage.mode=multi does not allow legacy hot backend overrides; remove S3_BUCKET/S3_REGION/S3_ENDPOINT")
		}
		regionConfig, ok := c.regionClassConfig(c.Server.Region)
		if !ok {
			return fmt.Errorf("server.region %q is not defined in storage.region_classes", c.Server.Region)
		}
		if strings.TrimSpace(regionConfig.Hot) == "" {
			return fmt.Errorf("storage.region_classes.%s.hot must be set for multi-region mode", c.Server.Region)
		}
		if c.Database.ReplicationClass != "NetworkTopologyStrategy" {
			return fmt.Errorf("storage.mode=multi requires database replication_class=NetworkTopologyStrategy")
		}
	}
	if !c.Auth.DevMode && storageMode == "single" {
		if strings.TrimSpace(c.Server.Region) != "" {
			return fmt.Errorf("storage.mode=single requires server.region to be empty")
		}
		hot, ok := c.Storage.Backends["hot"]
		if !ok || strings.TrimSpace(hot.Bucket) == "" {
			return fmt.Errorf("storage.backends.hot.bucket must be set for storage.mode=single")
		}
	}
	if c.WebUploads.ResumableChunkSizeMB <= 0 {
		return fmt.Errorf("web_uploads.resumable_chunk_size_mb must be greater than zero")
	}
	if c.WebUploads.WebBlockUploadBlockSizeMB <= 0 {
		return fmt.Errorf("web_uploads.web_block_upload_block_size_mb must be greater than zero")
	}
	if c.WebUploads.EnableWebBlockUpload &&
		c.EffectiveMaxStagedBytesPerSession() > 0 &&
		c.WebUploads.MaxConcurrentBlockUploadsPerUser <= 0 {
		return fmt.Errorf("web block upload with a staged-bytes cap requires web_uploads.max_concurrent_block_uploads_per_user to be greater than zero")
	}
	if c.WebUploads.MaxFilesPerBatch < 0 {
		return fmt.Errorf("web_uploads.max_files_per_batch must be zero or greater")
	}
	if c.WebUploads.SimultaneousUploads <= 0 {
		return fmt.Errorf("web_uploads.simultaneous_uploads must be greater than zero")
	}
	if c.OnlyOffice.Enabled && strings.TrimSpace(c.OnlyOffice.JWTSecret) == "" {
		return fmt.Errorf("onlyoffice.jwt_secret must be set when onlyoffice.enabled is true")
	}
	if c.OnlyOffice.MaxDocumentBytes <= 0 {
		return fmt.Errorf("onlyoffice.max_document_bytes must be greater than zero")
	}
	if c.OnlyOffice.JWTTTLSeconds < 300 {
		return fmt.Errorf("onlyoffice.jwt_ttl_seconds must be at least 300 (5 minutes)")
	}
	if c.OnlyOffice.JWTTTLSeconds > 28800 {
		return fmt.Errorf("onlyoffice.jwt_ttl_seconds must be at most 28800 (8 hours)")
	}
	if c.SeafHTTP.ZipMaxEntries <= 0 {
		return fmt.Errorf("seafhttp.zip_max_entries must be greater than zero")
	}
	if c.SeafHTTP.ZipMaxEntries > 1000000 {
		return fmt.Errorf("seafhttp.zip_max_entries must be less than or equal to 1000000")
	}
	if c.SeafHTTP.ZipMaxDepth <= 0 {
		return fmt.Errorf("seafhttp.zip_max_depth must be greater than zero")
	}
	if c.SeafHTTP.ZipMaxDepth > 256 {
		return fmt.Errorf("seafhttp.zip_max_depth must be less than or equal to 256")
	}
	if c.SeafHTTP.ZipMaxBytes <= 0 {
		return fmt.Errorf("seafhttp.zip_max_bytes must be greater than zero")
	}
	if c.SeafHTTP.ZipMaxBytes > 100*1024*1024*1024 {
		return fmt.Errorf("seafhttp.zip_max_bytes must be less than or equal to 107374182400")
	}
	if c.SeafHTTP.ChunkedStagingMaxBytes < 0 {
		return fmt.Errorf("seafhttp.chunked_staging_max_bytes must be greater than or equal to zero")
	}
	// Zero is rejected here rather than treated as "unlimited". An unbounded
	// PutBlock body is the defect this cap exists for, so no configuration may
	// restore it — which is the opposite of chunked_staging_max_bytes above,
	// where zero legitimately means "guard disabled".
	if c.SeafHTTP.SyncBlockMaxBytes <= 0 {
		return fmt.Errorf("seafhttp.sync_block_max_bytes must be greater than zero (an unbounded sync block body is not a supported configuration)")
	}
	if c.SeafHTTP.SyncBlockMaxBytes > MaxSyncBlockMaxBytes {
		return fmt.Errorf("seafhttp.sync_block_max_bytes is %d, above the %d ceiling; this route carries %d-byte blocks, so a larger value is almost certainly derived from the web uploader's chunk ceiling by mistake",
			c.SeafHTTP.SyncBlockMaxBytes, MaxSyncBlockMaxBytes, 8*1024*1024)
	}
	if err := validateUploadLinkWriteLimit(
		"upload_link_writes_per_minute", "upload_link_write_burst",
		c.SeafHTTP.UploadLinkWritesPerMinute, c.SeafHTTP.UploadLinkWriteBurst,
	); err != nil {
		return err
	}
	if err := validateUploadLinkWriteLimit(
		"upload_link_token_writes_per_minute", "upload_link_token_write_burst",
		c.SeafHTTP.UploadLinkTokenWritesPerMinute, c.SeafHTTP.UploadLinkTokenWriteBurst,
	); err != nil {
		return err
	}
	normalizedPreviewExtensions, err := normalizeFileViewPreviewExtensions(c.FileView.PreviewExtensions)
	if err != nil {
		return err
	}
	c.FileView.PreviewExtensions = normalizedPreviewExtensions
	normalizedTrustedProxies, err := normalizeTrustedProxies(c.Server.TrustedProxies)
	if err != nil {
		return err
	}
	c.Server.TrustedProxies = normalizedTrustedProxies
	if c.Auth.OIDC.Enabled && !hasConfiguredStrings(c.Auth.OIDC.RedirectURIs) {
		return fmt.Errorf("auth.oidc.redirect_uris must contain at least one redirect URI when OIDC is enabled")
	}
	return nil
}

func normalizeCassandraReplicationClass(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "NetworkTopologyStrategy"
	case "simplestrategy":
		return "SimpleStrategy"
	case "networktopologystrategy":
		return "NetworkTopologyStrategy"
	default:
		return strings.TrimSpace(raw)
	}
}

func normalizeCassandraConsistency(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "local_quorum", "localquorum":
		return "LOCAL_QUORUM"
	case "one":
		return "ONE"
	case "quorum":
		return "QUORUM"
	case "each_quorum", "eachquorum":
		return "EACH_QUORUM"
	case "all":
		return "ALL"
	default:
		return strings.TrimSpace(raw)
	}
}

func normalizeCassandraSerialConsistency(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "serial":
		return "SERIAL"
	case "local_serial", "localserial":
		return "LOCAL_SERIAL"
	default:
		return strings.TrimSpace(raw)
	}
}

func (c *Config) addEnvOverrideError(format string, args ...any) {
	if c == nil {
		return
	}
	c.envOverrideErrors = append(c.envOverrideErrors, fmt.Sprintf(format, args...))
}

func parseCassandraReplicationDCs(raw string) (map[string]int, error) {
	result := make(map[string]int)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		dc, rfText, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("invalid replication dc entry %q", entry)
		}
		dc = strings.TrimSpace(dc)
		rfText = strings.TrimSpace(rfText)
		rf, err := strconv.Atoi(rfText)
		if dc == "" || !isValidCassandraDCName(dc) || err != nil {
			return nil, fmt.Errorf("invalid replication dc entry %q", entry)
		}
		result[dc] = rf
	}
	return result, nil
}

func isValidCassandraDCName(name string) bool {
	return cassandraDCNamePattern.MatchString(strings.TrimSpace(name))
}

func validateStorageEncryptionConfig(scope, backendType, mode, kmsKeyID string) error {
	mode = strings.TrimSpace(mode)
	kmsKeyID = strings.TrimSpace(kmsKeyID)
	if mode == "" && kmsKeyID == "" {
		return nil
	}
	if strings.TrimSpace(backendType) != "s3" {
		return fmt.Errorf("%s server-side encryption is only supported for s3 backends", scope)
	}
	switch mode {
	case "AES256", "aws:kms":
	case "":
		return fmt.Errorf("%s.sse_kms_key_id requires storage encryption mode aws:kms", scope)
	default:
		return fmt.Errorf("%s.server_side_encryption must be one of: AES256, aws:kms", scope)
	}
	if kmsKeyID != "" && mode != "aws:kms" {
		return fmt.Errorf("%s.sse_kms_key_id requires storage encryption mode aws:kms", scope)
	}
	return nil
}

func normalizeTrustedProxies(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if ip := net.ParseIP(value); ip == nil {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return nil, fmt.Errorf("server.trusted_proxies contains invalid IP or CIDR %q", raw)
			}
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func hasConfiguredStrings(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func applyStorageClassEnvOverrides(c *Config) {
	if c == nil {
		return
	}
	defaultAccessKey := strings.TrimSpace(os.Getenv("S3_ACCESS_KEY_ID"))
	defaultSecretKey := strings.TrimSpace(os.Getenv("S3_SECRET_ACCESS_KEY"))
	for name, classCfg := range c.Storage.Classes {
		if value := storageClassEnvValue(name, "BUCKET"); value != "" {
			classCfg.Bucket = value
		}
		if value := storageClassEnvValue(name, "REGION"); value != "" {
			classCfg.Region = value
		}
		if value := storageClassEnvValue(name, "ENDPOINT"); value != "" {
			classCfg.Endpoint = value
		}
		if value := storageClassEnvValue(name, "ACCESS_KEY_ID"); value != "" {
			classCfg.AccessKey = value
		} else if strings.TrimSpace(classCfg.AccessKey) == "" {
			classCfg.AccessKey = defaultAccessKey
		}
		if value := storageClassEnvValue(name, "SECRET_ACCESS_KEY"); value != "" {
			classCfg.SecretKey = value
		} else if strings.TrimSpace(classCfg.SecretKey) == "" {
			classCfg.SecretKey = defaultSecretKey
		}
		if value := storageClassEnvValue(name, "SERVER_SIDE_ENCRYPTION"); value != "" {
			classCfg.ServerSideEncryption = value
		}
		if value := storageClassEnvValue(name, "SSE_KMS_KEY_ID"); value != "" {
			classCfg.SSEKMSKeyID = value
		}
		c.Storage.Classes[name] = classCfg
	}
}

func storageClassEnvPrefix(name string) string {
	var builder strings.Builder
	builder.Grow(len(name))
	lastUnderscore := false
	for _, ch := range strings.ToUpper(strings.TrimSpace(name)) {
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			builder.WriteRune(ch)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(builder.String(), "_")
}

func storageClassEnvVar(name, suffix string) string {
	prefix := storageClassEnvPrefix(name)
	if prefix == "" {
		return suffix
	}
	return "S3_CLASS_" + prefix + "_" + suffix
}

func storageClassEnvValue(name, suffix string) string {
	return strings.TrimSpace(os.Getenv(storageClassEnvVar(name, suffix)))
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// getEnvInt returns environment variable as int or default
func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}
