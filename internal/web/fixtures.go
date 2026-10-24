package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"leafygo/internal/domain"
)

// Fixture meniru seed-db untuk /_preview (UI_PREVIEW=true) dan test render.

var (
	fxAdmin = domain.User{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000a1"), Username: "admin", IsPlatformAdmin: true}
	fxBudi  = domain.User{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000b2"), Username: "budi"}
	fxAndi  = domain.User{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000a3"), Username: "andi"}

	fxToko  = domain.DBConnection{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000d1"), Name: "Toko Online", Host: "seed-db", Port: 5432, DBName: "toko", Username: "leafy_app", SSLMode: "disable"}
	fxSDM   = domain.DBConnection{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000d2"), Name: "SDM Internal", Host: "seed-db", Port: 5432, DBName: "sdm", Username: "leafy_app", SSLMode: "disable"}
	fxArsip = domain.DBConnection{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000d3"), Name: "Arsip Lama", Host: "arsip.invalid", Port: 5432, DBName: "arsip", Username: "leafy_app", SSLMode: "disable"}

	fxTargetError = "The database cannot be reached (host not found). Try again later."
)

func fxTime(day, hour, minute int) time.Time {
	return time.Date(2026, time.October, day, hour, minute, 0, 0, time.FixedZone("WIB", 7*3600))
}

func fxTimePtr(day, hour, minute int) *time.Time {
	t := fxTime(day, hour, minute)
	return &t
}

func fxInt(n int) *int       { return &n }
func fxInt64(n int64) *int64 { return &n }

func fxDBView(c domain.DBConnection) dbView {
	return dbView{ID: c.ID, Name: c.Name, Host: c.Host, Port: c.Port, DBName: c.DBName, Username: c.Username, SSLMode: c.SSLMode,
		CreatedAt: fxTime(1, 8, 0), UpdatedAt: fxTime(20, 10, 15)}
}

func fxUserOptions() []userOption {
	return []userOption{
		{ID: fxAndi.ID, Username: "andi"},
		{ID: fxBudi.ID, Username: "budi"},
		{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000c4"), Username: "citra"},
		{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000d5"), Username: "dewi"},
		{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000e6"), Username: "eko"},
	}
}

var (
	fxGroupAnalyst = groupOption{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000f1"), Name: "analyst"}
	fxGroupBackend = groupOption{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000f2"), Name: "backend"}
)

func fxAdminUsers(form formState, activeAdmins int) adminUsersView {
	groups := map[string][]groupOption{
		"andi": {fxGroupAnalyst}, "budi": {fxGroupBackend}, "citra": {fxGroupAnalyst, fxGroupBackend},
	}
	users := []adminUserRow{{ID: fxAdmin.ID, Username: "admin", IsPlatformAdmin: true, IsActive: true, CreatedAt: fxTime(1, 8, 0)}}
	if activeAdmins > 1 {
		users = append(users, adminUserRow{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000a9"), Username: "ega", IsPlatformAdmin: true, IsActive: true, CreatedAt: fxTime(1, 9, 0)})
	}
	for i, o := range fxUserOptions() {
		users = append(users, adminUserRow{ID: o.ID, Username: o.Username, IsActive: o.Username != "eko", Groups: groups[o.Username], CreatedAt: fxTime(2+i, 9, 30)})
	}
	return adminUsersView{Users: users, ActiveAdmins: activeAdmins, Form: form}
}

func fxAdminUserEdit(username string, activeAdmins int, form formState) adminUsersView {
	v := fxAdminUsers(form, activeAdmins)
	for i := range v.Users {
		if v.Users[i].Username == username {
			v.Editing = &v.Users[i]
		}
	}
	return v
}

func fxAdminDatabases() adminDatabasesView {
	return adminDatabasesView{Databases: []dbView{fxDBView(fxArsip), fxDBView(fxSDM), fxDBView(fxToko)}}
}

func fxAdminGroups() adminGroupsView {
	opts := fxUserOptions()
	return adminGroupsView{
		Groups: []adminGroupRow{
			{
				ID: fxGroupAnalyst.ID, Name: fxGroupAnalyst.Name, Description: "Data team, read only.",
				Members: []userOption{opts[0], opts[2]},
				Access: []groupAccessRow{
					{DBID: fxArsip.ID, DBName: fxArsip.Name, Level: domain.LevelView},
					{DBID: fxSDM.ID, DBName: fxSDM.Name, Level: domain.LevelView},
					{DBID: fxToko.ID, DBName: fxToko.Name, Level: domain.LevelView},
				},
			},
			{
				ID: fxGroupBackend.ID, Name: fxGroupBackend.Name, Description: "Store app developers.",
				Members: []userOption{opts[1], opts[2]},
				Access:  []groupAccessRow{{DBID: fxToko.ID, DBName: fxToko.Name, Level: domain.LevelEdit}},
			},
			{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000f3"), Name: "auditor", Description: "New group, not configured yet."},
		},
	}
}

func fxAdminGroupEdit(i int) adminGroupView {
	g := fxAdminGroups().Groups[i]
	dbs := []dbOption{{ID: fxArsip.ID, Name: fxArsip.Name}, {ID: fxSDM.ID, Name: fxSDM.Name}, {ID: fxToko.ID, Name: fxToko.Name}}
	v := adminGroupView{Group: g, Form: formState{Values: groupFormValues(g)}}
	v.Users, v.Databases = groupChoices(g, fxUserOptions(), dbs)
	return v
}

func fxAdminDatabaseEdit(errs map[string]string, override map[string]string) adminDatabaseFormView {
	editing := fxDBView(fxSDM)
	values := connFormValues(editing)
	for k, val := range override {
		values[k] = val
	}
	return adminDatabaseFormView{Editing: &editing, Form: formState{Values: values, Errors: errs}}
}

func fxHistory() []historyRow {
	return []historyRow{
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000105"), Version: 5, Status: historyRunning, ExecutedByName: "citra", StartedAt: fxTime(24, 21, 2)},
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000104"), Version: 4, Status: historySuccess, ExecutedByName: "citra", StartedAt: fxTime(23, 10, 0), FinishedAt: fxTimePtr(23, 10, 0), DurationMs: fxInt(184)},
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000103"), Version: 3, Status: historyFailed, ExecutedByName: "budi", StartedAt: fxTime(22, 14, 40), FinishedAt: fxTimePtr(22, 14, 40), DurationMs: fxInt(37),
			Error: `ERROR: column "stok" does not exist (SQLSTATE 42703)`},
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000102"), Version: 2, Status: historyFailed, ExecutedByName: "budi", StartedAt: fxTime(21, 9, 0), FinishedAt: fxTimePtr(21, 9, 0),
			Error: "server stopped during execution; target state unknown, check manually"},
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000101"), Version: 1, Status: historySuccess, ExecutedByName: "", StartedAt: fxTime(20, 9, 0), FinishedAt: fxTimePtr(20, 9, 1), DurationMs: fxInt(1240)},
	}
}

var fxHistorySQL = map[int64]string{
	4: "create index orders_status_ordered_idx on orders (status, ordered_at desc);",
	3: "alter table products add column discount_pct numeric(5,2) not null default 0;\nalter table products alter column stok type bigint;",
}

func fxDrafts() []draftRow {
	return []draftRow{
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000201"), Title: "add discount column to products", Revision: 3, CreatedByName: "budi", UpdatedByName: "citra", CreatedAt: fxTime(23, 16, 5), UpdatedAt: fxTime(24, 9, 12)},
		{ID: uuid.MustParse("00000000-0000-4000-8000-000000000202"), Revision: 1, CreatedByName: "budi", UpdatedByName: "budi", CreatedAt: fxTime(24, 8, 0), UpdatedAt: fxTime(24, 8, 0)},
	}
}

const fxDraftSQL = `alter table products
    add column discount_pct numeric(5,2) not null default 0
        check (discount_pct between 0 and 100);

create index products_discount_idx on products (discount_pct)
    where discount_pct > 0;`

const fxExplainPlan = `Index Scan using orders_status_ordered_idx on orders  (cost=0.15..18.42 rows=50 width=52)
  Index Cond: (status = 'paid'::text)`

func fxTables() []tableInfo {
	return []tableInfo{
		{Schema: "analytics", Name: "daily_sales", Kind: "r", EstimatedRows: fxInt64(42), CanSelect: true},
		{Schema: "public", Name: "categories", Kind: "r", EstimatedRows: fxInt64(5), CanSelect: true},
		{Schema: "public", Name: "customers", Kind: "r", EstimatedRows: fxInt64(40), CanSelect: true},
		{Schema: "public", Name: "order_items", Kind: "r", EstimatedRows: fxInt64(390), CanSelect: true},
		{Schema: "public", Name: "order_totals", Kind: "v", CanSelect: true},
		{Schema: "public", Name: "orders", Kind: "r", EstimatedRows: fxInt64(1200), CanSelect: true},
		{Schema: "public", Name: "products", Kind: "r", EstimatedRows: fxInt64(120), CanSelect: true},
		{Schema: "public", Name: "supplier_contracts", Kind: "r", EstimatedRows: fxInt64(12), CanSelect: false},
	}
}

func fxOrdersDetail() tableDetail {
	return tableDetail{
		tableInfo:  tableInfo{Schema: "public", Name: "orders", Kind: "r", EstimatedRows: fxInt64(1200), CanSelect: true},
		PrimaryKey: []string{"id"},
		Columns: []columnInfo{
			{Name: "id", DataType: "integer", Default: "nextval('orders_id_seq'::regclass)", IsPK: true, Ordinal: 1},
			{Name: "customer_id", DataType: "integer", Ordinal: 2},
			{Name: "status", DataType: "text", Ordinal: 3},
			{Name: "ordered_at", DataType: "timestamp with time zone", Default: "now()", Ordinal: 4},
			{Name: "note", DataType: "text", Nullable: true, Ordinal: 5},
		},
		Constraints: []constraintInfo{
			{Name: "orders_customer_id_fkey", Type: "f", Definition: "FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE CASCADE"},
			{Name: "orders_pkey", Type: "p", Definition: "PRIMARY KEY (id)"},
			{Name: "orders_status_check", Type: "c", Definition: "CHECK (status = ANY (ARRAY['pending', 'paid', 'shipped', 'cancelled']))"},
			{Name: "orders_status_not_null", Type: "n", Definition: "NOT NULL status"},
		},
		Indexes: []indexInfo{
			{Name: "orders_customer_idx", Definition: "CREATE INDEX orders_customer_idx ON public.orders USING btree (customer_id)"},
			{Name: "orders_pkey", Unique: true, Primary: true, Definition: "CREATE UNIQUE INDEX orders_pkey ON public.orders USING btree (id)"},
			{Name: "orders_status_ordered_idx", Definition: "CREATE INDEX orders_status_ordered_idx ON public.orders USING btree (status, ordered_at DESC)"},
		},
	}
}

func fxViewDetail() tableDetail {
	return tableDetail{
		tableInfo: tableInfo{Schema: "public", Name: "order_totals", Kind: "v", CanSelect: true},
		Columns: []columnInfo{
			{Name: "order_id", DataType: "integer", Nullable: true, Ordinal: 1},
			{Name: "customer", DataType: "text", Nullable: true, Ordinal: 2},
			{Name: "status", DataType: "text", Nullable: true, Ordinal: 3},
			{Name: "total", DataType: "numeric", Nullable: true, Ordinal: 4},
		},
	}
}

func fxProductsPage(page int) *dataPage {
	names := []string{"Bayam", "Kangkung", "Apel", "Mangga", "Kunyit", "Jahe", "Teh Hijau", "Kopi", "Keripik", "Kacang"}
	grades := []string{"Organik", "Segar", "Premium", "Lokal"}
	const pageSize = 50
	var rows [][]dataCell
	for i := 1; i <= pageSize && (page-1)*pageSize+i <= 120; i++ {
		g := (page-1)*pageSize + i
		note := dataCell{IsNull: true}
		switch {
		case g%4 == 0:
			note = dataCell{Text: "limited stock"}
		case g%9 == 0:
			note = dataCell{Text: ""}
		case g == 7:
			note = dataCell{Text: strings.Repeat("deskripsi panjang ", 20)}
		}
		rows = append(rows, []dataCell{
			{Text: fmt.Sprint(g)},
			{Text: fmt.Sprintf("SKU-%04d", g)},
			{Text: fmt.Sprintf("%s %s #%d", names[g%10], grades[g%4], g)},
			{Text: fmt.Sprint(1 + g%5)},
			{Text: fmt.Sprintf("%d.00", 5000+(g*1375)%95000)},
			{Text: fmt.Sprint((g * 7) % 150)},
			{Text: fmt.Sprint(g%11 != 0)},
			{Text: `{"warna":"hijau"}`},
			note,
		})
	}
	return &dataPage{
		Schema: "public", Table: "products",
		Columns:       []string{"id", "sku", "name", "category_id", "price", "stock", "is_active", "attrs", "note"},
		Rows:          rows,
		Page:          page,
		PageSize:      pageSize,
		HasNext:       page*pageSize < 120,
		EstimatedRows: fxInt64(120),
	}
}

type previewPage struct {
	Key   string
	Label string
	Page  string
	Build func() pageData
}

type previewGroup struct {
	Name  string
	Title string
	Items []previewPage
}

func dbPage(title, nav string, conn domain.DBConnection, user domain.User, level domain.AccessLevel, data any) pageData {
	c, u := conn, user
	return pageData{Title: title + " · " + c.Name, Nav: nav, DB: &c, Level: level, User: &u, Data: data}
}

func adminPage(title, nav string, data any) pageData {
	u := fxAdmin
	return pageData{Title: title, Nav: nav, User: &u, Data: data}
}

func previewGroups() []previewGroup {
	invalidUser := formState{
		Values: map[string]string{"username": "budi santoso", "is_admin": "true"},
		Errors: map[string]string{"username": "Usernames may only contain letters, digits, dots, underscores, or hyphens.", "password": "Password must be at least 8 characters."},
	}
	invalidConn := formState{
		Values: map[string]string{"name": "Toko Online", "host": "seed-db", "port": "5432", "dbname": "toko", "username": "leafy_app", "sslmode": "require"},
		Errors: map[string]string{"form": "A connection with this name already exists.", "name": "Name is already taken.", "password": "Password is required."},
	}
	history := fxHistory()
	drafts := fxDrafts()
	detail := func(i int) *historyDetail {
		h := history[i]
		return &historyDetail{historyRow: h, SQL: fxHistorySQL[h.Version]}
	}
	editor := func(v draftEditorView) func() pageData {
		return func() pageData {
			return dbPage("Drafts", navDBDrafts, fxToko, fxBudi, domain.LevelEdit, v)
		}
	}

	return []previewGroup{
		{Name: "umum", Title: "General", Items: []previewPage{
			{"login", "Login", "auth/login.html", func() pageData {
				return pageData{Title: "Sign in", NoHeader: true, Data: map[string]string{"Next": "/"}, Flash: &flashMessage{Kind: "error", Message: "Incorrect username or password."}}
			}},
			{"dashboard", "User dashboard", "dashboard/dashboard.html", func() pageData {
				u := fxBudi
				return pageData{Title: "Databases", Nav: navDatabases, User: &u, Data: dashboardData{Databases: []domain.AccessibleDB{
					{DBConnection: fxArsip, Level: domain.LevelView}, {DBConnection: fxSDM, Level: domain.LevelView}, {DBConnection: fxToko, Level: domain.LevelEdit},
				}}}
			}},
			{"dashboard-empty", "Dashboard without access", "dashboard/dashboard.html", func() pageData {
				u := domain.User{ID: uuid.New(), Username: "eko"}
				return pageData{Title: "Databases", Nav: navDatabases, User: &u, Data: dashboardData{}}
			}},
			{"error-admin-db", "403 admin on /databases", "system/error.html", func() pageData {
				return adminPage("Error", "", nil).with(func(p *pageData) { p.Message = "Admin accounts cannot access user utilities." })
			}},
			{"error-target", "502 target unreachable", "system/error.html", func() pageData {
				u := fxAndi
				return pageData{Title: "Error", User: &u, Message: fxTargetError}
			}},
		}},
		{Name: "admin", Title: "Platform Admin", Items: []previewPage{
			{"admin-users", "Users", "admin/users.html", func() pageData {
				return adminPage("Users", navAdminUsers, fxAdminUsers(formState{}, 2))
			}},
			{"admin-users-last-admin", "Users, last active admin", "admin/users.html", func() pageData {
				return adminPage("Users", navAdminUsers, fxAdminUserEdit("admin", 1, formState{}))
			}},
			{"admin-users-invalid", "Users, invalid form", "admin/users.html", func() pageData {
				return adminPage("Users", navAdminUsers, fxAdminUsers(invalidUser, 2))
			}},
			{"admin-users-edit", "Users, managing active user", "admin/users.html", func() pageData {
				return adminPage("Users", navAdminUsers, fxAdminUserEdit("andi", 2, formState{}))
			}},
			{"admin-users-edit-inactive", "Users, managing inactive user", "admin/users.html", func() pageData {
				return adminPage("Users", navAdminUsers, fxAdminUserEdit("eko", 2, formState{}))
			}},
			{"admin-users-edit-admin", "Users, managing another admin", "admin/users.html", func() pageData {
				return adminPage("Users", navAdminUsers, fxAdminUserEdit("ega", 2, formState{}))
			}},
			{"admin-users-edit-self", "Users, managing yourself", "admin/users.html", func() pageData {
				return adminPage("Users", navAdminUsers, fxAdminUserEdit("admin", 2, formState{}))
			}},
			{"admin-users-edit-invalid", "Users, invalid password reset", "admin/users.html", func() pageData {
				return adminPage("Users", navAdminUsers, fxAdminUserEdit("andi", 2, formState{Errors: map[string]string{"password": "Password must be at least 8 characters."}}))
			}},
			{"admin-groups", "Groups and access", "admin/groups.html", func() pageData {
				return adminPage("Groups", navAdminGroups, fxAdminGroups())
			}},
			{"admin-groups-empty", "Groups, none yet", "admin/groups.html", func() pageData {
				return adminPage("Groups", navAdminGroups, adminGroupsView{})
			}},
			{"admin-group-new", "Create group", "admin/group_new.html", func() pageData {
				return adminPage("Create group", navAdminGroups, adminGroupNewView{})
			}},
			{"admin-group-new-invalid", "Create group, invalid", "admin/group_new.html", func() pageData {
				return adminPage("Create group", navAdminGroups, adminGroupNewView{Form: formState{
					Values: map[string]string{"name": "analyst"},
					Errors: map[string]string{"name": "A group with this name already exists."},
				}})
			}},
			{"admin-group-edit", "Group analyst", "admin/group_edit.html", func() pageData {
				return adminPage("analyst", navAdminGroups, fxAdminGroupEdit(0))
			}},
			{"admin-group-edit-new", "Group auditor, not configured", "admin/group_edit.html", func() pageData {
				return adminPage("auditor", navAdminGroups, fxAdminGroupEdit(2))
			}},
			{"admin-group-edit-invalid", "Group backend, invalid details", "admin/group_edit.html", func() pageData {
				v := fxAdminGroupEdit(1)
				v.Form = formState{
					Values: map[string]string{"name": "analyst", "description": v.Group.Description},
					Errors: map[string]string{"name": "A group with this name already exists."},
				}
				return adminPage("backend", navAdminGroups, v)
			}},
			{"admin-databases", "Connections", "admin/databases.html", func() pageData {
				return adminPage("Connections", navAdminDatabases, fxAdminDatabases()).with(func(p *pageData) {
					p.Flash = &flashMessage{Kind: "error", Message: "Connection test for Arsip Lama failed: host not found."}
				})
			}},
			{"admin-databases-empty", "Connections, none yet", "admin/databases.html", func() pageData {
				return adminPage("Connections", navAdminDatabases, adminDatabasesView{})
			}},
			{"admin-database-new", "Add connection", "admin/database_form.html", func() pageData {
				return adminPage("Add connection", navAdminDatabases, adminDatabaseFormView{})
			}},
			{"admin-database-new-invalid", "Add connection, invalid", "admin/database_form.html", func() pageData {
				return adminPage("Add connection", navAdminDatabases, adminDatabaseFormView{Form: invalidConn})
			}},
			{"admin-database-edit", "Edit connection", "admin/database_form.html", func() pageData {
				return adminPage("SDM Internal", navAdminDatabases, fxAdminDatabaseEdit(nil, nil))
			}},
			{"admin-database-edit-invalid", "Edit connection, invalid", "admin/database_form.html", func() pageData {
				return adminPage("SDM Internal", navAdminDatabases, fxAdminDatabaseEdit(
					map[string]string{"port": "Port must be between 1 and 65535."},
					map[string]string{"host": "sdm.internal", "port": "70000"},
				))
			}},
		}},
		{Name: "viewer", Title: "Database viewer", Items: []previewPage{
			{"viewer-overview", "Overview", "viewer/overview.html", func() pageData {
				return dbPage("Overview", navDBOverview, fxToko, fxBudi, domain.LevelEdit, overviewView{DB: fxDBView(fxToko), TableCount: 8, LatestMigration: &history[0]})
			}},
			{"viewer-overview-failed", "Overview, target unreachable", "viewer/overview.html", func() pageData {
				return dbPage("Overview", navDBOverview, fxArsip, fxAndi, domain.LevelView, overviewView{DB: fxDBView(fxArsip), TargetError: fxTargetError})
			}},
			{"viewer-schema", "Schema, table list", "viewer/schema.html", func() pageData {
				return dbPage("Schema", navDBSchema, fxToko, fxAndi, domain.LevelView, schemaView{Tables: fxTables()})
			}},
			{"viewer-schema-table", "Schema, table detail", "viewer/schema.html", func() pageData {
				d := fxOrdersDetail()
				return dbPage("Schema", navDBSchema, fxToko, fxAndi, domain.LevelView, schemaView{Detail: &d})
			}},
			{"viewer-schema-view", "Schema, view detail", "viewer/schema.html", func() pageData {
				d := fxViewDetail()
				return dbPage("Schema", navDBSchema, fxToko, fxAndi, domain.LevelView, schemaView{Detail: &d})
			}},
			{"viewer-schema-empty", "Schema, empty", "viewer/schema.html", func() pageData {
				return dbPage("Schema", navDBSchema, fxSDM, fxAndi, domain.LevelView, schemaView{})
			}},
			{"viewer-data-pick", "Data, choose table", "viewer/data.html", func() pageData {
				return dbPage("Data", navDBData, fxToko, fxAndi, domain.LevelView, dataView{Tables: fxTables()})
			}},
			{"viewer-data", "Data, page 1", "viewer/data.html", func() pageData {
				return dbPage("Data", navDBData, fxToko, fxAndi, domain.LevelView, dataView{Page: fxProductsPage(1)})
			}},
			{"viewer-data-last", "Data, last page", "viewer/data.html", func() pageData {
				return dbPage("Data", navDBData, fxToko, fxAndi, domain.LevelView, dataView{Page: fxProductsPage(3)})
			}},
			{"viewer-data-view", "Data, view without stable order", "viewer/data.html", func() pageData {
				p := &dataPage{Schema: "public", Table: "order_totals", Columns: []string{"order_id", "customer", "status", "total"},
					Rows: [][]dataCell{{{Text: "12"}, {Text: "Ayu Lestari"}, {Text: "paid"}, {Text: "48250.00"}}, {{Text: "13"}, {Text: "Bayu Wijaya"}, {Text: "shipped"}, {Text: "125000.00"}}},
					Page: 1, PageSize: 50, OrderNote: "This view has no primary key, so row order across pages is not guaranteed to be stable."}
				return dbPage("Data", navDBData, fxToko, fxAndi, domain.LevelView, dataView{Page: p})
			}},
			{"viewer-data-noselect", "Data, no SELECT permission", "viewer/data.html", func() pageData {
				return dbPage("Data", navDBData, fxToko, fxAndi, domain.LevelView, dataView{Tables: fxTables(), Notice: "The database credentials have no SELECT permission on public.supplier_contracts."})
			}},
		}},
		{Name: "migration", Title: "Migration", Items: []previewPage{
			{"migrations-edit", "History, View + Edit", "migration/history.html", func() pageData {
				return dbPage("Migration", navDBMigrations, fxToko, fxBudi, domain.LevelEdit, migrationsView{Items: history, DraftCount: len(drafts)})
			}},
			{"migrations-view", "History, View Only", "migration/history.html", func() pageData {
				return dbPage("Migration", navDBMigrations, fxToko, fxAndi, domain.LevelView, migrationsView{Items: history})
			}},
			{"migrations-detail-success", "History, success detail", "migration/history.html", func() pageData {
				return dbPage("Migration", navDBMigrations, fxToko, fxAndi, domain.LevelView, migrationsView{Items: history, Selected: detail(1)})
			}},
			{"migrations-detail-failed", "History, failure detail", "migration/history.html", func() pageData {
				return dbPage("Migration", navDBMigrations, fxToko, fxBudi, domain.LevelEdit, migrationsView{Items: history, Selected: detail(2), DraftCount: len(drafts)})
			}},
			{"migrations-empty", "History, empty", "migration/history.html", func() pageData {
				return dbPage("Migration", navDBMigrations, fxSDM, fxBudi, domain.LevelEdit, migrationsView{})
			}},
			{"drafts", "Draft list", "migration/drafts.html", func() pageData {
				return dbPage("Drafts", navDBDrafts, fxToko, fxBudi, domain.LevelEdit, draftsView{Drafts: drafts})
			}},
			{"drafts-empty", "Draft list, empty", "migration/drafts.html", func() pageData {
				return dbPage("Drafts", navDBDrafts, fxSDM, fxBudi, domain.LevelEdit, draftsView{})
			}},
			{"draft-new", "New draft", "migration/draft_new.html", func() pageData {
				return dbPage("New draft", navDBDrafts, fxToko, fxBudi, domain.LevelEdit, draftNewView{})
			}},
			{"draft-new-invalid", "New draft, empty SQL", "migration/draft_new.html", func() pageData {
				return dbPage("New draft", navDBDrafts, fxToko, fxBudi, domain.LevelEdit, draftNewView{Title: "add vouchers table",
					Form: formState{Errors: map[string]string{"sql": "SQL must not be empty."}}})
			}},
			{"draft-new-explain", "New draft, EXPLAIN result", "migration/draft_new.html", func() pageData {
				return dbPage("New draft", navDBDrafts, fxToko, fxBudi, domain.LevelEdit, draftNewView{
					SQL: "select * from orders where status = 'paid';", Explain: &explainView{Plan: fxExplainPlan}})
			}},
			{"draft-editor", "Draft editor", "migration/draft_editor.html", editor(draftEditorView{Draft: drafts[0], SQL: fxDraftSQL})},
			{"draft-editor-explain", "Editor, EXPLAIN result", "migration/draft_editor.html", editor(draftEditorView{
				Draft: drafts[0], SQL: "select * from orders where status = 'paid';", Explain: &explainView{Plan: fxExplainPlan},
			})},
			{"draft-editor-explain-ddl", "Editor, EXPLAIN on DDL", "migration/draft_editor.html", editor(draftEditorView{
				Draft: drafts[0], SQL: fxDraftSQL, Explain: &explainView{Error: "EXPLAIN only works on a single SELECT, INSERT, UPDATE, DELETE, MERGE, VALUES, or WITH statement. It does not apply to DDL."},
			})},
			{"draft-editor-failed", "Editor, execution failed", "migration/draft_editor.html", editor(draftEditorView{
				Draft:        draftRow{ID: drafts[0].ID, Title: drafts[0].Title, Revision: 4, CreatedByName: "budi", UpdatedByName: "budi", UpdatedAt: fxTime(24, 21, 30)},
				SQL:          "alter table products add column discount_pct numeric(5,2) not null default 0;\nalter table products alter column stok type bigint;",
				Failure:      &historyRow{ID: history[2].ID, Version: 3, Status: historyFailed, Error: history[2].Error},
				SQLErrorLine: 2,
			})},
			{"draft-editor-conflict", "Editor, revision conflict", "migration/draft_editor.html", editor(draftEditorView{
				Draft: drafts[0], SQL: fxDraftSQL,
				Form: formState{Errors: map[string]string{"form": "Someone else changed this draft since you opened it. Reload the page and apply your changes again."}},
			})},
			{"draft-editor-txcontrol", "Editor, SQL contains COMMIT", "migration/draft_editor.html", editor(draftEditorView{
				Draft: drafts[1], SQL: "begin;\nupdate products set stock = 0 where is_active = false;\ncommit;",
				Form:         formState{Errors: map[string]string{"sql": "Statement 1 starts with BEGIN. Transactions are managed for you; remove BEGIN, COMMIT, or ROLLBACK."}},
				SQLErrorLine: 1,
			})},
		}},
	}
}

func (p pageData) with(f func(*pageData)) pageData {
	f(&p)
	return p
}

func (s *Server) handlePreviewIndex(w http.ResponseWriter, r *http.Request) {
	u := fxAdmin
	s.render(w, r, http.StatusOK, "system/preview_index.html", pageData{Title: "Preview", User: &u, CSRF: "preview", Data: previewGroups()})
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	for _, g := range previewGroups() {
		for _, p := range g.Items {
			if p.Key != key {
				continue
			}
			data := p.Build()
			data.Preview = true
			if data.CSRF == "" {
				data.CSRF = "preview"
			}
			s.render(w, r, http.StatusOK, p.Page, data)
			return
		}
	}
	s.renderError(w, r, http.StatusNotFound, "Preview not found.")
}
