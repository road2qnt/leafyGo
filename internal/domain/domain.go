package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID
	Username        string
	PasswordHash    string
	IsPlatformAdmin bool
	IsActive        bool
	CreatedAt       time.Time
}

type Group struct {
	ID          uuid.UUID
	Name        string
	Description string
	CreatedAt   time.Time
}

type DBConnection struct {
	ID          uuid.UUID
	Name        string
	Host        string
	Port        int
	DBName      string
	Username    string
	PasswordEnc []byte
	SSLMode     string
	CreatedBy   *uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AccessLevel int

const (
	LevelNone AccessLevel = iota
	LevelView
	LevelEdit
)

func ParseLevel(s string) AccessLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "view":
		return LevelView
	case "edit":
		return LevelEdit
	default:
		return LevelNone
	}
}

func (l AccessLevel) String() string {
	switch l {
	case LevelView:
		return "view"
	case LevelEdit:
		return "edit"
	default:
		return "none"
	}
}

func (l AccessLevel) Label() string {
	switch l {
	case LevelView:
		return "View Only"
	case LevelEdit:
		return "View + Edit"
	default:
		return "No access"
	}
}

func (l AccessLevel) CanView() bool { return l >= LevelView }
func (l AccessLevel) CanEdit() bool { return l >= LevelEdit }

type AccessibleDB struct {
	DBConnection
	Level AccessLevel
}

type GroupAccess struct {
	DBID   uuid.UUID
	DBName string
	Level  AccessLevel
}

const (
	MigrationRunning = "running"
	MigrationSuccess = "success"
	MigrationFailed  = "failed"
)

type MigrationHistory struct {
	ID             uuid.UUID
	DBID           uuid.UUID
	Version        int64
	SQLText        string
	ExecutedBy     *uuid.UUID
	StartedAt      time.Time
	FinishedAt     *time.Time
	Status         string
	Error          string
	DurationMS     *int
	ExecutedByName string
}

type MigrationDraft struct {
	ID            uuid.UUID
	DBID          uuid.UUID
	Title         string
	SQLText       string
	Revision      int
	CreatedBy     *uuid.UUID
	UpdatedBy     *uuid.UUID
	CreatedByName string
	UpdatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Session struct {
	IDHash            []byte
	UserID            uuid.UUID
	CSRFToken         string
	CreatedAt         time.Time
	LastSeenAt        time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	RotatedTo         []byte
	GraceExpiresAt    *time.Time
	RevokedAt         *time.Time
}
