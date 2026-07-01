package mb

import (
	"database/sql"
	"html/template"
	"net"
	"net/mail"
	"sync"
	"time"
)

const (
	defaultAddr       = ":15000"
	defaultDBPath     = "./mb.db"
	defaultCookieName = "mb_admin"

	permanentISODate = "9999-12-31"

	settingPublicMode                = "public_mode"
	settingPasswordHash              = "admin_password_hash"
	settingCookieSecret              = "cookie_secret"
	settingFxAutoUpdate              = "fx_auto_update"
	settingFxEURToUSD                = "fx_eur_to_usd"
	settingFxEURToCNY                = "fx_eur_to_cny"
	settingFxEURToRUB                = "fx_eur_to_rub"
	settingFxUpdatedAt               = "fx_updated_at"
	settingFxRateDate                = "fx_rate_date"
	settingFxLastAttempt             = "fx_last_attempt_at"
	settingFxLastError               = "fx_last_error"
	settingFxSource                  = "fx_source"
	settingPublicShowTotals          = "public_show_totals"
	settingPublicShowDomainRegistrar = "public_show_domain_registrar"
	settingPublicShowDomainExpiry    = "public_show_domain_expiry"
	settingPublicShowDomainPrice     = "public_show_domain_price"
	settingPublicShowDomainNotes     = "public_show_domain_notes"
	settingPublicShowVPSIP           = "public_show_vps_ip"
	settingPublicShowVPSRegion       = "public_show_vps_region"
	settingPublicShowVPSSpecs        = "public_show_vps_specs"
	settingPublicShowVPSExpiry       = "public_show_vps_expiry"
	settingPublicShowVPSPrice        = "public_show_vps_price"
	settingPublicShowVPSNotes        = "public_show_vps_notes"
	settingFaviconURL                = "favicon_url"
	settingWebDAVURL                 = "webdav_url"
	settingWebDAVFile                = "webdav_filename"
	settingWebDAVUser                = "webdav_user"
	settingWebDAVPass                = "webdav_pass"
	settingEmailEnabled              = "email_enabled"
	settingEmailDays                 = "email_days_before"
	settingEmailProtocol             = "email_protocol"
	settingEmailHost                 = "email_host"
	settingEmailPort                 = "email_port"
	settingEmailEncrypt              = "email_encryption"
	settingEmailUser                 = "email_user"
	settingEmailPass                 = "email_pass"
	settingEmailFrom                 = "email_from"
	settingEmailTo                   = "email_to"
	fxSourceECB                      = "ecb"
	fxSourceOpenER                   = "open.er-api.com"
	fxSourceManual                   = "manual"

	defaultFxEURToUSD = 1.10
	defaultFxEURToCNY = 7.90
	defaultFxEURToRUB = 90.0

	defaultEmailDaysList       = "30"
	smtpTimeout                = 30 * time.Second
	backupVersion              = 1
	backupMaxBytes       int64 = 10 << 20

	sessionDuration        = 7 * 24 * time.Hour
	maxFormBodyBytes int64 = 1 << 20

	defaultLoginMaxFailures = 5
	defaultLoginWindow      = 15 * time.Minute
	defaultLoginLockout     = 15 * time.Minute
	defaultLoginMaxEntries  = 4096
)

