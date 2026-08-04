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
	DownloadAdmission   DownloadAdmissionConfig       `yaml:"download_admission"`
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
	MaxPreviewBytes      int64 `yaml:"max_preview_bytes"`       // Maximum file size for general inline preview (default: 1GB)
	MaxVideoBytes        int64 `yaml:"max_video_bytes"`         // Maximum file size for video preview (default: 10GB)
	MaxTextBytes         int64 `yaml:"max_text_bytes"`          // Maximum file size for text preview (default: 50MB)
	MaxIWorkPreviewBytes int64 `yaml:"max_iwork_preview_bytes"` // Maximum size for extracted iWork preview (default: 50MB)
	// MaxIWorkSourceBytes caps the iWork *source* document, which the preview
	// branch materialises whole. D6 measured the peak at ~4x the source for a
	// plaintext library and ~6x when encrypted — a 256 MiB document costs 1.5 GiB
	// — so the general 1 GiB preview limit is not an in-memory budget for this
	// path. Sizing max_active_raw for it instead would throttle ordinary raw
	// streams, which cost one block, by two orders of magnitude. Default 32 MiB.
	MaxIWorkSourceBytes int64    `yaml:"max_iwork_source_bytes"`
	PreviewExtensions   []string `yaml:"preview_extensions"` // Extensions that should route to the frontend preview shell
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

