package web

import (
	"encoding/base64"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

const testCSRF = "csrf-fixture"

func renderRecorder(req *http.Request, page string, data pageData) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	(&Server{logger: testLogger()}).render(rec, req, http.StatusOK, page, data)
	return rec
}

func renderOK(t *testing.T, page string, data pageData) string {
	t.Helper()
	if data.CSRF == "" {
		data.CSRF = testCSRF
	}
	rec := renderRecorder(httptest.NewRequest(http.MethodGet, "/", nil), page, data)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", page, rec.Code)
	}
	body := rec.Body.String()
	if !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
		t.Fatalf("%s: template execution stopped early", page)
	}
	return body
}

func allPreviews() []previewPage {
	var out []previewPage
	for _, g := range previewGroups() {
		out = append(out, g.Items...)
	}
	return out
}

func renderPreview(t *testing.T, key string) string {
	t.Helper()
	for _, p := range allPreviews() {
		if p.Key == key {
			return renderOK(t, p.Page, p.Build())
		}
	}
	t.Fatalf("preview %q not found", key)
	return ""
}

type element struct {
	tag   string
	attrs map[string]string
}

func (e element) has(attr string) bool {
	_, ok := e.attrs[attr]
	return ok
}

var (
	tagRe  = regexp.MustCompile(`<([a-z][a-z0-9]*)\b((?:[^>"]|"[^"]*")*)>`)
	attrRe = regexp.MustCompile(`([a-zA-Z_:-]+)(?:="([^"]*)")?`)
	formRe = regexp.MustCompile(`(?s)<form\b.*?</form>`)
	cellRe = regexp.MustCompile(`(?s)<td\b[^>]*>(.*?)</td>`)
	markRe = regexp.MustCompile(`<[^>]+>`)
)

// Atribut start tag, cukup untuk output template sendiri.
func elements(body string, tags ...string) []element {
	var out []element
	for _, m := range tagRe.FindAllStringSubmatch(body, -1) {
		if len(tags) > 0 && !slices.Contains(tags, m[1]) {
			continue
		}
		attrs := map[string]string{}
		for _, a := range attrRe.FindAllStringSubmatch(m[2], -1) {
			attrs[a[1]] = html.UnescapeString(a[2])
		}
		out = append(out, element{tag: m[1], attrs: attrs})
	}
	return out
}

func forms(body string) []string { return formRe.FindAllString(body, -1) }

func formAction(form string) string { return elements(form, "form")[0].attrs["action"] }

// Semua URL yang bisa di-POST dari halaman, termasuk formaction.
func postTargets(body string) []string {
	var out []string
	for _, f := range forms(body) {
		out = append(out, formAction(f))
		for _, b := range elements(f, "button") {
			if v, ok := b.attrs["formaction"]; ok {
				out = append(out, v)
			}
		}
	}
	slices.Sort(out)
	return out
}

func linkTargets(body string) []string {
	var out []string
	for _, a := range elements(body, "a") {
		out = append(out, a.attrs["href"])
	}
	return append(out, postTargets(body)...)
}

func inputNamed(body, name string) (element, bool) {
	for _, e := range elements(body, "input", "textarea", "select") {
		if e.attrs["name"] == name {
			return e, true
		}
	}
	return element{}, false
}

func TestPreviewsRenderValidDocuments(t *testing.T) {
	for _, p := range allPreviews() {
		t.Run(p.Key, func(t *testing.T) {
			body := renderOK(t, p.Page, p.Build())
			all := elements(body)

			if n := len(elements(body, "h1")); n != 1 {
				t.Errorf("want exactly one h1, got %d", n)
			}
			ids := map[string]bool{}
			for _, e := range all {
				if id, ok := e.attrs["id"]; ok {
					if ids[id] {
						t.Errorf("duplicate id %q", id)
					}
					ids[id] = true
				}
			}
			for _, e := range all {
				var refs []string
				for _, attr := range []string{"aria-describedby", "aria-labelledby", "aria-controls"} {
					refs = append(refs, strings.Fields(e.attrs[attr])...)
				}
				if e.tag == "label" && e.has("for") {
					refs = append(refs, e.attrs["for"])
				}
				if href := e.attrs["href"]; e.tag == "a" && strings.HasPrefix(href, "#") {
					refs = append(refs, strings.TrimPrefix(href, "#"))
				}
				for _, ref := range refs {
					if !ids[ref] {
						t.Errorf("<%s> references missing id %q", e.tag, ref)
					}
				}
			}
		})
	}
}

