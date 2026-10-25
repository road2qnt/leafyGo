package web

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

// Kontrak data template (pageData.Data).
// Jangan masukkan password, DSN, atau session.

const (
	navDatabases      = "databases"
	navAdminUsers     = "admin-users"
	navAdminGroups    = "admin-groups"
	navAdminDatabases = "admin-databases"
	navDBOverview     = "db-overview"
	navDBSchema       = "db-schema"
	navDBData         = "db-data"
	navDBMigrations   = "db-migrations"
	navDBDrafts       = "db-drafts"
)

// Isi ulang form. Key = name input, "form" untuk pesan umum.
type formState struct {
	Values map[string]string
	Errors map[string]string
}

type dbView struct {
	ID        uuid.UUID
	Name      string
	Host      string
	Port      int
	DBName    string
	Username  string
	SSLMode   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Admin

// Editing terisi saat ?edit=<id>, Form milik reset password.
type adminUsersView struct {
	Users        []adminUserRow
	Editing      *adminUserRow
	ActiveAdmins int
	Form         formState
}

// Jumlah untuk caption: user biasa dan semua admin.
func (v adminUsersView) AdminCount() int {
	n := 0
	for _, u := range v.Users {
		if u.IsPlatformAdmin {
			n++
		}
	}
	return n
}

func (v adminUsersView) UserCount() int { return len(v.Users) - v.AdminCount() }

type adminUserRow struct {
	ID              uuid.UUID
	Username        string
	IsPlatformAdmin bool
	IsActive        bool
	Groups          []groupOption
	CreatedAt       time.Time
}

type groupOption struct {
	ID   uuid.UUID
	Name string
}

type adminGroupsView struct {
	Groups []adminGroupRow
}

// Halaman /admin/groups/new.
type adminGroupNewView struct {
	Form formState
}

// Halaman /admin/groups/{id}. Form milik form detail, Users dan Databases dari groupChoices.
type adminGroupView struct {
	Group     adminGroupRow
	Users     []userOption
	Databases []dbOption
	Form      formState
}

type adminGroupRow struct {
	ID          uuid.UUID
	Name        string
	Description string
	Members     []userOption
	Access      []groupAccessRow
}

// Nama DB yang aksesnya edit (true) atau view only (false).
func (g adminGroupRow) AccessNames(edit bool) []string {
	var names []string
	for _, a := range g.Access {
		if a.Level.CanEdit() == edit {
			names = append(names, a.DBName)
		}
	}
	return names
}

// Nilai awal form detail group.
func groupFormValues(g adminGroupRow) map[string]string {
	return map[string]string{"name": g.Name, "description": g.Description}
}

// Pilihan add member dan grant access yang belum dipakai g.
func groupChoices(g adminGroupRow, users []userOption, dbs []dbOption) ([]userOption, []dbOption) {
	members := make(map[uuid.UUID]bool, len(g.Members))
	for _, m := range g.Members {
		members[m.ID] = true
	}
	granted := make(map[uuid.UUID]bool, len(g.Access))
	for _, a := range g.Access {
		granted[a.DBID] = true
	}
	var u []userOption
	for _, o := range users {
		if !members[o.ID] {
			u = append(u, o)
		}
	}
	var d []dbOption
	for _, o := range dbs {
		if !granted[o.ID] {
			d = append(d, o)
		}
	}
	return u, d
}

type userOption struct {
	ID       uuid.UUID
	Username string
}

type dbOption struct {
	ID   uuid.UUID
	Name string
}

// Sama dengan domain.GroupAccess.
type groupAccessRow struct {
	DBID   uuid.UUID
	DBName string
	Level  domain.AccessLevel
}

// Hasil test connection lewat flash.
type adminDatabasesView struct {
	Databases []dbView
}

// Halaman /admin/databases/new (Editing nil) dan /admin/databases/{id}.
type adminDatabaseFormView struct {
	Editing *dbView
	Form    formState
}

// "1 group", "3 groups".
func countLabel(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// Nilai awal form edit. Password tidak pernah diisi ulang.
func connFormValues(d dbView) map[string]string {
	return map[string]string{
		"name":     d.Name,
		"host":     d.Host,
		"port":     strconv.Itoa(d.Port),
		"dbname":   d.DBName,
		"username": d.Username,
		"sslmode":  d.SSLMode,
	}
}

// Viewer. Mirror tipe introspect.

type tableInfo struct {
	Schema        string
	Name          string
	Kind          string // r, p, v, m, f
	EstimatedRows *int64
	CanSelect     bool
}

type tableDetail struct {
	tableInfo
	Columns     []columnInfo
	Constraints []constraintInfo
	Indexes     []indexInfo
	PrimaryKey  []string
}

type columnInfo struct {
	Name     string
	DataType string
	Nullable bool
	Default  string
	IsPK     bool
	Ordinal  int
}

type constraintInfo struct {
	Name       string
	Type       string // pg_constraint.contype
	Definition string
}

type indexInfo struct {
	Name       string
	Unique     bool
	Primary    bool
	Definition string
}

type dataCell struct {
	Text   string
	IsNull bool
}

type dataPage struct {
	Schema        string
	Table         string
	Columns       []string
	Rows          [][]dataCell
	Page          int
	PageSize      int
	HasNext       bool
	EstimatedRows *int64
	OrderNote     string
}

// TargetError terisi bila target tidak terjangkau (halaman tetap 200).
type overviewView struct {
	DB              dbView
	TableCount      int
	LatestMigration *historyRow
	TargetError     string
}

// Detail nil berarti mode daftar tabel.
type schemaView struct {
	Tables []tableInfo
	Detail *tableDetail
}

// Page nil = mode pilih tabel. Notice untuk pesan CanSelect=false.
type dataView struct {
	Tables []tableInfo
	Page   *dataPage
	Notice string
}

// Migration

const (
	historyRunning = "running"
	historySuccess = "success"
	historyFailed  = "failed"
)

type historyRow struct {
	ID             uuid.UUID
	Version        int64
	Status         string
	ExecutedByName string
	StartedAt      time.Time
	FinishedAt     *time.Time
	DurationMs     *int
	Error          string
}

func toHistoryRow(h domain.MigrationHistory) historyRow {
	return historyRow{
		ID: h.ID, Version: h.Version, Status: h.Status, ExecutedByName: h.ExecutedByName,
		StartedAt: h.StartedAt, FinishedAt: h.FinishedAt, DurationMs: h.DurationMS, Error: h.Error,
	}
}

type historyDetail struct {
	historyRow
	SQL string
}

// Selected terisi saat ?migration=<id>. DraftCount untuk level edit.
type migrationsView struct {
	Items      []historyRow
	Selected   *historyDetail
	DraftCount int
}

type draftRow struct {
	ID            uuid.UUID
	Title         string
	Revision      int
	CreatedByName string
	UpdatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type draftsView struct {
	Drafts []draftRow
}

// Explain terisi setelah POST explain (SQL belum disimpan).
// SQLErrorLine 1-based, 0 = tidak ada.
type draftNewView struct {
	Title        string
	SQL          string
	Form         formState
	Explain      *explainView
	SQLErrorLine int
}

// Failure terisi saat execute gagal (422).
type draftEditorView struct {
	Draft        draftRow
	SQL          string
	Form         formState
	Explain      *explainView
	Failure      *historyRow
	SQLErrorLine int
}

// Baris 1-based dari posisi karakter 1-based (PgError.Position), 0 jika di luar sql.
func sqlLineAt(sql string, pos int) int {
	if pos < 1 {
		return 0
	}
	line, i := 1, 0
	for _, r := range sql {
		i++
		if i == pos {
			return line
		}
		if r == '\n' {
			line++
		}
	}
	return 0
}

type explainView struct {
	Plan  string
	Error string
}

// Fungsi template

func statusLabel(status string) string {
	switch status {
	case historyRunning:
		return "Running"
	case historySuccess:
		return "Success"
	case historyFailed:
		return "Failed"
	default:
		return status
	}
}

func relkindLabel(kind string) string {
	switch kind {
	case "r":
		return "Table"
	case "p":
		return "Partitioned table"
	case "v":
		return "View"
	case "m":
		return "Materialized view"
	case "f":
		return "Foreign table"
	default:
		return kind
	}
}

func constraintLabel(kind string) string {
	switch kind {
	case "p":
		return "PRIMARY KEY"
	case "f":
		return "FOREIGN KEY"
	case "u":
		return "UNIQUE"
	case "c":
		return "CHECK"
	case "x":
		return "EXCLUDE"
	case "n":
		return "NOT NULL"
	case "t":
		return "TRIGGER"
	default:
		return kind
	}
}

func derefTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, !t.IsZero()
	case *time.Time:
		if t == nil {
			return time.Time{}, false
		}
		return *t, !t.IsZero()
	default:
		return time.Time{}, false
	}
}

func formatTime(v any) string {
	t, ok := derefTime(v)
	if !ok {
		return "-"
	}
	return t.Format("02 Jan 2006 15:04")
}

func formatISOTime(v any) string {
	t, ok := derefTime(v)
	if !ok {
		return ""
	}
	return t.Format(time.RFC3339)
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case *int:
		if n == nil {
			return 0, false
		}
		return int64(*n), true
	case *int64:
		if n == nil {
			return 0, false
		}
		return *n, true
	default:
		return 0, false
	}
}