// DownloadAdmissionConfig holds the process-local download admission contract.
// D1 ships this disabled with zero values. Positive values are for measured
// deployments after the later D phases, not defaults that claim safe capacity.
type DownloadAdmissionConfig struct {
	Enabled                bool          `yaml:"enabled"`
	MaxActivePerNode       int           `yaml:"max_active_per_node"`
	MaxActivePerAuthUser   int           `yaml:"max_active_per_auth_user"`
	MaxActivePerLinkSource int           `yaml:"max_active_per_link_source"`
	MaxActivePerClientLink int           `yaml:"max_active_per_client_link"`
	MaxWaitersPerIdentity  int           `yaml:"max_waiters_per_identity"`
	MaxWaitersPerNode      int           `yaml:"max_waiters_per_node"`
	AdmissionWait          time.Duration `yaml:"admission_wait"`
	PreparationDeadline    time.Duration `yaml:"preparation_deadline"`
	IdleWriteTimeout       time.Duration `yaml:"idle_write_timeout"`
	RetryAfter             time.Duration `yaml:"retry_after"`
	MaxActiveBlock         int           `yaml:"max_active_block"`
	MaxActiveFile          int           `yaml:"max_active_file"`
	MaxActiveRaw           int           `yaml:"max_active_raw"`
	MaxActiveHistory       int           `yaml:"max_active_history"`
	MaxActiveLinkRaw       int           `yaml:"max_active_link_raw"`
	MaxActiveZIP           int           `yaml:"max_active_zip"`
	MaxActiveLinkInline    int           `yaml:"max_active_link_inline"`
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
	// The default is sized with ample headroom over the official client's 4 MiB
	// CDC maximum and SesameFS's related 8 MiB server-side split, not from the web
	// uploader's adaptive ceiling. 16 MiB also leaves room for cipher padding while
	// cutting the previous 257 MiB buffered-body bound by approximately 16x.
	// `sync_put_block_body_bytes` exists to validate the choice with real traffic.
	//
	// It is NOT an aggregate bound on its own. N concurrent uploads cost N x this
	// value; the in-flight caps below supply the missing N (subcontract B / X10
	// of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01).
	//
	// Zero is rejected rather than meaning "unlimited": an unbounded body on this
	// route is the defect (F12), so there is no configuration that restores it.
	SyncBlockMaxBytes int64 `yaml:"sync_block_max_bytes"`

	// SyncBlockMaxInflightPerNode caps concurrent block uploads that have been
	// admitted past the gate on this process, and is therefore the term that
	// turns SyncBlockMaxBytes into an actual memory bound:
	//
	//	buffered bodies <= SyncBlockMaxInflightPerNode x SyncBlockMaxBytes
	//
	// plus read-growth, hashing and HTTP overhead. It is a memory-budget
	// setting, not a throughput one: derive it from
	// (memory allowed for block PUT) / (measured peak per request), not from a
	// desired request rate.
	//
	// Zero disables the cap and gives up the aggregate bound, which is the
	// pre-2026-07-30 behaviour. Unlike sync_block_max_bytes that is allowed,
	// because an operator who fronts this route with an external admission
	// controller has a legitimate reason to turn the process-local one off.
	SyncBlockMaxInflightPerNode int `yaml:"sync_block_max_inflight_per_node"`

	// SyncBlockMemoryBudgetBytes is the process memory budget assigned to admitted
	// block PUTs. Validation rejects a node cap whose measured design cost exceeds
	// this budget, including when SyncBlockMaxBytes is raised.
	SyncBlockMemoryBudgetBytes int64 `yaml:"sync_block_memory_budget_bytes"`

	// SyncBlockMaxInflightPerUser caps concurrent block uploads for one
	// (org, user) on this process. It exists for fairness, not for memory: the
	// node cap above already bounds total memory, and this one stops a single
	// identity from consuming all of it.
	//
	// It must leave room for one honest desktop. The official client runs up to
	// 5 sync tasks x 3 block threads, so ~15 concurrent PutBlock requests from a
	// single well-behaved client is normal traffic. All of a user's devices share
	// this budget, since sync auth resolves every device token to the same user.
	//
	// Requests over the cap are not refused outright — they wait up to
	// SyncBlockAdmissionWait first. Zero disables the cap.
	SyncBlockMaxInflightPerUser int `yaml:"sync_block_max_inflight_per_user"`

	// SyncBlockAdmissionWait is how long a block upload may queue for an
	// admission before being answered 503 + Retry-After.
	//
	// The wait is what makes a cap near the honest-client concurrency safe: a
	// burst is slowed instead of failed. It must stay below the sync client's own
	// request timeout, or the client gives up before the server answers and the
	// 503 contract never comes into play.
	//
	// The route answers 503, never 429: the official client treats 502/503/504 as
	// transient network errors and retries them, but has no 429 handling, so a
	// 429 here surfaces as a hard sync failure. Zero means "refuse immediately
	// when full", which is the behaviour the bounded wait exists to avoid; it is
	// allowed but is not the recommended setting.
	SyncBlockAdmissionWait time.Duration `yaml:"sync_block_admission_wait"`

	// SyncBlockMaxWaitersPerUser and SyncBlockMaxWaitersPerNode bound requests
	// parked for the gates above. Zero permits no waiting: a request that cannot
	// acquire immediately is rejected with 503 before a timer is allocated.
	SyncBlockMaxWaitersPerUser int `yaml:"sync_block_max_waiters_per_user"`
	SyncBlockMaxWaitersPerNode int `yaml:"sync_block_max_waiters_per_node"`

	// SyncBlockAdmittedLifetime is the processing deadline for body read, hashing,
	// and storage. The connection deadline is a hard body-read bound. Context-aware
	// backends cancel immediately; an already-running contextless Cassandra query
	// can finish within the separately required finite DB timeout.
	SyncBlockAdmittedLifetime time.Duration `yaml:"sync_block_admitted_lifetime"`

	// CheckBlocksMaxIDs bounds how many block ids one
	// POST /seafhttp/repo/:repo_id/check-blocks may carry. It used to be a
	// hardcoded 100k chosen as a safe *parse* bound, and that is all it ever was:
	// the parser stops allocating, while the accepted list still drives one
	// Cassandra mapping read per legacy SHA-1 id plus one object-store existence
	// check per unique block (X11 / subcontract C).
	//
	// The default keeps the historical value because it is a client-compatibility
	// bound, not a measured one: a large initial sync posts the block list of one
	// commit, the desktop client does not re-batch after a 413, and no capture of
	// real traffic exists yet to justify a lower number.
	// `sync_check_blocks_ids_per_request` is the instrument that will justify one;
	// it measures parsed lists, including malformed traffic that reached the parser.
	//
	// The validation ceiling is that same 100k, so this knob can only be lowered.
	// Raising it would re-open the amplification the cap exists to bound.
	CheckBlocksMaxIDs int `yaml:"check_blocks_max_ids"`

	// CheckBlocksMaxInflightPerNode and CheckBlocksMaxInflightPerUser cap
	// concurrently admitted check-blocks requests on this process.
	//
	// They are deliberately NOT the block-PUT caps and NOT the same limiter
	// instance. The scarce resource here is metadata work — Cassandra point
	// reads and object-store existence checks — not buffered body memory, so the
	// budget is different in kind and in size. Sharing one instance would let a
	// check-blocks storm consume the slots that bound block-upload memory, and
	// vice versa.
	//
	// The node cap is the term that turns the per-request work bound into an
	// aggregate one:
	//
	//	concurrent metadata lookups from this route
	//		<= CheckBlocksMaxInflightPerNode x CheckBlocksLookupFanout
	//
	// A zero node cap disables the aggregate guard and is only valid when the
	// per-user cap is also zero. A node cap may be used without a per-user cap,
	// but a per-user cap alone cannot claim to bound aggregate work.
	CheckBlocksMaxInflightPerNode int `yaml:"check_blocks_max_inflight_per_node"`
	CheckBlocksMaxInflightPerUser int `yaml:"check_blocks_max_inflight_per_user"`

	// CheckBlocksMaxWaitersPerUser and CheckBlocksMaxWaitersPerNode bound
	// requests parked for the gates above, exactly as their block-PUT
	// counterparts do. Zero permits no waiting.
	CheckBlocksMaxWaitersPerUser int `yaml:"check_blocks_max_waiters_per_user"`
	CheckBlocksMaxWaitersPerNode int `yaml:"check_blocks_max_waiters_per_node"`

	// CheckBlocksAdmissionWait is how long a check-blocks request may queue for
	// an admission before being answered 503 + Retry-After — never 429, for the
	// same client-contract reason as the block route: this is the same desktop
	// sync client, which retries 502/503/504 and has no 429 handling.
	CheckBlocksAdmissionWait time.Duration `yaml:"check_blocks_admission_wait"`

	// CheckBlocksAdmittedLifetime is the processing deadline for the body read,
	// the id parse and every metadata lookup an admitted request performs. It is
	// what makes a slot recoverable: without it a large accepted list would hold
	// its admission for as long as the lookups take, and a client that vanished
	// mid-request would keep driving Cassandra to completion.
	CheckBlocksAdmittedLifetime time.Duration `yaml:"check_blocks_admitted_lifetime"`

	// CheckBlocksLookupFanout bounds the per-request concurrency of both metadata
	// phases: the legacy SHA-1 mapping resolution and the canonical existence
	// check.
	//
	// It is a two-sided knob, which is why it is small. Raising it shortens how
	// long one request holds its admission, and raises the instantaneous load one
	// request can put on Cassandra and the object store. Validation therefore
	// bounds the *product* with the node cap rather than this value alone.
	CheckBlocksLookupFanout int `yaml:"check_blocks_lookup_fanout"`

	// UploadLinkWritesPerMinute bounds anonymous writes through a public upload
	// link, keyed on client IP *and* stable public-link source. This is
	// subcontract A1 of
	// ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01: the upload-link *routes* under
	// /api/v2.1 already carry a per-IP limiter, but the seafhttp upload endpoint
	// they hand off to carries none, so the actual write was unbounded.
	//
	// It applies ONLY to tokens whose Source is "link". The same endpoint serves
	// authenticated web uploads, and a limiter applied by URL rather than by token
	// origin would throttle those too — they are not the anonymous surface this
	// bounds. Zero disables it.
	//
	// Keyed on (IP, stable link source) rather than IP alone because a single address is
	// routinely a whole office, school or mobile carrier NAT. Keyed on IP alone,
	// one person uploading through one link would throttle everyone else behind
	// that address who is using a *different* link.
	//
	// This is a request-rate bound, not a cost bound: a 20 KiB photo and an 8 MiB
	// chunk each spend one token. The separate in-flight caps close A2.
	UploadLinkWritesPerMinute int `yaml:"upload_link_writes_per_minute"`

	// UploadLinkWriteBurst is the token-bucket burst for the limiter above. A
	// browser uploading one file issues several requests back to back (chunks),
	// and a dropped folder issues one per small file, so the burst is what decides
	// whether ordinary use ever meets the limiter at all.
	UploadLinkWriteBurst int `yaml:"upload_link_write_burst"`

	// UploadLinkSourceWritesPerMinute bounds writes against a single stable public
	// link across *all* client IPs on this process. Seafhttp token remints retain
	// the source key. The per-client bucket above cannot
	// see a leaked upload URL being hit from many addresses at once; this one can.
	//
	// It is deliberately far above any plausible legitimate figure, because a
	// shared link legitimately serves many people at once — a classroom uploading
	// to one link is normal traffic, not abuse. Zero disables it.
	//
	// Both upload-link limiters are process-local capacity guards, not
	// cluster-global quotas: each node holds its own buckets, so aggregate
	// admission across the fleet scales with the number of nodes.
	UploadLinkSourceWritesPerMinute int `yaml:"upload_link_source_writes_per_minute"`

	// UploadLinkSourceWriteBurst is the token-bucket burst for the per-link limit.
	UploadLinkSourceWriteBurst int `yaml:"upload_link_source_write_burst"`

	// UploadLinkMaxInflightPerSource caps concurrent anonymous writes for one
	// stable public-link identity on this process. UploadLinkMaxInflightPerNode
	// separately protects total process capacity across all links. Both are
	// process-local, non-blocking guards; zero disables that cap.
	UploadLinkMaxInflightPerSource int `yaml:"upload_link_max_inflight_per_source"`
	UploadLinkMaxInflightPerNode   int `yaml:"upload_link_max_inflight_per_node"`
}