func TestPostFormsCarryCSRFToken(t *testing.T) {
	for _, p := range allPreviews() {
		t.Run(p.Key, func(t *testing.T) {
			data := p.Build()
			data.CSRF = testCSRF
			for _, f := range forms(renderOK(t, p.Page, data)) {
				if method := elements(f, "form")[0].attrs["method"]; method != "post" {
					t.Errorf("form %s uses method %q", formAction(f), method)
					continue
				}
				token, ok := inputNamed(f, "csrf_token")
				if !ok || token.attrs["type"] != "hidden" || token.attrs["value"] != testCSRF {
					t.Errorf("form %s lacks the session CSRF token", formAction(f))
				}
			}
		})
	}
}

func TestPasswordsAreNeverRendered(t *testing.T) {
	for _, p := range allPreviews() {
		t.Run(p.Key, func(t *testing.T) {
			body := renderOK(t, p.Page, p.Build())
			for _, e := range elements(body, "input") {
				if e.attrs["type"] == "password" && e.attrs["value"] != "" {
					t.Errorf("password input %q is prefilled", e.attrs["name"])
				}
			}
			for _, secret := range []string{"password_enc", "PasswordEnc", "postgres://"} {
				if strings.Contains(body, secret) {
					t.Errorf("page leaks %q", secret)
				}
			}
		})
	}

	const secret = "Sup3r-Secret!"
	submitted := formState{
		Values: map[string]string{"username": "budi", "password": secret, "name": "Toko", "host": "seed-db"},
		Errors: map[string]string{"form": "Something went wrong."},
	}
	cases := map[string]struct {
		page string
		data any
	}{
		"create user":       {"admin/users.html", fxAdminUsers(submitted, 2)},
		"reset password":    {"admin/users.html", fxAdminUserEdit("andi", 2, submitted)},
		"create connection": {"admin/database_form.html", adminDatabaseFormView{Form: submitted}},
		"edit connection":   {"admin/database_form.html", fxAdminDatabaseEdit(submitted.Errors, submitted.Values)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(renderOK(t, c.page, adminPage("Form", "", c.data)), secret) {
				t.Error("submitted password echoed back into the page")
			}
		})
	}
}

func TestUserContentIsEscaped(t *testing.T) {
	const payload = `<script>alert("x")</script>`
	body := renderOK(t, "dashboard/dashboard.html", pageData{
		Title: payload,
		User:  &domain.User{ID: uuid.New(), Username: payload},
		Data: dashboardData{Databases: []domain.AccessibleDB{{
			DBConnection: domain.DBConnection{ID: uuid.New(), Name: payload, DBName: payload},
			Level:        domain.LevelView,
		}}},
	})
	if strings.Contains(body, "<script>alert") {
		t.Fatal("user controlled text rendered as markup")
	}
	if strings.Count(body, "&lt;script&gt;") < 4 {
		t.Fatal("title, username, connection name, and database name should all be escaped")
	}
}

func TestLinksStayWithinRole(t *testing.T) {
	for _, p := range allPreviews() {
		t.Run(p.Key, func(t *testing.T) {
			data := p.Build()
			admin := data.User != nil && data.User.IsPlatformAdmin
			for _, link := range linkTargets(renderOK(t, p.Page, data)) {
				if admin && strings.HasPrefix(link, "/databases") {
					t.Errorf("admin page links to database utility %q", link)
				}
				if !admin && strings.HasPrefix(link, "/admin") {
					t.Errorf("user page links to admin route %q", link)
				}
			}
		})
	}
}

