import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, Plus, Trash2 } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";

import { isApiError } from "../../api/client";
import { adminApi } from "../../api/endpoints";
import type { Database } from "../../api/types";
import {
  Button,
  EmptyState,
  ErrorBanner,
  Field,
  PageHeader,
  PageLoader,
  Pagination,
  Select,
  TableWrap,
  TextInput,
} from "../../components/ui";
import { usePagination } from "../../hooks/usePagination";
import { useToast } from "../../hooks/useToast";
import { formatDateTime } from "../../lib/format";

interface DatabaseForm {
  name: string;
  host: string;
  port: string;
  db_name: string;
  username: string;
  password: string;
  ssl_mode: string;
}

const emptyForm: DatabaseForm = {
  name: "",
  host: "",
  port: "5432",
  db_name: "",
  username: "",
  password: "",
  ssl_mode: "disable",
};

function DatabasesListView() {
  const toast = useToast();
  const databasesQuery = useQuery({
    queryKey: ["admin", "databases"],
    queryFn: adminApi.databases,
  });

  const [testingId, setTestingId] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<Record<string, { ok: boolean; text: string }>>({});

  const databases = databasesQuery.data?.databases ?? [];
  const connectionsPage = usePagination(databases);

  if (databasesQuery.isLoading) return <PageLoader label="Loading connections" />;

  const testConnection = async (database: Database) => {
    setTestingId(database.id);
    try {
      const result = await adminApi.testDatabase(database.id);
      if (result.ok) {
        setTestResult((current) => ({
          ...current,
          [database.id]: { ok: true, text: `Connected in ${result.latency_ms ?? "?"} ms` },
        }));
        toast.success(
          "Connection OK",
          `${database.name} answered in ${result.latency_ms ?? "?"} ms.`,
        );
      } else {
        setTestResult((current) => ({
          ...current,
          [database.id]: { ok: false, text: result.error ?? "Connection failed" },
        }));
        toast.error("Connection failed", result.error ?? "The target did not answer.");
      }
    } catch (error) {
      toast.error(
        "Connection test failed",
        isApiError(error) ? error.message : "Unexpected error.",
      );
    } finally {
      setTestingId(null);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Connections"
        actions={
          databases.length > 0 ? (
            <Link to="/admin/databases/new" className="btn btn-primary">
              <Plus className="h-4 w-4" /> Add connection
            </Link>
          ) : null
        }
      />

      {databasesQuery.isError ? (
        <ErrorBanner
          error={databasesQuery.error}
          onRetry={() => void databasesQuery.refetch()}
        />
      ) : null}

      <section aria-labelledby="conn-caption">
        {databases.length > 0 ? (
          <div>
            <TableWrap>
              <table className="table-base table-stack">
                <caption id="conn-caption">
                  {databases.length} {databases.length === 1 ? "connection" : "connections"}
                </caption>
                <thead>
                  <tr>
                    <th scope="col" className="th">Name</th>
                    <th scope="col" className="th">Endpoint</th>
                    <th scope="col" className="th text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {connectionsPage.pageItems.map((database) => {
                    const status = testResult[database.id];
                    return (
                      <tr key={database.id}>
                        <th scope="row" data-label="Name" className="td cell-title">
                          <Link
                            to={`/admin/databases/${database.id}`}
                            className="font-semibold text-leaf-800 hover:text-leaf-900"
                          >
                            {database.name}
                          </Link>
                        </th>
                        <td data-label="Endpoint" className="td cell-wide">
                          <div>
                            <span className="font-mono text-sm break-all text-ink">
                              {database.username}@{database.host}:{database.port}/{database.db_name}
                            </span>
                            <span className="mt-1 block text-xs text-muted">
                              SSL {database.ssl_mode} · updated {formatDateTime(database.updated_at)}
                            </span>
                            {status ? (
                              <span
                                className={`mt-1 inline-block text-xs font-semibold uppercase ${
                                  status.ok ? "text-leaf-700" : "text-crimson"
                                }`}
                              >
                                {status.text}
                              </span>
                            ) : null}
                          </div>
                        </td>
                        <td data-label="Actions" className="td cell-actions w-[1%] text-right whitespace-nowrap">
                          <div className="inline-flex items-center justify-end gap-2">
                            <Button
                              variant="secondary"
                              size="sm"
                              loading={testingId === database.id}
                              onClick={() => void testConnection(database)}
                            >
                              Test connection<span className="sr-only"> {database.name}</span>
                            </Button>
                            <Link
                              to={`/admin/databases/${database.id}`}
                              className="btn btn-sm"
                            >
                              Edit<span className="sr-only"> {database.name}</span>
                            </Link>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </TableWrap>
            <Pagination label="Connections" {...connectionsPage.controls} />
          </div>
        ) : (
          <EmptyState
            title="No connections yet"
            description="Register the first PostgreSQL database so groups can be granted access to it."
            action={
              <Link to="/admin/databases/new" className="btn btn-primary">
                <Plus className="h-4 w-4" /> Add connection
              </Link>
            }
          />
        )}
      </section>
    </div>
  );
}

function DatabaseFormView({ dbId }: { dbId?: string }) {
  const isEditing = Boolean(dbId);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();

  const databasesQuery = useQuery({
    queryKey: ["admin", "databases"],
    queryFn: adminApi.databases,
    enabled: isEditing,
  });

  const editing: Database | null = isEditing
    ? (databasesQuery.data?.databases ?? []).find((db) => db.id === dbId) ?? null
    : null;

  const [form, setForm] = useState<DatabaseForm>(emptyForm);
  const [hydratedId, setHydratedId] = useState<string | null>(isEditing ? null : "__new__");
  const [formError, setFormError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  const [testing, setTesting] = useState(false);
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    if (!isEditing) {
      setForm(emptyForm);
      setHydratedId("__new__");
      return;
    }
    if (editing && hydratedId !== editing.id) {
      setForm({
        name: editing.name,
        host: editing.host,
        port: String(editing.port),
        db_name: editing.db_name,
        username: editing.username,
        password: "",
        ssl_mode: editing.ssl_mode || "disable",
      });
      setHydratedId(editing.id);
    }
  }, [isEditing, editing, hydratedId]);

  const update = (key: keyof DatabaseForm, value: string) => {
    setForm((current) => ({ ...current, [key]: value }));
  };

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["admin", "databases"] }),
      queryClient.invalidateQueries({ queryKey: ["databases"] }),
    ]);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setFormError(null);
    try {
      const input = {
        name: form.name.trim(),
        host: form.host.trim(),
        port: Number(form.port),
        db_name: form.db_name.trim(),
        username: form.username.trim(),
        ssl_mode: form.ssl_mode,
      };
      if (isEditing && editing) {
        await adminApi.updateDatabase(editing.id, {
          ...input,
          password: form.password ? form.password : undefined,
        });
        toast.success("Connection updated", `${input.name} was saved.`);
      } else {
        await adminApi.createDatabase({ ...input, password: form.password });
        toast.success("Connection created", "Grant group access to make it usable.");
      }
      await invalidate();
      navigate("/admin/databases");
    } catch (error) {
      setFormError(error);
    } finally {
      setSaving(false);
    }
  };

  const testConnection = async () => {
    if (!editing) return;
    setTesting(true);
    try {
      const result = await adminApi.testDatabase(editing.id);
      if (result.ok) {
        toast.success(
          "Connection OK",
          `${editing.name} answered in ${result.latency_ms ?? "?"} ms.`,
        );
      } else {
        toast.error("Connection failed", result.error ?? "The target did not answer.");
      }
    } catch (error) {
      toast.error(
        "Connection test failed",
        isApiError(error) ? error.message : "Unexpected error.",
      );
    } finally {
      setTesting(false);
    }
  };

  const confirmDelete = async () => {
    if (!editing) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await adminApi.deleteDatabase(editing.id);
      await invalidate();
      toast.success(
        "Connection removed",
        "Databases with history are soft-deleted so execution evidence stays intact.",
      );
      navigate("/admin/databases");
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  if (isEditing && databasesQuery.isLoading) {
    return <PageLoader label="Loading connection" />;
  }

  if (isEditing && !editing) {
    return (
      <div className="space-y-4">
        <nav aria-label="Breadcrumb">
          <ol className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
            <li>
              <Link to="/admin/databases" className="text-leaf-800 hover:text-leaf-900">
                Connections
              </Link>
            </li>
          </ol>
        </nav>
        <ErrorBanner error={new Error("Connection not found.")} />
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <nav aria-label="Breadcrumb">
        <ol className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
          <li>
            <Link to="/admin/databases" className="text-leaf-800 hover:text-leaf-900">
              Connections
            </Link>
          </li>
          <li className="flex items-center gap-2">
            <span aria-hidden="true" className="text-muted">
              /
            </span>
            <span aria-current="page" className="font-semibold text-ink-soft">
              {editing ? editing.name : "New connection"}
            </span>
          </li>
        </ol>
      </nav>

      <PageHeader
        title={editing ? `Edit ${editing.name}` : "Add connection"}
        description={
          editing ? `Updated ${formatDateTime(editing.updated_at)}` : undefined
        }
        actions={
          editing ? (
            <Button
              variant="secondary"
              type="button"
              loading={testing}
              onClick={() => void testConnection()}
            >
              Test connection
            </Button>
          ) : null
        }
      />

      {/* Card 1: Connection settings */}
      <section className="card card-pad flex flex-col gap-3" aria-labelledby="conn-form-title">
        <h2 id="conn-form-title" className="section-title">
          Connection settings
        </h2>
        {formError ? <ErrorBanner error={formError} /> : null}

        <form className="grid gap-4" onSubmit={submit}>
          {/* Row 1: Display name, Host, Port */}
          <div className="form-row">
            <Field label="Display name" htmlFor="f-conn-name" required>
              <TextInput
                id="f-conn-name"
                value={form.name}
                onChange={(event) => update("name", event.target.value)}
                autoComplete="off"
                required
                autoFocus={!editing}
              />
            </Field>
            <Field label="Host" htmlFor="f-conn-host" required>
              <TextInput
                id="f-conn-host"
                value={form.host}
                onChange={(event) => update("host", event.target.value)}
                autoComplete="off"
                required
              />
            </Field>
            <Field label="Port" htmlFor="f-conn-port" required>
              <TextInput
                id="f-conn-port"
                type="number"
                min={1}
                max={65535}
                value={form.port}
                onChange={(event) => update("port", event.target.value)}
                required
              />
            </Field>
          </div>

          {/* Row 2: Database name, Database user */}
          <div className="form-row">
            <Field label="Database name" htmlFor="f-conn-dbname" required>
              <TextInput
                id="f-conn-dbname"
                value={form.db_name}
                onChange={(event) => update("db_name", event.target.value)}
                autoComplete="off"
                required
              />
            </Field>
            <Field label="Database user" htmlFor="f-conn-username" required>
              <TextInput
                id="f-conn-username"
                value={form.username}
                onChange={(event) => update("username", event.target.value)}
                autoComplete="off"
                required
              />
            </Field>
          </div>

          {/* Row 3: Database password, SSL mode */}
          <div className="form-row">
            <Field
              label="Database password"
              htmlFor="f-conn-password"
              required={!editing}
              hint={
                editing
                  ? "Leave empty to keep the current password."
                  : "Stored encrypted. Re-enter it if the form fails to save."
              }
            >
              <TextInput
                id="f-conn-password"
                type="password"
                value={form.password}
                onChange={(event) => update("password", event.target.value)}
                autoComplete="new-password"
                required={!editing}
              />
            </Field>
            <Field
              label="SSL mode"
              htmlFor="f-conn-sslmode"
              required
              hint="Use require or stricter for hosts outside the server."
            >
              <Select
                id="f-conn-sslmode"
                value={form.ssl_mode}
                onChange={(event) => update("ssl_mode", event.target.value)}
              >
                <option value="disable">disable</option>
                <option value="allow">allow</option>
                <option value="prefer">prefer</option>
                <option value="require">require</option>
                <option value="verify-ca">verify-ca</option>
                <option value="verify-full">verify-full</option>
              </Select>
            </Field>
          </div>

          <div className="flex flex-wrap items-center gap-3 pt-2">
            <Button type="submit" loading={saving}>
              {editing ? "Save changes" : "Add connection"}
            </Button>
            <Link to="/admin/databases" className="btn btn-ghost">
              Cancel
            </Link>
          </div>
        </form>
      </section>

      {/* Card 2: Delete connection (card--danger) when editing */}
      {editing ? (
        <section
          className="card card-danger card-pad flex flex-col gap-3"
          aria-labelledby="conn-delete-title"
        >
          <h2 id="conn-delete-title" className="section-title">
            Delete connection
          </h2>
          <p className="m-0 text-sm leading-relaxed text-ink">
            Connection <strong>{editing.name}</strong> and its group access, drafts, and migration
            history will be removed from the platform. The database itself is untouched. Refused
            while a migration is running.
          </p>
          <div>
            <details className="group inline-block max-w-full">
              <summary className="inline-flex min-h-[2.75rem] cursor-pointer list-none items-center gap-2 border-2 border-ink bg-paper px-4 py-2 font-semibold text-crimson shadow-[2px_2px_0_rgb(15_31_20/0.18)] hover:bg-crimson-wash">
                <Trash2 className="h-4 w-4" aria-hidden />
                <span>Delete {editing.name}</span>
                <ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180" />
              </summary>
              <div className="mt-2 min-w-[min(18rem,80vw)] space-y-3 border-2 border-ink bg-paper p-3 text-sm">
                <p className="m-0">This cannot be undone.</p>
                {deleteError ? <ErrorBanner error={deleteError} /> : null}
                <Button
                  variant="danger"
                  size="sm"
                  type="button"
                  loading={deleting}
                  onClick={() => void confirmDelete()}
                >
                  Yes, delete connection
                </Button>
              </div>
            </details>
          </div>
        </section>
      ) : null}
    </div>
  );
}

export default function DatabasesPage() {
  const { dbId } = useParams();
  const location = useLocation();
  const isNew = location.pathname.endsWith("/admin/databases/new");

  if (isNew) {
    return <DatabaseFormView />;
  }
  if (dbId) {
    return <DatabaseFormView dbId={dbId} />;
  }
  return <DatabasesListView />;
}