// Sync block body bounds. MaxSyncBlockMaxBytes is a validation ceiling, not a
// default: a value above it is almost certainly derived from the web uploader's
// chunk ceiling by mistake, which is exactly how the 257 MiB bound arose.
const (
	DefaultSyncBlockMaxBytes int64 = 16 * 1024 * 1024
	MaxSyncBlockMaxBytes     int64 = 64 * 1024 * 1024

	// Download admission sizes are derived from the D6 measurements. Encrypted
	// prefetch keeps the current and next block plus the encrypted source, so the
	// 4.5x block-size figure is the conservative design cost used for validation.
	DefaultDownloadAdmissionMemoryBudgetBytes     int64 = 2 * 1024 * 1024 * 1024
	DownloadAdmissionEncryptedPeakNumerator             = 9
	DownloadAdmissionEncryptedPeakDenominator           = 2
	DownloadAdmissionIWorkEncryptedPeakMultiplier       = 6

	// Sync block in-flight admission (subcontract B / X10).
	//
	// The ceilings are not the safety mechanism — the measured default is. They
	// exist to reject values that cannot have come from a memory budget: a node
	// cap in the thousands only makes sense on a machine with tens of gigabytes
	// to spare for this one route, and an admission wait longer than any sync
	// client's request timeout means the client gives up before the server ever
	// answers, so the 503 contract never applies.
	MaxSyncBlockMaxInflightPerNode = 4096
	MaxSyncBlockMaxInflightPerUser = 4096
	MaxSyncBlockAdmissionWait      = 2 * time.Minute
	MaxSyncBlockMaxWaitersPerNode  = 65536
	MaxSyncBlockMaxWaitersPerUser  = 4096
	MaxSyncBlockMemoryBudgetBytes  = int64(1 << 40) // 1 TiB operator ceiling

	// The admitted processing deadline needs a ceiling for the same reason it needs
	// a floor. An override of hours or days would satisfy "greater than zero" while
	// making the guard ineffective in practice. 30 minutes is far above any legitimate
	// single-block PUT (a 64 MiB body plus storage) and far below "effectively
	// forever".
	MaxSyncBlockAdmittedLifetime = 30 * time.Minute

	// The node default is derived from the complete admitted lifetime, not just
	// the buffered-body plateau. Clean-process trials sample correlated RSS,
	// cgroup and heap from all bodies resident through hash, Cassandra and object
	// storage drain. The earlier 64 MiB design failed that stronger probe; 80 MiB
	// is the rounded design cost. 24 admissions reserve 1.875 GiB of the explicit
	// 2 GiB route budget and leave 128 MiB headroom.
	//
	// Validation scales the design cost linearly when SyncBlockMaxBytes changes,
	// so the cap and body size cannot silently outgrow the configured budget.
	DefaultSyncBlockMaxInflightPerNode = 24
	DefaultSyncBlockMemoryBudgetBytes  = int64(2 * 1024 * 1024 * 1024)
	DefaultSyncBlockDesignBytes        = int64(80 * 1024 * 1024)

	// The per-user default is a fairness split, not a memory one. It sits just
	// above the ~15 concurrent PutBlock requests one official client can issue
	// (5 sync tasks x 3 block threads) so a single honest desktop is never
	// queued by its own budget, while two identities can still fill the node.
	DefaultSyncBlockMaxInflightPerUser = 16

	// The wait has to be long enough to absorb a burst and short enough that the
	// client is still listening when the 503 arrives. See the dated subcontract B
	// note in docs/KNOWN_ISSUES.md for the fault-injection run behind this value.
	DefaultSyncBlockAdmissionWait     = 10 * time.Second
	DefaultSyncBlockMaxWaitersPerUser = 16
	DefaultSyncBlockMaxWaitersPerNode = 128
	DefaultSyncBlockAdmittedLifetime  = 5 * time.Minute

	// check-blocks admission and work bounds (subcontract C / X11).
	//
	// The node default is a metadata-work budget, not a memory one. Eight
	// admitted requests at a fan-out of eight is at most 64 concurrent Cassandra
	// point reads (or object-store existence checks) from this route — a figure
	// chosen to stay well inside a single node's driver pool while leaving the
	// rest of it for the request paths users actually wait on. The per-user cap
	// is the fairness split: one desktop issues check-blocks serially per sync
	// task, so 4 leaves an honest client unqueued while two identities can still
	// fill the node.
	DefaultCheckBlocksMaxInflightPerNode = 8
	DefaultCheckBlocksMaxInflightPerUser = 4
	DefaultCheckBlocksMaxWaitersPerUser  = 8
	DefaultCheckBlocksMaxWaitersPerNode  = 64

	// The wait matches the block route's, and for the same reason: it is long
	// enough to absorb a burst and short enough that the sync client is still
	// listening when the 503 arrives. That value was validated against the real
	// client for subcontract B; reusing it means one number to re-validate, not
	// two.
	DefaultCheckBlocksAdmissionWait = 10 * time.Second

	// The lifetime must cover a legitimate worst case, not a typical one: a
	// 100k-id list at fan-out 8 is ~12.5k sequential rounds of mapping reads plus
	// the same order of existence checks. 5 minutes covers that with headroom
	// while still bounding a stalled request to something a slot recovers from.
	DefaultCheckBlocksAdmittedLifetime = 5 * time.Minute

	DefaultCheckBlocksLookupFanout = 8

	// The historical parse cap, kept as both the default and the ceiling: this
	// knob exists to be lowered once real traffic is measured, never raised.
	DefaultCheckBlocksMaxIDs = 100_000
	MaxCheckBlocksMaxIDs     = 100_000

	MaxCheckBlocksMaxInflightPerNode = 4096
	MaxCheckBlocksMaxInflightPerUser = 4096
	MaxCheckBlocksMaxWaitersPerNode  = 65536
	MaxCheckBlocksMaxWaitersPerUser  = 4096
	MaxCheckBlocksAdmissionWait      = 2 * time.Minute
	MaxCheckBlocksAdmittedLifetime   = 30 * time.Minute
	// Keep this aligned with the canonical reader's actual maximum. A larger
	// value would be accepted here but silently reduced by the reader, while
	// the mapping phase would still use the larger value.
	MaxCheckBlocksLookupFanout = 32

	// MaxCheckBlocksConcurrentLookups is the ceiling on the product
	// (node cap x fan-out): the real quantity being budgeted is how many metadata
	// lookups this one route may have outstanding at once. Either factor alone
	// says nothing about that, which is how a "harmless" fan-out bump would
	// quietly multiply the node's load on Cassandra.
	MaxCheckBlocksConcurrentLookups = 256

	// Anonymous upload-link write limits.
	//
	// These are starting values, not measured ones, and they are deliberately
	// generous. The failure mode of a rate limit on a data path is not "an
	// attacker gets through" — it is a real person's upload dying — so the
	// defaults are set well above plausible browser behaviour and left to be
	// tightened from `upload_link_write_throttled_total`. At 8 MiB chunks, 600/min
	// sustains ~80 MiB/s per (IP, stable link source) after a burst covering ~9 GiB or 1200
	// small files.
	//
	// A2's concurrency defaults cap one stable source at 16 and the process/node
	// at 128. All rate and concurrency state is process-local, so fleet capacity
	// scales with node count.
	DefaultUploadLinkWritesPerMinute       = 600
	DefaultUploadLinkWriteBurst            = 1200
	DefaultUploadLinkSourceWritesPerMinute = 12000
	DefaultUploadLinkSourceWriteBurst      = 24000
	DefaultUploadLinkMaxInflightPerSource  = 16
	DefaultUploadLinkMaxInflightPerNode    = 128

	// MaxUploadLinkWritesPerMinute is an operational configuration ceiling. Rates
	// above it are not useful protection and are more likely to be a unit mistake;
	// this is policy, not the mathematical overflow point of time.Duration.
	MaxUploadLinkWritesPerMinute = 600000
	// MaxUploadLinkMaxInflightPerSource and MaxUploadLinkMaxInflightPerNode
	// reject likely unit mistakes before they allocate unrealistic process-local
	// concurrency budgets.
	MaxUploadLinkMaxInflightPerSource = 4096
	MaxUploadLinkMaxInflightPerNode   = 65536
)

// Download admission validation ceilings are configuration sanity limits, not
// measured operating defaults. D1 ships the feature disabled; D6 chooses positive
// values from real transfer evidence.
const (
	MaxDownloadAdmissionActive             = 1024
	MaxDownloadAdmissionWaitersPerNode     = 4096
	MaxDownloadAdmissionWaitersPerIdentity = 1024
	MaxDownloadAdmissionWait               = 5 * time.Minute
	MaxDownloadAdmissionPreparation        = 1 * time.Hour
	MaxDownloadAdmissionIdleWrite          = 15 * time.Minute
	MaxDownloadAdmissionRetryAfter         = 1 * time.Hour
)