func TestDatabasePagesGateEditControlsByLevel(t *testing.T) {
	for _, p := range allPreviews() {
		data := p.Build()
		if data.DB == nil {
			continue
		}
		t.Run(p.Key, func(t *testing.T) {
			drafts := "/databases/" + data.DB.ID.String() + "/migrations/drafts"
			var editLinks []string
			for _, link := range linkTargets(renderOK(t, p.Page, data)) {
				if strings.Contains(link, "/migrations/new") || strings.Contains(link, "/migrations/drafts") {
					editLinks = append(editLinks, link)
				}
			}
			if data.Level.CanEdit() && !slices.Contains(editLinks, drafts) {
				t.Error("edit level should reach the drafts tab")
			}
			if !data.Level.CanEdit() && len(editLinks) > 0 {
				t.Errorf("view level exposes edit routes %v", editLinks)
			}
		})
	}
}

func TestDraftFormsPostToDraftRoutes(t *testing.T) {
	base := "/databases/" + fxToko.ID.String() + "/migrations/drafts"
	draft := base + "/" + fxDrafts()[0].ID.String()
	cases := []struct {
		key         string
		action      string
		formactions []string
		revision    string
	}{
		{"draft-new", base, []string{base + "/execute", base + "/explain"}, ""},
		{"draft-editor", draft, []string{draft + "/delete", draft + "/execute", draft + "/explain"}, "3"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			var form string
			for _, f := range forms(renderPreview(t, c.key)) {
				if formAction(f) == c.action {
					form = f
				}
			}
			if form == "" {
				t.Fatalf("no form posts to %s", c.action)
			}
			var got []string
			for _, b := range elements(form, "button") {
				if v, ok := b.attrs["formaction"]; ok {
					got = append(got, v)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, c.formactions) {
				t.Errorf("formactions = %v, want %v", got, c.formactions)
			}
			if confirm, ok := inputNamed(form, "confirm"); !ok || !confirm.has("required") {
				t.Error("execute must require the confirmation checkbox")
			}
			token, ok := inputNamed(form, "token")
			if c.revision == "" && ok {
				t.Error("new draft has no revision to send")
			}
			if c.revision != "" && token.attrs["value"] != c.revision {
				t.Errorf("revision token = %q, want %q", token.attrs["value"], c.revision)
			}
		})
	}
}

func TestUserStatusControls(t *testing.T) {
	ega := uuid.MustParse("00000000-0000-4000-8000-0000000000a9")
	eko := fxUserOptions()[4].ID
	cases := []struct {
		key        string
		user       uuid.UUID
		wantActive string
	}{
		{"admin-users", uuid.Nil, ""},
		{"admin-users-edit", fxAndi.ID, "false"},
		{"admin-users-edit-inactive", eko, "true"},
		{"admin-users-edit-admin", ega, "false"},
		{"admin-users-edit-self", fxAdmin.ID, ""},
		{"admin-users-last-admin", fxAdmin.ID, ""},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			var got []string
			for _, f := range forms(renderPreview(t, c.key)) {
				if !strings.HasSuffix(formAction(f), "/active") {
					continue
				}
				if want := "/admin/users/" + c.user.String() + "/active"; formAction(f) != want {
					t.Errorf("status form targets %s, want %s", formAction(f), want)
				}
				active, _ := inputNamed(f, "active")
				got = append(got, active.attrs["value"])
			}
			var want []string
			if c.wantActive != "" {
				want = []string{c.wantActive}
			}
			if !slices.Equal(got, want) {
				t.Errorf("status forms set active=%v, want %v", got, want)
			}
		})
	}
}