// 新增与导入路径共用同一条写语句,避免两份 SQL 抄写不一致(参见 P0-0:
// VPS 插入曾被抄成两份,一份 28 个占位符、一份 27 个)。
// 启动时 validateWriteStatements 会对下列语句做 Prepare 自检(校验列数与占位符数)。
const (
	insertDomainSQL = `INSERT INTO domains(domain,registrar,expires_at,renewal_price,currency,notes,exclude_cost,pinned,hidden,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`

	insertVPSSQL = `INSERT INTO vps(vendor,ip,specs,region,cores,memory_gb,disk_gb,disk_type,disk2_gb,disk2_type,disks_json,traffic_tb,traffic_set,traffic_direction,bandwidth_value,bandwidth_unit,bandwidth_mbps,expires_at,renewal_price,currency,notes,exclude_cost,billing_months,pinned,hidden,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
)

type config struct {
	addr              string
	dbPath            string
	cookieName        string
	adminPassword     string
	loginMaxFailures  int
	loginWindow       time.Duration
	loginLockout      time.Duration
	loginMaxEntries   int
	trustProxyHeaders bool
	trustedProxyCIDRs []*net.IPNet
}
type app struct {
	cfg          config
	db           *sql.DB
	templates    map[string]*template.Template
	loginLimiter *loginLimiter
	fxMu         sync.Mutex
	fxUpdating   bool
}
type domainRow struct {
	ID               int64
	Domain           string
	Registrar        string
	ExpiresAt        string
	IsPermanent      bool
	DaysLeft         int
	DaysBadgeClass   string
	RenewalPrice     float64
	Currency         string
	Notes            string
	ExcludeCost      bool
	Pinned           bool
	Hidden           bool
	RenewalPriceBase float64
}
type diskSpec struct {
	SizeGB int    `json:"size_gb"`
	Type   string `json:"type"`
}
type diskFormRow struct {
	SizeGB string
	Type   string
}
type backupPayload struct {
	Version    int               `json:"version"`
	ExportedAt string            `json:"exported_at"`
	Settings   map[string]string `json:"settings,omitempty"`
	Domains    []backupDomain    `json:"domains"`
	VPS        []backupVPS       `json:"vps"`
}
type backupDomain struct {
	Domain       string  `json:"domain"`
	Registrar    string  `json:"registrar"`
	ExpiresAt    string  `json:"expires_at"`
	RenewalPrice float64 `json:"renewal_price"`
	Currency     string  `json:"currency"`
	Notes        string  `json:"notes"`
	ExcludeCost  int     `json:"exclude_cost,omitempty"`
	Pinned       int     `json:"pinned,omitempty"`
	Hidden       int     `json:"hidden,omitempty"`
	SortOrder    int     `json:"sort_order,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}
type backupVPS struct {
	Vendor         string  `json:"vendor"`
	IP             string  `json:"ip"`
	Specs          string  `json:"specs"`
	Region         string  `json:"region"`
	Cores          int     `json:"cores"`
	MemoryGB       float64 `json:"memory_gb"`
	DiskGB         int     `json:"disk_gb"`
	DiskType       string  `json:"disk_type"`
	Disk2GB        int     `json:"disk2_gb"`
	Disk2Type      string  `json:"disk2_type"`
	DisksJSON      string  `json:"disks_json"`
	TrafficTB      float64 `json:"traffic_tb"`
	TrafficSet     int     `json:"traffic_set"`
	TrafficDir     string  `json:"traffic_direction"`
	BandwidthValue float64 `json:"bandwidth_value"`
	BandwidthUnit  string  `json:"bandwidth_unit"`
	BandwidthMbps  int     `json:"bandwidth_mbps"`
	ExpiresAt      string  `json:"expires_at"`
	RenewalPrice   float64 `json:"renewal_price"`
	Currency       string  `json:"currency"`
	Notes          string  `json:"notes"`
	ExcludeCost    int     `json:"exclude_cost,omitempty"`
	Pinned         int     `json:"pinned,omitempty"`
	Hidden         int     `json:"hidden,omitempty"`
	BillingMonths  int     `json:"billing_months,omitempty"`
	SortOrder      int     `json:"sort_order,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}
type vpsRow struct {
	ID             int64
	Vendor         string
	IP             string
	Specs          string
	Region         string
	Cores          int
	MemoryGB       float64
	DiskGB         int
	DiskType       string
	Disk2GB        int
	Disk2Type      string
	Disks          []diskSpec
	TrafficTB      float64
	TrafficSet     bool
	TrafficDir     string
	BandwidthValue float64
	BandwidthUnit  string
	ExpiresAt      string
	IsPermanent    bool
	DaysLeft       int
	DaysBadgeClass string
	RenewalPrice   float64
	Currency       string
	Notes          string
	ExcludeCost    bool
	Pinned         bool
	Hidden         bool
	BillingMonths  int
	BillingLabel   string

	RenewalPriceBase float64
	AnnualPriceBase  float64
}
type fxInfo struct {
	EURToUSD      float64
	EURToCNY      float64
	EURToRUB      float64
	UpdatedAt     string
	RateDate      string
	AutoUpdate    bool
	Source        string
	LastAttemptAt string
	LastError     string
}
type dashboardVM struct {
	NowISO              string
	IsAdmin             bool
	PublicMode          bool
	FaviconURL          string
	Fx                  fxInfo
	BaseCurrency        string
	BaseSymbol          string
	ShowTotals          bool
	DomainShowRegistrar bool
	DomainShowExpiry    bool
	DomainShowPrice     bool
	DomainShowNotes     bool
	VPSShowIP           bool
	VPSShowRegion       bool
	VPSShowSpecs        bool
	VPSShowExpiry       bool
	VPSShowPrice        bool
	VPSShowNotes        bool
	Domains             []domainRow
	VPS                 []vpsRow
	DomainTotalBase     float64
	VPSTotalBase        float64
	GrandTotalBase      float64
	DomainExpiring30    int
	DomainExpiring90    int
	VPSExpiring30       int
	VPSExpiring90       int
	AllExpiring30Total  int
}
type loginVM struct {
	IsAdmin    bool
	PublicMode bool
	FaviconURL string
	Error      string
}
type settingsVM struct {
	IsAdmin                   bool
	PublicMode                bool
	FaviconURL                string
	Fx                        fxInfo
	PublicShowTotals          bool
	PublicShowDomainRegistrar bool
	PublicShowDomainExpiry    bool
	PublicShowDomainPrice     bool
	PublicShowDomainNotes     bool
	PublicShowVPSIP           bool
	PublicShowVPSRegion       bool
	PublicShowVPSSpecs        bool
	PublicShowVPSExpiry       bool
	PublicShowVPSPrice        bool
	PublicShowVPSNotes        bool
	WebDAVURL                 string
	WebDAVFile                string
	WebDAVUser                string
	WebDAVPass                string
	EmailEnabled              bool
	EmailDays                 string
	EmailProtocol             string
	EmailHost                 string
	EmailPort                 string
	EmailEncryption           string
	EmailUser                 string
	EmailPass                 string
	EmailFrom                 string
	EmailTo                   string
	Error                     string
	Notice                    string
}
type assetFormVM struct {
	IsAdmin    bool
	PublicMode bool
	FaviconURL string
	Kind       string
	Title      string
	Action     string
	Error      string
	Item       map[string]string
	DiskRows   []diskFormRow
}
type emailSettingsForm struct {
	Enabled       bool
	DaysBefore    []int
	DaysBeforeRaw string
	Protocol      string
	Host          string
	Port          string
	Encryption    string
	User          string
	Pass          string
	From          string
	To            string
}
type emailConfig struct {
	Protocol   string
	Host       string
	Port       int
	Encryption string
	Username   string
	Password   string
	From       mail.Address
	To         []mail.Address
}
type reminderItem struct {
	Type      string
	ID        int64
	Title     string
	ExpiresAt string
	Price     float64
	Currency  string
}
type loginAttempt struct {
	Failures     int
	FirstFailure time.Time
	LockedUntil  time.Time
	LastSeen     time.Time
}
type loginLimiter struct {
	mu          sync.Mutex
	maxFailures int
	window      time.Duration
	lockout     time.Duration
	maxEntries  int
	attempts    map[string]*loginAttempt
}
type ecbEnvelope struct {
	Cube struct {
		Cubes []struct {
			Time  string `xml:"time,attr"`
			Rates []struct {
				Currency string  `xml:"currency,attr"`
				Rate     float64 `xml:"rate,attr"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	} `xml:"Cube"`
}
type openERResponse struct {
	Result            string             `json:"result"`
	BaseCode          string             `json:"base_code"`
	TimeLastUpdateUTC string             `json:"time_last_update_utc"`
	Rates             map[string]float64 `json:"rates"`
	ErrorType         string             `json:"error-type"`
}
type sortRequest struct {
	IDs []int64 `json:"ids"`
}
type batchRequest struct {
	IDs    []int64 `json:"ids"`
	Action string  `json:"action"`
	Value  string  `json:"value,omitempty"`
}
type domainExportFilter struct {
	Scope     string
	Domain    string
	Registrar string
	Notes     string
	Page      int
	PageSize  int
}
type digestChallenge struct {
	Realm     string
	Nonce     string
	Qop       string
	Opaque    string
	Algorithm string
}