// validateCheckBlocksBounds checks the subcontract C knobs.
//
// The shape mirrors the sync-block validation deliberately: same bounds and
// per-user-must-not-exceed-per-node rules, with both caps allowed to be zero as
// an explicit full disable. Unlike the block route, a per-user-only mode is not
// valid here because it leaves aggregate metadata work unbounded. The other rule
// with no counterpart there is the product ceiling, because neither factor
// states concurrent metadata lookups on its own.
func (c *Config) validateCheckBlocksBounds() error {
	if c.SeafHTTP.CheckBlocksMaxIDs <= 0 {
		return fmt.Errorf("seafhttp.check_blocks_max_ids must be greater than zero (an unbounded id list is the defect this cap exists to prevent)")
	}
	if c.SeafHTTP.CheckBlocksMaxIDs > MaxCheckBlocksMaxIDs {
		return fmt.Errorf("seafhttp.check_blocks_max_ids is %d, above the %d ceiling; this knob exists to be lowered from real traffic, and raising it re-opens the per-request work amplification it bounds",
			c.SeafHTTP.CheckBlocksMaxIDs, MaxCheckBlocksMaxIDs)
	}
	if c.SeafHTTP.CheckBlocksMaxInflightPerNode < 0 {
		return fmt.Errorf("seafhttp.check_blocks_max_inflight_per_node must be greater than or equal to zero (zero disables the cap)")
	}
	if c.SeafHTTP.CheckBlocksMaxInflightPerUser < 0 {
		return fmt.Errorf("seafhttp.check_blocks_max_inflight_per_user must be greater than or equal to zero (zero disables the cap)")
	}
	if c.SeafHTTP.CheckBlocksMaxInflightPerNode > MaxCheckBlocksMaxInflightPerNode {
		return fmt.Errorf("seafhttp.check_blocks_max_inflight_per_node is %d, above the %d ceiling; this cap is a metadata-work budget, not a throughput setting",
			c.SeafHTTP.CheckBlocksMaxInflightPerNode, MaxCheckBlocksMaxInflightPerNode)
	}
	if c.SeafHTTP.CheckBlocksMaxInflightPerUser > MaxCheckBlocksMaxInflightPerUser {
		return fmt.Errorf("seafhttp.check_blocks_max_inflight_per_user is %d, above the %d ceiling",
			c.SeafHTTP.CheckBlocksMaxInflightPerUser, MaxCheckBlocksMaxInflightPerUser)
	}
	if c.SeafHTTP.CheckBlocksMaxInflightPerUser > 0 && c.SeafHTTP.CheckBlocksMaxInflightPerNode == 0 {
		return fmt.Errorf("seafhttp.check_blocks_max_inflight_per_user requires seafhttp.check_blocks_max_inflight_per_node; enable both caps or set both to zero")
	}
	if c.SeafHTTP.CheckBlocksMaxInflightPerUser > 0 && c.SeafHTTP.CheckBlocksMaxInflightPerNode > 0 &&
		c.SeafHTTP.CheckBlocksMaxInflightPerUser > c.SeafHTTP.CheckBlocksMaxInflightPerNode {
		return fmt.Errorf("seafhttp.check_blocks_max_inflight_per_user must not exceed seafhttp.check_blocks_max_inflight_per_node when both caps are enabled")
	}
	if c.SeafHTTP.CheckBlocksMaxWaitersPerNode < 0 {
		return fmt.Errorf("seafhttp.check_blocks_max_waiters_per_node must be greater than or equal to zero (zero rejects immediately when the node gate is full)")
	}
	if c.SeafHTTP.CheckBlocksMaxWaitersPerUser < 0 {
		return fmt.Errorf("seafhttp.check_blocks_max_waiters_per_user must be greater than or equal to zero (zero rejects immediately when the user gate is full)")
	}
	if c.SeafHTTP.CheckBlocksMaxWaitersPerNode > MaxCheckBlocksMaxWaitersPerNode {
		return fmt.Errorf("seafhttp.check_blocks_max_waiters_per_node is %d, above the %d ceiling", c.SeafHTTP.CheckBlocksMaxWaitersPerNode, MaxCheckBlocksMaxWaitersPerNode)
	}
	if c.SeafHTTP.CheckBlocksMaxWaitersPerUser > MaxCheckBlocksMaxWaitersPerUser {
		return fmt.Errorf("seafhttp.check_blocks_max_waiters_per_user is %d, above the %d ceiling", c.SeafHTTP.CheckBlocksMaxWaitersPerUser, MaxCheckBlocksMaxWaitersPerUser)
	}
	if c.SeafHTTP.CheckBlocksMaxInflightPerUser > 0 && c.SeafHTTP.CheckBlocksMaxWaitersPerUser > c.SeafHTTP.CheckBlocksMaxWaitersPerNode {
		return fmt.Errorf("seafhttp.check_blocks_max_waiters_per_user=%d must not exceed seafhttp.check_blocks_max_waiters_per_node=%d; per-user waiters also reserve node waiter capacity, so the larger value would never bind",
			c.SeafHTTP.CheckBlocksMaxWaitersPerUser, c.SeafHTTP.CheckBlocksMaxWaitersPerNode)
	}
	if c.SeafHTTP.CheckBlocksAdmissionWait < 0 {
		return fmt.Errorf("seafhttp.check_blocks_admission_wait must be greater than or equal to zero (zero refuses immediately instead of waiting)")
	}
	if c.SeafHTTP.CheckBlocksAdmissionWait > MaxCheckBlocksAdmissionWait {
		return fmt.Errorf("seafhttp.check_blocks_admission_wait is %s, above the %s ceiling; a wait longer than the sync client's own request timeout means the client gives up before the server answers",
			c.SeafHTTP.CheckBlocksAdmissionWait, MaxCheckBlocksAdmissionWait)
	}
	if c.SeafHTTP.CheckBlocksAdmittedLifetime <= 0 {
		return fmt.Errorf("seafhttp.check_blocks_admitted_lifetime must be greater than zero")
	}
	if c.SeafHTTP.CheckBlocksAdmittedLifetime > MaxCheckBlocksAdmittedLifetime {
		return fmt.Errorf("seafhttp.check_blocks_admitted_lifetime is %s, above the %s ceiling; a larger processing deadline is indistinguishable from disabling the guard in practice",
			c.SeafHTTP.CheckBlocksAdmittedLifetime, MaxCheckBlocksAdmittedLifetime)
	}
	// Same reasoning as the block route: the admitted lifetime installs a
	// connection read deadline, and an earlier server deadline is preserved
	// rather than overwritten, so a server timeout above this value would leave
	// the stricter-looking number never taking effect.
	if c.Server.ReadTimeout > 0 && c.Server.ReadTimeout > c.SeafHTTP.CheckBlocksAdmittedLifetime {
		return fmt.Errorf("server.read_timeout=%s must not exceed seafhttp.check_blocks_admitted_lifetime=%s when enabled; the server deadline starts earlier and is preserved rather than overwritten by check-blocks admission",
			c.Server.ReadTimeout, c.SeafHTTP.CheckBlocksAdmittedLifetime)
	}
	if c.SeafHTTP.CheckBlocksLookupFanout <= 0 {
		return fmt.Errorf("seafhttp.check_blocks_lookup_fanout must be greater than zero (a request must be able to make at least one lookup)")
	}
	if c.SeafHTTP.CheckBlocksLookupFanout > MaxCheckBlocksLookupFanout {
		return fmt.Errorf("seafhttp.check_blocks_lookup_fanout is %d, above the %d ceiling", c.SeafHTTP.CheckBlocksLookupFanout, MaxCheckBlocksLookupFanout)
	}
	if c.SeafHTTP.CheckBlocksMaxInflightPerNode > 0 &&
		c.SeafHTTP.CheckBlocksMaxInflightPerNode*c.SeafHTTP.CheckBlocksLookupFanout > MaxCheckBlocksConcurrentLookups {
		return fmt.Errorf("seafhttp.check_blocks_max_inflight_per_node=%d x seafhttp.check_blocks_lookup_fanout=%d is %d concurrent metadata lookups, above the %d ceiling; the two multiply, so raising either one alone still raises what this route can put on Cassandra and object storage at once",
			c.SeafHTTP.CheckBlocksMaxInflightPerNode, c.SeafHTTP.CheckBlocksLookupFanout,
			c.SeafHTTP.CheckBlocksMaxInflightPerNode*c.SeafHTTP.CheckBlocksLookupFanout, MaxCheckBlocksConcurrentLookups)
	}
	return nil
}