func TestConnectionFormRoutes(t *testing.T) {
	edit := "/admin/databases/" + fxSDM.ID.String()
	cases := []struct {
		key  string
		want []string
	}{
		{"admin-database-new-invalid", []string{"/admin/databases", "/logout"}},
		{"admin-database-edit-invalid", []string{edit, edit + "/delete", edit + "/test", "/logout"}},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			if got := postTargets(renderPreview(t, c.key)); !slices.Equal(got, c.want) {
				t.Errorf("post targets = %v, want %v", got, c.want)
			}
		})
	}

	body := renderPreview(t, "admin-database-edit-invalid")
	for name, want := range map[string]string{"host": "sdm.internal", "port": "70000", "dbname": "sdm"} {
		if e, _ := inputNamed(body, name); e.attrs["value"] != want {
			t.Errorf("%s refilled with %q, want %q", name, e.attrs["value"], want)
		}
	}
	if port, _ := inputNamed(body, "port"); port.attrs["aria-invalid"] != "true" {
		t.Error("invalid port should be marked aria-invalid")
	}
}

func TestDataCellsDistinguishNullEmptyAndTruncated(t *testing.T) {
	long := strings.Repeat("é", 250)
	page := &dataPage{
		Schema: "public", Table: "t", Columns: []string{"a", "b", "c", "d"},
		Rows: [][]dataCell{{{IsNull: true}, {Text: "NULL"}, {Text: ""}, {Text: long}}},
		Page: 1, PageSize: 50,
	}
	body := renderOK(t, "viewer/data.html", dbPage("Data", navDBData, fxToko, fxAndi, domain.LevelView, dataView{Page: page}))
	cells := cellRe.FindAllStringSubmatch(body, -1)
	if len(cells) != 4 {
		t.Fatalf("want 4 cells, got %d", len(cells))
	}
	text := func(i int) string { return html.UnescapeString(markRe.ReplaceAllString(cells[i][1], "")) }

	if cells[0][1] == cells[1][1] {
		t.Error("SQL NULL and the string 'NULL' render identically")
	}
	if got := text(2); !strings.Contains(got, "empty string") {
		t.Errorf("empty string cell reads %q", got)
	}
	if got, want := text(3), strings.Repeat("é", 200)+"…"; got != want {
		t.Errorf("long cell not truncated to 200 characters: %d runes", len([]rune(got)))
	}
}

func TestFlashCookie(t *testing.T) {
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name     string
		cookie   string
		wantRole string
		wantText string
	}{
		{"error is announced as alert", "error|" + b64("Wrong password"), "alert", "Wrong password"},
		{"success is announced as status", "success|" + b64("Saved"), "status", "Saved"},
		{"message is escaped", "error|" + b64("<b>hi</b>"), "alert", "&lt;b&gt;hi&lt;/b&gt;"},
		{"missing separator", "error", "", ""},
		{"invalid base64", "error|***", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.AddCookie(&http.Cookie{Name: "flash", Value: c.cookie})
			u := fxBudi
			rec := renderRecorder(req, "system/error.html", pageData{Title: "Error", User: &u, Message: "x"})
			body := rec.Body.String()

			var flashes []element
			for _, e := range elements(body) {
				if e.has("data-flash") {
					flashes = append(flashes, e)
				}
			}
			switch {
			case c.wantRole == "" && len(flashes) > 0:
				t.Error("malformed cookie rendered a flash")
			case c.wantRole != "" && len(flashes) != 1:
				t.Fatalf("want one flash, got %d", len(flashes))
			case c.wantRole != "":
				if flashes[0].attrs["role"] != c.wantRole {
					t.Errorf("role = %q, want %q", flashes[0].attrs["role"], c.wantRole)
				}
				if !strings.Contains(body, c.wantText) {
					t.Errorf("flash text %q missing", c.wantText)
				}
			}

			cleared := false
			for _, ck := range rec.Result().Cookies() {
				cleared = cleared || (ck.Name == "flash" && ck.MaxAge < 0)
			}
			if !cleared {
				t.Error("flash cookie should be cleared once read")
			}
		})
	}
}