func formatDuration(v any) string {
	var ms int64
	if d, ok := v.(time.Duration); ok {
		ms = d.Milliseconds()
	} else if n, ok := toInt64(v); ok {
		ms = n
	} else {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	return fmt.Sprintf("%.1f s", float64(ms)/1000)
}

func formatInt(v any) string {
	n, ok := toInt64(v)
	if !ok {
		return "-"
	}
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errors.New("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			return nil, errors.New("dict: keys must be strings")
		}
		m[key] = kv[i+1]
	}
	return m, nil
}

type pageLink struct {
	Number  int
	Current bool
	Gap     bool
}

// Halaman terakhir dari estimasi baris, bisa meleset.
func pageLinks(page, pageSize int, hasNext bool, estimated *int64) []pageLink {
	last := page
	if hasNext {
		last = page + 1
		if estimated != nil && pageSize > 0 {
			if est := int((*estimated + int64(pageSize) - 1) / int64(pageSize)); est > last {
				last = est
			}
		}
	}
	var out []pageLink
	gap := false
	for n := 1; n <= last; n++ {
		if n == 1 || n == last || (n >= page-2 && n <= page+2) {
			out = append(out, pageLink{Number: n, Current: n == page})
			gap = false
		} else if !gap {
			out = append(out, pageLink{Gap: true})
			gap = true
		}
	}
	return out
}