// validateUploadLinkWriteLimit checks one rate/burst pair.
//
// Rate and burst are independent dimensions of a token bucket: rate is how fast
// tokens come back, burst is how many may be spent at once. There is no rule
// requiring one to exceed the other — 600/min with a burst of 1 is a coherent
// (if unfriendly) configuration. So the only things rejected here are values
// that cannot mean anything: a negative rate or burst, and a burst of zero while
// the limiter is on, which would refuse every request. The ceiling on a positive
// rate is operational policy rather than a value that cannot mean anything.
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
		return fmt.Errorf("seafhttp.%s is %d, above the operational configuration ceiling of %d requests per minute",
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

	// A per-IP limiter is only per-IP if the IP is real. Behind an untrusted proxy,
	// ClientIP() is the proxy: clients using the same public link then share its
	// (proxy IP, source) bucket, while different links remain isolated. This is a
	// warning rather than a validation error because direct deployments are valid.
	if cfg.SeafHTTP.UploadLinkWritesPerMinute > 0 && len(cfg.Server.TrustedProxies) == 0 {
		slog.Warn("upload-link write limiter is enabled but server.trusted_proxies is empty; "+
			"behind a reverse proxy, clients using the same public link are attributed to the proxy IP "+
			"and share one per-client bucket (different links remain isolated) — set SERVER_TRUSTED_PROXIES to the proxy CIDR",
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

			SyncBlockMaxInflightPerNode: DefaultSyncBlockMaxInflightPerNode,
			SyncBlockMemoryBudgetBytes:  DefaultSyncBlockMemoryBudgetBytes,
			SyncBlockMaxInflightPerUser: DefaultSyncBlockMaxInflightPerUser,
			SyncBlockAdmissionWait:      DefaultSyncBlockAdmissionWait,
			SyncBlockMaxWaitersPerUser:  DefaultSyncBlockMaxWaitersPerUser,
			SyncBlockMaxWaitersPerNode:  DefaultSyncBlockMaxWaitersPerNode,
			SyncBlockAdmittedLifetime:   DefaultSyncBlockAdmittedLifetime,

			CheckBlocksMaxIDs:             DefaultCheckBlocksMaxIDs,
			CheckBlocksMaxInflightPerNode: DefaultCheckBlocksMaxInflightPerNode,
			CheckBlocksMaxInflightPerUser: DefaultCheckBlocksMaxInflightPerUser,
			CheckBlocksMaxWaitersPerUser:  DefaultCheckBlocksMaxWaitersPerUser,
			CheckBlocksMaxWaitersPerNode:  DefaultCheckBlocksMaxWaitersPerNode,
			CheckBlocksAdmissionWait:      DefaultCheckBlocksAdmissionWait,
			CheckBlocksAdmittedLifetime:   DefaultCheckBlocksAdmittedLifetime,
			CheckBlocksLookupFanout:       DefaultCheckBlocksLookupFanout,

			UploadLinkWritesPerMinute:       DefaultUploadLinkWritesPerMinute,
			UploadLinkWriteBurst:            DefaultUploadLinkWriteBurst,
			UploadLinkSourceWritesPerMinute: DefaultUploadLinkSourceWritesPerMinute,
			UploadLinkSourceWriteBurst:      DefaultUploadLinkSourceWriteBurst,
			UploadLinkMaxInflightPerSource:  DefaultUploadLinkMaxInflightPerSource,
			UploadLinkMaxInflightPerNode:    DefaultUploadLinkMaxInflightPerNode,
		},
		DownloadAdmission: DownloadAdmissionConfig{},
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
			MaxIWorkSourceBytes:  32 * 1024 * 1024,        // 32 MiB source -> ~192 MiB measured peak when encrypted
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

	// Download admission (D1 schema; shipped disabled until D6 measurement)
	if v := os.Getenv("DOWNLOAD_ADMISSION_ENABLED"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			c.DownloadAdmission.Enabled = true
		case "false", "0":
			c.DownloadAdmission.Enabled = false
		default:
			c.addEnvOverrideError("DOWNLOAD_ADMISSION_ENABLED must be true, false, 1, or 0, got %q", v)
		}
	}
	for _, override := range []struct {
		env    string
		target *int
	}{
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_NODE", &c.DownloadAdmission.MaxActivePerNode},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_AUTH_USER", &c.DownloadAdmission.MaxActivePerAuthUser},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_LINK_SOURCE", &c.DownloadAdmission.MaxActivePerLinkSource},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_CLIENT_LINK", &c.DownloadAdmission.MaxActivePerClientLink},
		{"DOWNLOAD_ADMISSION_MAX_WAITERS_PER_IDENTITY", &c.DownloadAdmission.MaxWaitersPerIdentity},
		{"DOWNLOAD_ADMISSION_MAX_WAITERS_PER_NODE", &c.DownloadAdmission.MaxWaitersPerNode},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_BLOCK", &c.DownloadAdmission.MaxActiveBlock},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_FILE", &c.DownloadAdmission.MaxActiveFile},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_RAW", &c.DownloadAdmission.MaxActiveRaw},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_HISTORY", &c.DownloadAdmission.MaxActiveHistory},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_LINK_RAW", &c.DownloadAdmission.MaxActiveLinkRaw},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_ZIP", &c.DownloadAdmission.MaxActiveZIP},
		{"DOWNLOAD_ADMISSION_MAX_ACTIVE_LINK_INLINE", &c.DownloadAdmission.MaxActiveLinkInline},
	} {
		if v := os.Getenv(override.env); v != "" {
			i, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				c.addEnvOverrideError("%s is invalid: %v", override.env, err)
			} else {
				*override.target = i
			}
		}
	}
	for _, override := range []struct {
		env    string
		target *time.Duration
	}{
		{"DOWNLOAD_ADMISSION_ADMISSION_WAIT", &c.DownloadAdmission.AdmissionWait},
		{"DOWNLOAD_ADMISSION_PREPARATION_DEADLINE", &c.DownloadAdmission.PreparationDeadline},
		{"DOWNLOAD_ADMISSION_IDLE_WRITE_TIMEOUT", &c.DownloadAdmission.IdleWriteTimeout},
		{"DOWNLOAD_ADMISSION_RETRY_AFTER", &c.DownloadAdmission.RetryAfter},
	} {
		if v := os.Getenv(override.env); v != "" {
			d, err := time.ParseDuration(strings.TrimSpace(v))
			if err != nil {
				c.addEnvOverrideError("%s is invalid: %v", override.env, err)
			} else {
				*override.target = d
			}
		}
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
	// The admission wait is reported rather than silently dropped for the same
	// reason as the body cap above: an operator who deliberately shortened the
	// wait must not end up running the default one with no signal.
	if v := os.Getenv("SEAFHTTP_SYNC_BLOCK_ADMISSION_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			c.addEnvOverrideError("SEAFHTTP_SYNC_BLOCK_ADMISSION_WAIT is invalid: %v", err)
		} else {
			c.SeafHTTP.SyncBlockAdmissionWait = d
		}
	}
	if v := os.Getenv("SEAFHTTP_SYNC_BLOCK_ADMITTED_LIFETIME"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			c.addEnvOverrideError("SEAFHTTP_SYNC_BLOCK_ADMITTED_LIFETIME is invalid: %v", err)
		} else {
			c.SeafHTTP.SyncBlockAdmittedLifetime = d
		}
	}
	if v := os.Getenv("SEAFHTTP_SYNC_BLOCK_MEMORY_BUDGET_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			c.addEnvOverrideError("SEAFHTTP_SYNC_BLOCK_MEMORY_BUDGET_BYTES is invalid: %v", err)
		} else {
			c.SeafHTTP.SyncBlockMemoryBudgetBytes = n
		}
	}
	for _, override := range []struct {
		env    string
		target *time.Duration
	}{
		{"SEAFHTTP_CHECK_BLOCKS_ADMISSION_WAIT", &c.SeafHTTP.CheckBlocksAdmissionWait},
		{"SEAFHTTP_CHECK_BLOCKS_ADMITTED_LIFETIME", &c.SeafHTTP.CheckBlocksAdmittedLifetime},
	} {
		v := os.Getenv(override.env)
		if v == "" {
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			c.addEnvOverrideError("%s is invalid: %v", override.env, err)
			continue
		}
		*override.target = d
	}
	for _, override := range []struct {
		env    string
		target *int
	}{
		{"SEAFHTTP_SYNC_BLOCK_MAX_INFLIGHT_PER_NODE", &c.SeafHTTP.SyncBlockMaxInflightPerNode},
		{"SEAFHTTP_SYNC_BLOCK_MAX_INFLIGHT_PER_USER", &c.SeafHTTP.SyncBlockMaxInflightPerUser},
		{"SEAFHTTP_SYNC_BLOCK_MAX_WAITERS_PER_NODE", &c.SeafHTTP.SyncBlockMaxWaitersPerNode},
		{"SEAFHTTP_SYNC_BLOCK_MAX_WAITERS_PER_USER", &c.SeafHTTP.SyncBlockMaxWaitersPerUser},
		{"SEAFHTTP_CHECK_BLOCKS_MAX_IDS", &c.SeafHTTP.CheckBlocksMaxIDs},
		{"SEAFHTTP_CHECK_BLOCKS_MAX_INFLIGHT_PER_NODE", &c.SeafHTTP.CheckBlocksMaxInflightPerNode},
		{"SEAFHTTP_CHECK_BLOCKS_MAX_INFLIGHT_PER_USER", &c.SeafHTTP.CheckBlocksMaxInflightPerUser},
		{"SEAFHTTP_CHECK_BLOCKS_MAX_WAITERS_PER_NODE", &c.SeafHTTP.CheckBlocksMaxWaitersPerNode},
		{"SEAFHTTP_CHECK_BLOCKS_MAX_WAITERS_PER_USER", &c.SeafHTTP.CheckBlocksMaxWaitersPerUser},
		{"SEAFHTTP_CHECK_BLOCKS_LOOKUP_FANOUT", &c.SeafHTTP.CheckBlocksLookupFanout},
		{"SEAFHTTP_UPLOAD_LINK_WRITES_PER_MINUTE", &c.SeafHTTP.UploadLinkWritesPerMinute},
		{"SEAFHTTP_UPLOAD_LINK_WRITE_BURST", &c.SeafHTTP.UploadLinkWriteBurst},
		{"SEAFHTTP_UPLOAD_LINK_SOURCE_WRITES_PER_MINUTE", &c.SeafHTTP.UploadLinkSourceWritesPerMinute},
		{"SEAFHTTP_UPLOAD_LINK_SOURCE_WRITE_BURST", &c.SeafHTTP.UploadLinkSourceWriteBurst},
		{"SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_SOURCE", &c.SeafHTTP.UploadLinkMaxInflightPerSource},
		{"SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_NODE", &c.SeafHTTP.UploadLinkMaxInflightPerNode},
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
	if v := os.Getenv("FILEVIEW_MAX_IWORK_SOURCE_BYTES"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.FileView.MaxIWorkSourceBytes = i
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

// ValidateDownloadAdmissionConfig validates the standalone D1 configuration.
// Cross-config server checks remain in Config.Validate.
func ValidateDownloadAdmissionConfig(d DownloadAdmissionConfig) error {
	return validateDownloadAdmissionConfig(d, 0)
}

func (c *Config) validateDownloadAdmissionBounds() error {
	if err := validateDownloadAdmissionConfig(c.DownloadAdmission, c.Server.WriteTimeout); err != nil {
		return err
	}
	return c.validateDownloadAdmissionMemoryBudget()
}

func (c *Config) validateDownloadAdmissionMemoryBudget() error {
	if c == nil || !c.DownloadAdmission.Enabled {
		return nil
	}

	source := c.FileView.MaxIWorkSourceBytes
	if source <= 0 {
		return fmt.Errorf("fileview.max_iwork_source_bytes must be greater than zero when download_admission.enabled is true")
	}
	if source > c.FileView.MaxPreviewBytes {
		return fmt.Errorf("fileview.max_iwork_source_bytes=%d must not exceed fileview.max_preview_bytes=%d", source, c.FileView.MaxPreviewBytes)
	}
	// SyncBlockMaxBytes has its own validation later in Config.Validate. Avoid
	// masking that more precise error if it is invalid here.
	if c.SeafHTTP.SyncBlockMaxBytes <= 0 {
		return nil
	}

	rawSlots := int64(c.DownloadAdmission.MaxActivePerNode)
	if rawCap := int64(c.DownloadAdmission.MaxActiveRaw); rawCap > 0 && rawCap < rawSlots {
		rawSlots = rawCap
	}
	if source > DefaultDownloadAdmissionMemoryBudgetBytes/DownloadAdmissionIWorkEncryptedPeakMultiplier {
		return fmt.Errorf("fileview.max_iwork_source_bytes=%d is too large for the %d-byte download admission budget", source, DefaultDownloadAdmissionMemoryBudgetBytes)
	}
	iworkCost := source * DownloadAdmissionIWorkEncryptedPeakMultiplier
	encryptedCost := (c.SeafHTTP.SyncBlockMaxBytes*DownloadAdmissionEncryptedPeakNumerator + DownloadAdmissionEncryptedPeakDenominator - 1) / DownloadAdmissionEncryptedPeakDenominator
	otherSlots := int64(c.DownloadAdmission.MaxActivePerNode) - rawSlots
	total := rawSlots*iworkCost + otherSlots*encryptedCost
	if total > DefaultDownloadAdmissionMemoryBudgetBytes {
		return fmt.Errorf("download admission memory design is %d bytes with %d raw slots at %d bytes and %d other slots at %d bytes; budget is %d bytes", total, rawSlots, iworkCost, otherSlots, encryptedCost, DefaultDownloadAdmissionMemoryBudgetBytes)
	}
	return nil
}

func validateDownloadAdmissionConfig(d DownloadAdmissionConfig, serverWriteTimeout time.Duration) error {
	activeCaps := []struct {
		name  string
		value int
	}{
		{"max_active_per_node", d.MaxActivePerNode},
		{"max_active_per_auth_user", d.MaxActivePerAuthUser},
		{"max_active_per_link_source", d.MaxActivePerLinkSource},
		{"max_active_per_client_link", d.MaxActivePerClientLink},
		{"max_active_block", d.MaxActiveBlock},
		{"max_active_file", d.MaxActiveFile},
		{"max_active_raw", d.MaxActiveRaw},
		{"max_active_history", d.MaxActiveHistory},
		{"max_active_link_raw", d.MaxActiveLinkRaw},
		{"max_active_zip", d.MaxActiveZIP},
		{"max_active_link_inline", d.MaxActiveLinkInline},
	}
	for _, cap := range activeCaps {
		if cap.value < 0 {
			return fmt.Errorf("download_admission.%s must be greater than or equal to zero", cap.name)
		}
		if cap.value > MaxDownloadAdmissionActive {
			return fmt.Errorf("download_admission.%s is %d, above the %d ceiling", cap.name, cap.value, MaxDownloadAdmissionActive)
		}
	}
	if d.MaxWaitersPerIdentity < 0 {
		return fmt.Errorf("download_admission.max_waiters_per_identity must be greater than or equal to zero")
	}
	if d.MaxWaitersPerNode < 0 {
		return fmt.Errorf("download_admission.max_waiters_per_node must be greater than or equal to zero")
	}
	if d.MaxWaitersPerIdentity > MaxDownloadAdmissionWaitersPerIdentity {
		return fmt.Errorf("download_admission.max_waiters_per_identity is %d, above the %d ceiling", d.MaxWaitersPerIdentity, MaxDownloadAdmissionWaitersPerIdentity)
	}
	if d.MaxWaitersPerNode > MaxDownloadAdmissionWaitersPerNode {
		return fmt.Errorf("download_admission.max_waiters_per_node is %d, above the %d ceiling", d.MaxWaitersPerNode, MaxDownloadAdmissionWaitersPerNode)
	}

	durations := []struct {
		name  string
		value time.Duration
		max   time.Duration
	}{
		{"admission_wait", d.AdmissionWait, MaxDownloadAdmissionWait},
		{"preparation_deadline", d.PreparationDeadline, MaxDownloadAdmissionPreparation},
		{"idle_write_timeout", d.IdleWriteTimeout, MaxDownloadAdmissionIdleWrite},
		{"retry_after", d.RetryAfter, MaxDownloadAdmissionRetryAfter},
	}
	for _, duration := range durations {
		if duration.value < 0 {
			return fmt.Errorf("download_admission.%s must be greater than or equal to zero", duration.name)
		}
		if duration.value > duration.max {
			return fmt.Errorf("download_admission.%s is %s, above the %s ceiling", duration.name, duration.value, duration.max)
		}
	}

	if d.MaxActivePerAuthUser > d.MaxActivePerNode {
		return fmt.Errorf("download_admission.max_active_per_auth_user must not exceed max_active_per_node")
	}
	if d.MaxActivePerLinkSource > d.MaxActivePerNode {
		return fmt.Errorf("download_admission.max_active_per_link_source must not exceed max_active_per_node")
	}
	if d.MaxActivePerClientLink > d.MaxActivePerNode {
		return fmt.Errorf("download_admission.max_active_per_client_link must not exceed max_active_per_node")
	}
	for _, cap := range activeCaps[4:] {
		if cap.value > d.MaxActivePerNode {
			return fmt.Errorf("download_admission.%s must not exceed max_active_per_node", cap.name)
		}
	}
	if d.MaxActivePerClientLink > d.MaxActivePerLinkSource {
		return fmt.Errorf("download_admission.max_active_per_client_link must not exceed max_active_per_link_source")
	}
	if d.MaxWaitersPerIdentity > d.MaxWaitersPerNode {
		return fmt.Errorf("download_admission.max_waiters_per_identity must not exceed max_waiters_per_node")
	}

	if d.Enabled {
		positiveCaps := []struct {
			name  string
			value int
		}{
			{"max_active_per_node", d.MaxActivePerNode},
			{"max_active_per_auth_user", d.MaxActivePerAuthUser},
			{"max_active_per_link_source", d.MaxActivePerLinkSource},
			{"max_active_per_client_link", d.MaxActivePerClientLink},
		}
		for _, cap := range positiveCaps {
			if cap.value <= 0 {
				return fmt.Errorf("download_admission.%s must be greater than zero when enabled", cap.name)
			}
		}
		positiveDurations := []struct {
			name  string
			value time.Duration
		}{
			{"preparation_deadline", d.PreparationDeadline},
			{"idle_write_timeout", d.IdleWriteTimeout},
			{"retry_after", d.RetryAfter},
		}
		for _, duration := range positiveDurations {
			if duration.value <= 0 {
				return fmt.Errorf("download_admission.%s must be greater than zero when enabled", duration.name)
			}
		}
		if serverWriteTimeout != 0 {
			return fmt.Errorf("server.write_timeout must be zero when download_admission.enabled is true")
		}
	}
	return nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if len(c.envOverrideErrors) > 0 {
		return fmt.Errorf("invalid environment override: %s", strings.Join(c.envOverrideErrors, "; "))
	}
	if err := c.validateDownloadAdmissionBounds(); err != nil {
		return err
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
		return fmt.Errorf("seafhttp.sync_block_max_bytes is %d, above the %d ceiling; the official client CDC maximum is 4 MiB and SesameFS's related server-side split is 8 MiB, so a larger value is likely derived from the unrelated web uploader ceiling",
			c.SeafHTTP.SyncBlockMaxBytes, MaxSyncBlockMaxBytes)
	}
	// The in-flight caps are what make sync_block_max_bytes an aggregate bound.
	// Zero is accepted here — unlike the body cap — because disabling the
	// process-local gate is a coherent choice for an operator running an external
	// admission controller in front of this route, whereas an unbounded body
	// never is.
	if c.SeafHTTP.SyncBlockMaxInflightPerNode < 0 {
		return fmt.Errorf("seafhttp.sync_block_max_inflight_per_node must be greater than or equal to zero (zero disables the cap)")
	}
	if c.SeafHTTP.SyncBlockMaxInflightPerUser < 0 {
		return fmt.Errorf("seafhttp.sync_block_max_inflight_per_user must be greater than or equal to zero (zero disables the cap)")
	}
	if c.SeafHTTP.SyncBlockMaxInflightPerNode > MaxSyncBlockMaxInflightPerNode {
		return fmt.Errorf("seafhttp.sync_block_max_inflight_per_node is %d, above the %d ceiling; this cap is a memory budget divided by the measured per-request cost, not a throughput setting",
			c.SeafHTTP.SyncBlockMaxInflightPerNode, MaxSyncBlockMaxInflightPerNode)
	}
	if c.SeafHTTP.SyncBlockMaxInflightPerUser > MaxSyncBlockMaxInflightPerUser {
		return fmt.Errorf("seafhttp.sync_block_max_inflight_per_user is %d, above the %d ceiling",
			c.SeafHTTP.SyncBlockMaxInflightPerUser, MaxSyncBlockMaxInflightPerUser)
	}
	if c.SeafHTTP.SyncBlockMemoryBudgetBytes <= 0 || c.SeafHTTP.SyncBlockMemoryBudgetBytes > MaxSyncBlockMemoryBudgetBytes {
		return fmt.Errorf("seafhttp.sync_block_memory_budget_bytes must be between 1 and %d", MaxSyncBlockMemoryBudgetBytes)
	}
	if c.SeafHTTP.SyncBlockMaxInflightPerNode > 0 {
		designBytes := (DefaultSyncBlockDesignBytes*c.SeafHTTP.SyncBlockMaxBytes + DefaultSyncBlockMaxBytes - 1) / DefaultSyncBlockMaxBytes
		// The measurement includes fixed HTTP, goroutine, hashing and storage
		// overhead. A smaller body cap cannot scale that fixed cost toward zero.
		if designBytes < DefaultSyncBlockDesignBytes {
			designBytes = DefaultSyncBlockDesignBytes
		}
		if int64(c.SeafHTTP.SyncBlockMaxInflightPerNode) > c.SeafHTTP.SyncBlockMemoryBudgetBytes/designBytes {
			return fmt.Errorf("seafhttp.sync_block_max_inflight_per_node=%d exceeds sync_block_memory_budget_bytes=%d at estimated design cost %d bytes per admission", c.SeafHTTP.SyncBlockMaxInflightPerNode, c.SeafHTTP.SyncBlockMemoryBudgetBytes, designBytes)
		}
	}
	// A per-user cap above the node cap cannot bind: the node gate is acquired
	// second and would refuse first, so the configuration reads as fairness
	// protection that silently does nothing.
	if c.SeafHTTP.SyncBlockMaxInflightPerUser > 0 && c.SeafHTTP.SyncBlockMaxInflightPerNode > 0 &&
		c.SeafHTTP.SyncBlockMaxInflightPerUser > c.SeafHTTP.SyncBlockMaxInflightPerNode {
		return fmt.Errorf("seafhttp.sync_block_max_inflight_per_user must not exceed seafhttp.sync_block_max_inflight_per_node when both caps are enabled")
	}
	if c.SeafHTTP.SyncBlockAdmissionWait < 0 {
		return fmt.Errorf("seafhttp.sync_block_admission_wait must be greater than or equal to zero (zero refuses immediately instead of waiting)")
	}
	if c.SeafHTTP.SyncBlockAdmissionWait > MaxSyncBlockAdmissionWait {
		return fmt.Errorf("seafhttp.sync_block_admission_wait is %s, above the %s ceiling; a wait longer than the sync client's own request timeout means the client gives up before the server answers",
			c.SeafHTTP.SyncBlockAdmissionWait, MaxSyncBlockAdmissionWait)
	}
	if c.SeafHTTP.SyncBlockMaxWaitersPerNode < 0 {
		return fmt.Errorf("seafhttp.sync_block_max_waiters_per_node must be greater than or equal to zero (zero rejects immediately when the node gate is full)")
	}
	if c.SeafHTTP.SyncBlockMaxWaitersPerUser < 0 {
		return fmt.Errorf("seafhttp.sync_block_max_waiters_per_user must be greater than or equal to zero (zero rejects immediately when the user gate is full)")
	}
	if c.SeafHTTP.SyncBlockMaxWaitersPerNode > MaxSyncBlockMaxWaitersPerNode {
		return fmt.Errorf("seafhttp.sync_block_max_waiters_per_node is %d, above the %d ceiling", c.SeafHTTP.SyncBlockMaxWaitersPerNode, MaxSyncBlockMaxWaitersPerNode)
	}
	if c.SeafHTTP.SyncBlockMaxWaitersPerUser > MaxSyncBlockMaxWaitersPerUser {
		return fmt.Errorf("seafhttp.sync_block_max_waiters_per_user is %d, above the %d ceiling", c.SeafHTTP.SyncBlockMaxWaitersPerUser, MaxSyncBlockMaxWaitersPerUser)
	}
	// A per-user waiter budget above the per-node one cannot bind: a request
	// parked on its own gate also reserves node waiter capacity, so the node
	// queue refuses first and the per-user number silently does nothing. Same
	// reasoning as the in-flight caps above.
	if c.SeafHTTP.SyncBlockMaxInflightPerUser > 0 && c.SeafHTTP.SyncBlockMaxWaitersPerUser > c.SeafHTTP.SyncBlockMaxWaitersPerNode {
		return fmt.Errorf("seafhttp.sync_block_max_waiters_per_user=%d must not exceed seafhttp.sync_block_max_waiters_per_node=%d; per-user waiters also reserve node waiter capacity, so the larger value would never bind",
			c.SeafHTTP.SyncBlockMaxWaitersPerUser, c.SeafHTTP.SyncBlockMaxWaitersPerNode)
	}
	if c.SeafHTTP.SyncBlockAdmittedLifetime <= 0 {
		return fmt.Errorf("seafhttp.sync_block_admitted_lifetime must be greater than zero")
	}
	if c.SeafHTTP.SyncBlockAdmittedLifetime > MaxSyncBlockAdmittedLifetime {
		return fmt.Errorf("seafhttp.sync_block_admitted_lifetime is %s, above the %s ceiling; a larger processing deadline is indistinguishable from disabling the guard in practice",
			c.SeafHTTP.SyncBlockAdmittedLifetime, MaxSyncBlockAdmittedLifetime)
	}
	if c.Server.ReadTimeout > 0 && c.Server.ReadTimeout > c.SeafHTTP.SyncBlockAdmittedLifetime {
		return fmt.Errorf("server.read_timeout=%s must not exceed seafhttp.sync_block_admitted_lifetime=%s when enabled; the server deadline starts earlier and is preserved rather than overwritten by block admission",
			c.Server.ReadTimeout, c.SeafHTTP.SyncBlockAdmittedLifetime)
	}
	if err := c.validateCheckBlocksBounds(); err != nil {
		return err
	}
	if err := validateUploadLinkWriteLimit(
		"upload_link_writes_per_minute", "upload_link_write_burst",
		c.SeafHTTP.UploadLinkWritesPerMinute, c.SeafHTTP.UploadLinkWriteBurst,
	); err != nil {
		return err
	}
	if c.SeafHTTP.UploadLinkMaxInflightPerSource < 0 {
		return fmt.Errorf("seafhttp.upload_link_max_inflight_per_source must be greater than or equal to zero (zero disables the cap)")
	}
	if c.SeafHTTP.UploadLinkMaxInflightPerNode < 0 {
		return fmt.Errorf("seafhttp.upload_link_max_inflight_per_node must be greater than or equal to zero (zero disables the cap)")
	}
	if c.SeafHTTP.UploadLinkMaxInflightPerSource > MaxUploadLinkMaxInflightPerSource {
		return fmt.Errorf("seafhttp.upload_link_max_inflight_per_source is %d, above the %d ceiling", c.SeafHTTP.UploadLinkMaxInflightPerSource, MaxUploadLinkMaxInflightPerSource)
	}
	if c.SeafHTTP.UploadLinkMaxInflightPerNode > MaxUploadLinkMaxInflightPerNode {
		return fmt.Errorf("seafhttp.upload_link_max_inflight_per_node is %d, above the %d ceiling", c.SeafHTTP.UploadLinkMaxInflightPerNode, MaxUploadLinkMaxInflightPerNode)
	}
	if c.SeafHTTP.UploadLinkMaxInflightPerSource > 0 && c.SeafHTTP.UploadLinkMaxInflightPerNode > 0 && c.SeafHTTP.UploadLinkMaxInflightPerSource > c.SeafHTTP.UploadLinkMaxInflightPerNode {
		return fmt.Errorf("seafhttp.upload_link_max_inflight_per_source must not exceed seafhttp.upload_link_max_inflight_per_node when both caps are enabled")
	}
	if err := validateUploadLinkWriteLimit(
		"upload_link_source_writes_per_minute", "upload_link_source_write_burst",
		c.SeafHTTP.UploadLinkSourceWritesPerMinute, c.SeafHTTP.UploadLinkSourceWriteBurst,
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
