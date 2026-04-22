package model

type ProviderStatus string

const (
	ProviderStatusUnknown  ProviderStatus = "unknown"
	ProviderStatusHealthy  ProviderStatus = "healthy"
	ProviderStatusDegraded ProviderStatus = "degraded"
	ProviderStatusError    ProviderStatus = "error"
	ProviderStatusDisabled ProviderStatus = "disabled"
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

type PlaybackMode string

const (
	PlaybackModeRedirect PlaybackMode = "redirect"
	PlaybackModeProxy    PlaybackMode = "proxy"
)

type Provider struct {
	ID          string
	Type        string
	Name        string
	RootPath    string
	Status      ProviderStatus
	LastCheckAt string
	LastError   string
	ConfigJSON  string
	Enabled     bool
	CreatedAt   string
	UpdatedAt   string
}

type ProviderSecret struct {
	ProviderID  string
	SecretType  string
	SecretValue string
	MaskedValue string
	UpdatedAt   string
}

type Library struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
	LastScanAt  string
	CreatedAt   string
	UpdatedAt   string
}

type LibraryMount struct {
	ID         string
	LibraryID  string
	ProviderID string
	SourcePath string
	TargetPath string
	MediaType  string
	Priority   int
	Enabled    bool
	CreatedAt  string
	UpdatedAt  string
}

type Setting struct {
	Key       string
	ValueJSON string
	UpdatedAt string
}

type AdminUser struct {
	ID           string
	Username     string
	PasswordHash string
	Role         string
	Enabled      bool
	LastLoginAt  string
	CreatedAt    string
	UpdatedAt    string
}

type AdminSession struct {
	Token      string
	UserID     string
	ExpiresAt  string
	LastSeenAt string
	CreatedAt  string
}

type Entry struct {
	ID          string
	ProviderID  string
	EntryType   string
	Path        string
	ParentPath  string
	Name        string
	Size        int64
	MTime       string
	MimeType    string
	ContentHash string
	LastSeenAt  string
	CreatedAt   string
	UpdatedAt   string
}

type DirectLinkCache struct {
	ProviderID    string
	Path          string
	URL           string
	HeadersJSON   string
	SupportsRange bool
	ExpireAt      string
	UpdatedAt     string
}

type ScanTask struct {
	ID            string
	TaskType      string
	LibraryID     string
	Status        TaskStatus
	ProgressTotal int
	ProgressDone  int
	Message       string
	ErrorMessage  string
	StartedAt     string
	FinishedAt    string
	CreatedAt     string
	UpdatedAt     string
}

type TaskLog struct {
	ID          string
	TaskID      string
	Level       string
	Message     string
	PayloadJSON string
	CreatedAt   string
}

type PlaybackLog struct {
	ID           string
	ProviderID   string
	Path         string
	Mode         PlaybackMode
	Client       string
	UserAgent    string
	StatusCode   int
	DurationMS   int
	RemoteAddr   string
	ErrorMessage string
	CreatedAt    string
}

type SystemEvent struct {
	ID          string
	EventType   string
	Level       string
	Source      string
	Message     string
	PayloadJSON string
	CreatedAt   string
}