func TestRenderUnknownPage(t *testing.T) {
	rec := renderRecorder(httptest.NewRequest(http.MethodGet, "/", nil), "missing.html", pageData{})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

func TestPageLinks(t *testing.T) {
	est := func(n int64) *int64 { return &n }
	cases := []struct {
		page, size int
		hasNext    bool
		est        *int64
		want       string
	}{
		{1, 50, false, nil, "[1]"},
		{1, 50, true, nil, "[1] 2"},
		{1, 50, true, est(1000), "[1] 2 3 … 20"},
		{3, 50, true, est(1000), "1 2 [3] 4 5 … 20"},
		{10, 50, true, est(1000), "1 … 8 9 [10] 11 12 … 20"},
		{20, 50, false, est(1000), "1 … 18 19 [20]"},
		{4, 50, true, est(10), "1 2 3 [4] 5"},
		{1, 0, true, est(1000), "[1] 2"},
	}
	for _, c := range cases {
		var parts []string
		for _, l := range pageLinks(c.page, c.size, c.hasNext, c.est) {
			switch {
			case l.Gap:
				parts = append(parts, "…")
			case l.Current:
				parts = append(parts, fmt.Sprintf("[%d]", l.Number))
			default:
				parts = append(parts, fmt.Sprint(l.Number))
			}
		}
		if got := strings.Join(parts, " "); got != c.want {
			t.Errorf("pageLinks(%d, %d, %v): got %q, want %q", c.page, c.size, c.hasNext, got, c.want)
		}
	}
}

func TestSQLLineAt(t *testing.T) {
	sql := "alter table a add column b int;\nalter table a alter column stok type bigint;"
	cases := []struct{ pos, want int }{
		{-1, 0}, {0, 0}, {1, 1}, {31, 1}, {32, 1}, {33, 2}, {len(sql), 2}, {len(sql) + 1, 0},
	}
	for _, c := range cases {
		if got := sqlLineAt(sql, c.pos); got != c.want {
			t.Errorf("sqlLineAt(%d) = %d, want %d", c.pos, got, c.want)
		}
	}
	if got := sqlLineAt("é\nx", 3); got != 2 {
		t.Errorf("position should count characters, not bytes: got line %d", got)
	}
}

func TestFormatters(t *testing.T) {
	at := fxTime(24, 21, 2)
	var nilTime *time.Time
	var nilInt *int
	var nilInt64 *int64
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"int zero", formatInt(0), "0"},
		{"int below thousand", formatInt(999), "999"},
		{"int thousand", formatInt(int64(1000)), "1,000"},
		{"int million", formatInt(fxInt64(1234567)), "1,234,567"},
		{"int negative", formatInt(-1234567), "-1,234,567"},
		{"int nil", formatInt(nilInt64), "-"},
		{"int unsupported", formatInt("12"), "-"},
		{"duration ms", formatDuration(999), "999 ms"},
		{"duration seconds", formatDuration(fxInt(1240)), "1.2 s"},
		{"duration type", formatDuration(1500 * time.Millisecond), "1.5 s"},
		{"duration nil", formatDuration(nilInt), "-"},
		{"time", formatTime(at), "24 Oct 2026 21:02"},
		{"time pointer", formatTime(&at), "24 Oct 2026 21:02"},
		{"time zero", formatTime(time.Time{}), "-"},
		{"time nil", formatTime(nilTime), "-"},
		{"iso time", formatISOTime(at), "2026-10-24T21:02:00+07:00"},
		{"iso time nil", formatISOTime(nilTime), ""},
		{"truncate short", truncate("abc", 3), "abc"},
		{"truncate long", truncate("abcd", 3), "abc…"},
		{"truncate runes", truncate("éééé", 2), "éé…"},
		{"count one", countLabel(1, "group", "groups"), "1 group"},
		{"count zero", countLabel(0, "group", "groups"), "0 groups"},
		{"count many", countLabel(3, "group", "groups"), "3 groups"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestDict(t *testing.T) {
	m, err := dict("a", 1, "b", "x")
	if err != nil || m["a"] != 1 || m["b"] != "x" || len(m) != 2 {
		t.Fatalf("dict = %v, %v", m, err)
	}
	if _, err := dict("a"); err == nil {
		t.Error("odd argument count should fail")
	}
	if _, err := dict(1, "a"); err == nil {
		t.Error("non string key should fail")
	}
}

func TestGroupChoicesExcludeExisting(t *testing.T) {
	andi, budi := userOption{ID: fxAndi.ID, Username: "andi"}, userOption{ID: fxBudi.ID, Username: "budi"}
	toko, sdm := dbOption{ID: fxToko.ID, Name: fxToko.Name}, dbOption{ID: fxSDM.ID, Name: fxSDM.Name}
	g := adminGroupRow{
		Members: []userOption{andi},
		Access:  []groupAccessRow{{DBID: toko.ID, DBName: toko.Name, Level: domain.LevelEdit}},
	}
	users, dbs := groupChoices(g, []userOption{andi, budi}, []dbOption{toko, sdm})
	if !slices.Equal(users, []userOption{budi}) {
		t.Errorf("user choices = %v, want only budi", users)
	}
	if !slices.Equal(dbs, []dbOption{sdm}) {
		t.Errorf("database choices = %v, want only SDM", dbs)
	}
}

func TestAccessNamesSplitByLevel(t *testing.T) {
	g := adminGroupRow{Access: []groupAccessRow{
		{DBName: "a", Level: domain.LevelView},
		{DBName: "b", Level: domain.LevelEdit},
		{DBName: "c", Level: domain.LevelView},
	}}
	if got := g.AccessNames(true); !slices.Equal(got, []string{"b"}) {
		t.Errorf("edit = %v", got)
	}
	if got := g.AccessNames(false); !slices.Equal(got, []string{"a", "c"}) {
		t.Errorf("view = %v", got)
	}
}

func TestAdminUsersViewCounts(t *testing.T) {
	v := adminUsersView{Users: []adminUserRow{{IsPlatformAdmin: true}, {}, {IsPlatformAdmin: true, IsActive: true}, {}, {}}}
	if v.AdminCount() != 2 || v.UserCount() != 3 {
		t.Errorf("admins = %d, users = %d, want 2 and 3", v.AdminCount(), v.UserCount())
	}
}

func TestConnFormValuesOmitPassword(t *testing.T) {
	v := connFormValues(fxDBView(fxToko))
	if _, ok := v["password"]; ok {
		t.Error("password must never be part of the refill values")
	}
	if v["port"] != "5432" || v["host"] != "seed-db" {
		t.Errorf("values = %v", v)
	}
}

func TestIconFilesExist(t *testing.T) {
	refs := []*regexp.Regexp{
		regexp.MustCompile(`icons/([a-z-]+)\.svg`),
		regexp.MustCompile(`template "icon" "([a-z-]+)"`),
	}
	names := map[string]bool{}
	for _, root := range []string{"templates", "static"} {
		err := fs.WalkDir(assets, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(path, ".woff2") || strings.HasSuffix(path, ".png") {
				return err
			}
			b, err := fs.ReadFile(assets, path)
			if err != nil {
				return err
			}
			for _, ref := range refs {
				for _, m := range ref.FindAllStringSubmatch(string(b), -1) {
					names[m[1]] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(names) < 10 {
		t.Fatalf("found only %d icon references, the patterns are likely stale", len(names))
	}
	for name := range names {
		if _, err := fs.Stat(assets, "static/icons/"+name+".svg"); err != nil {
			t.Errorf("icon %q has no file in static/icons", name)
		}
	}
}
