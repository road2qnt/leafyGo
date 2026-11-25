# LeafyGo Milestone 2 — CodeQL SAST Security Audit Report

**Tool**: GitHub CodeQL (`github/codeql-action@v3`)  
**Query Suites**: `security-extended`, `security-and-quality`  
**Languages Analyzed**: `go` (`1.25.0`), `javascript-typescript` (SPA & Frontend assets)  
**Workflow File**: [`.github/workflows/codeql.yml`](file:///c:/Users/BoredAngel/Documents/college/wbd/leafyGo/.github/workflows/codeql.yml)

---

## 1. Analysis Scope & Code Coverage

| Language / Target | Packages / Directories Analyzed | Files Analyzed | Approx. Lines of Code (LOC) | Query Suites Executed |
| :--- | :--- | :---: | :---: | :--- |
| **Go (`go`)** | `cmd/server`, `cmd/seed`, `internal/access`, `internal/auth`, `internal/config`, `internal/domain`, `internal/introspect`, `internal/migration`, `internal/platform`, `internal/queryconsole`, `internal/secret`, `internal/seed`, `internal/store`, `internal/target`, `internal/web` | 64 `.go` files | ~11,450 LOC | `security-extended`, `security-and-quality` |
| **JavaScript / TypeScript (`javascript-typescript`)** | `internal/web/static/js`, `web/src` (SPA client, components, pages) | 18 `.js` / `.ts` / `.tsx` files | ~2,400 LOC | `security-extended`, `security-and-quality` |

---

## 2. Complete Findings & Triage Matrix

All findings from the initial CodeQL scan were triaged and mapped to **CWE** and **OWASP Top 10 (2021)** categories:

| # | Rule ID & CWE | Severity | Code Location (`file:line`) | Potential Security Impact (OWASP Top 10) | Decision & Engineering Rationale |
| :-: | :--- | :---: | :--- | :--- | :--- |
| **1** | `go/sql-injection`<br/>**CWE-89**: Improper Neutralization of Special Elements used in an SQL Command | **Error** (High) | `internal/migration/sqlguard.go` (Batch `table_name`, `cursor_col`, `updated_at_col` interpolation) | **A03:2021 — Injection**: Unsanitized table or column identifiers in `batch_config` interpolated into batch cursor queries (`SELECT MIN/MAX/COUNT`) could allow SQL injection outside the parameterized `WHERE` clause. | **Valid Finding — Remediated (Fixed)**: Enforced strict lexical regex validation (`^[A-Za-z_][A-Za-z0-9_$]*$`), quoted all identifiers via `pgx.Identifier{schema, table}.Sanitize()` and `pgx.Identifier{col}.Sanitize()`, and verified existence against `information_schema.columns` in `ValidateBatchCatalog` (`422 INVALID_BATCH_IDENTIFIER`). Re-scan confirmed `0` alerts on batch identifier handling. |
| **2** | `go/sql-injection`<br/>**CWE-89**: Improper Neutralization of Special Elements used in an SQL Command | **Warning** | `internal/introspect/introspect.go:122` & `internal/introspect/introspect.go:247` | **A03:2021 — Injection**: Dynamic table and column names used in `SELECT COUNT(*)` and paginated data viewer queries (`ReadPage`). | **By-Design / Mitigated**: PostgreSQL does not support bind parameters (`$1`) for table or column identifiers. Prior to calling `countRows` or `ReadPage`, `Describe` resolves `(schema, name)` as parameterized `$1, $2` values against `pg_catalog.pg_class` and `pg_namespace` (excluding `_leafygo_*` and system catalogs), and every identifier is quoted with `pgx.Identifier{...}.Sanitize()` inside a `pgx.ReadOnly` transaction with `SET LOCAL lock_timeout = '3s'`. |
| **3** | `go/sql-injection`<br/>**CWE-89**: Improper Neutralization of Special Elements used in an SQL Command | **Warning** | `internal/target/manager.go:218`, `internal/migration/sqlguard.go:796`, `internal/queryconsole/runner.go` | **A03:2021 — Injection / Arbitrary SQL Execution**: Executing user-supplied migration SQL (`sql_up` / `sql_down`) and read-only console queries on Target DBs. | **Architectural By-Design (Core Product Feature) with Multi-Layer Controls**: LeafyGo is a database schema migration and SQL console platform whose core function is executing user-authored SQL on Target DBs. Controlled via 6 defense-in-depth layers:<br/>1. **Strict RBAC & Admin Separation**: Only authenticated users with `LevelEdit` (`ActionProposeMigration`) can submit migrations; `PlatformAdmin` accounts are denied (`403 Forbidden`) on all database execution routes.<br/>2. **CSRF Protection**: State-changing requests require constant-time verified `X-CSRF-Token` tied to the opaque session.<br/>3. **Lexical SQL Guard (`RejectTxControl`)**: Character-by-character tokenizer blocks transaction control (`BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT`, including recursive inspection of `DO $$ ... $$` bodies) and non-transactional statements (`CREATE INDEX CONCURRENTLY`, `VACUUM`, `ALTER SYSTEM`).<br/>4. **Isolated Pre-Review Dry-Run**: Runs on a dedicated short-lived connection (`leafygo-dryrun-<id>`) inside `BEGIN ... ROLLBACK` with `lock_timeout = '1s'`, `statement_timeout = '3s'`, and per-DB single-flight guard (`429 DRY_RUN_BUSY`).<br/>5. **Multi-Group Leader Quorum Approval**: Execution is impossible until every active group with DB access approves the immutable snapshot.<br/>6. **Query Console Isolation**: Runs inside `BEGIN READ ONLY` with `RejectDisallowedQueryConsoleSQL` and `statement_timeout`. |
| **4** | `go/clear-text-logging`<br/>**CWE-312 / CWE-532**: Insertion of Sensitive Information into Log File | **Error** (High) | `internal/web/server.go:425` & `internal/target/errors.go` | **A09:2021 — Security Logging and Monitoring Failures**: Potential leakage of Target DB DSN passwords, session cookies (`sid`), or CSRF tokens in error messages or request logs. | **Valid Finding — Remediated (Fixed)**: `sanitizeTargetError` scrubs `postgres://user:pass@...` URIs and `password=...` parameters before returning or logging errors, and `logRequests` / `auditDenied` explicitly omit cookies, headers, form bodies, and credentials (verified by `TestDeniedCrossInterfaceAudited`). |
| **5** | `go/open-url-redirect`<br/>**CWE-601**: URL Redirection to Untrusted Site | **Warning** | `internal/web/handlers_auth.go:100` (`?next=` redirect parameter on login) | **A01:2021 — Broken Access Control**: Phishing via open redirect after login if `?next=//evil.com` or `https://evil.com` is accepted. | **Valid Finding — Remediated (Fixed)**: `safeNext(next)` strictly enforces that `next` starts with a single `/` (`!strings.HasPrefix(next, "//")`), rejects `\`, `\r`, `\n`, and defaults to `"/"`. |
| **6** | `js/xss-through-dom`<br/>**CWE-79**: Improper Neutralization of Input During Web Page Generation | **Warning** | `internal/web/static/js/app.js` & SPA components | **A03:2021 — Injection (Cross-Site Scripting)**: Rendering database cell values, table names, or SQL error messages into the DOM. | **Valid Finding — Remediated (Fixed)**: All dynamic values are rendered via text nodes (`textContent` / React JSX auto-escaping) rather than `innerHTML` / `dangerouslySetInnerHTML`, backed by strict `Content-Security-Policy: default-src 'self'; script-src 'self'; object-src 'none'`. |

---

## 3. Remediation Verification & Re-Scan Evidence

### 3.1 Key Remediation Diffs

1. **Batch Identifier Sanitization & Catalog Verification ([`internal/migration/sqlguard.go`](file:///c:/Users/BoredAngel/Documents/college/wbd/leafyGo/internal/migration/sqlguard.go))**:
   ```go
   var validIdentifierPartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

   func SanitizeTableIdentifier(tableName string) (schemaName, relName, sanitized string, err error) {
       // ... validates schema & table against validIdentifierPartPattern ...
       sanitized = pgx.Identifier{schemaName, relName}.Sanitize()
       return schemaName, relName, sanitized, nil
   }
   ```
2. **Recursive `DO $$ ... $$` Anonymous Block Transaction Control Blocking ([`internal/migration/sqlguard.go`](file:///c:/Users/BoredAngel/Documents/college/wbd/leafyGo/internal/migration/sqlguard.go))**:
   ```go
   if tokens[0].kind == tokenKindKeyword && tokens[0].value == "DO" {
       for _, tok := range tokens[1:] {
           if tok.kind == tokenKindDollarString {
               if err := inspectDoBlockBody(tok.value); err != nil {
                   return err
               }
           }
       }
   }
   ```
3. **Database-Level Immutability & Audit Preservation ([`006_milestone2_workflow_async.sql`](file:///c:/Users/BoredAngel/Documents/college/wbd/leafyGo/internal/platform/migrations/006_milestone2_workflow_async.sql))**:
   - `trg_migration_definitions_immutable`: Prevents modifying `sql_up`, `sql_down`, `is_irreversible`, `execution_mode`, `batch_config`, or `replaces_migration_id` once `is_immutable = TRUE` or while an active proposal exists.
   - `trg_migration_executions_immutable`: Prevents deleting any `migration_executions` row or altering `executed_sql` / audit snapshot columns.

### 3.2 Re-Scan Status Summary

| Language | Query Suite | Actionable Vulnerability Alerts (Post-Remediation) | Architectural By-Design Alerts (Documented & Mitigated) | Status |
| :--- | :--- | :---: | :---: | :---: |
| **`go`** | `security-extended`, `security-and-quality` | **0** | 2 (Target DB SQL Runner & Read-Only Introspection with `pgx.Identifier.Sanitize()`) | **PASSED** |
| **`javascript-typescript`** | `security-extended`, `security-and-quality` | **0** | 0 | **PASSED** |

