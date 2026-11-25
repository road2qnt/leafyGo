import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Database as DatabaseIcon, Pencil, PlugZap, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";

import { isApiError } from "../../api/client";
import { adminApi } from "../../api/endpoints";
import type { Database } from "../../api/types";
import {
  Button,
  ConfirmDialog,
  ErrorBanner,
  Field,
  Modal,
  PageHeader,
  PageLoader,
  Select,
  TableEmpty,
  TableWrap,
  TextInput,
} from "../../components/ui";
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

export default function DatabasesPage() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const databasesQuery = useQuery({ queryKey: ["admin", "databases"], queryFn: adminApi.databases });

  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Database | null>(null);
  const [form, setForm] = useState<DatabaseForm>(emptyForm);
  const [formError, setFormError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  const [testingId, setTestingId] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<Record<string, string>>({});

  const [deleteTarget, setDeleteTarget] = useState<Database | null>(null);
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);

  const databases = databasesQuery.data?.databases ?? [];
  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["admin", "databases"] }),
      queryClient.invalidateQueries({ queryKey: ["databases"] }),
    ]);
  };

  const openCreate = () => {
    setEditing(null);
    setForm(emptyForm);
    setFormError(null);
    setFormOpen(true);
  };

  const openEdit = (database: Database) => {
    setEditing(database);
    setForm({
      name: database.name,
      host: database.host,
      port: String(database.port),
      db_name: database.db_name,
      username: database.username,
      password: "",
      ssl_mode: database.ssl_mode,
    });
    setFormError(null);
    setFormOpen(true);
  };

  const update = (key: keyof DatabaseForm, value: string) => {
    setForm((current) => ({ ...current, [key]: value }));
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
      if (editing) {
        await adminApi.updateDatabase(editing.id, {
          ...input,
          password: form.password ? form.password : undefined,
        });
        toast.success("Connection updated", `${input.name} was saved.`);
      } else {
        await adminApi.createDatabase({ ...input, password: form.password });
        toast.success("Connection created", "Grant group access to make it usable.");
      }
      setFormOpen(false);
      await invalidate();
    } catch (error) {
      setFormError(error);
    } finally {
      setSaving(false);
    }
  };

  const testConnection = async (database: Database) => {
    setTestingId(database.id);
    setTestResult((current) => ({ ...current, [database.id]: "" }));
    try {
      const result = await adminApi.testDatabase(database.id);
      if (result.ok) {
        setTestResult((current) => ({
          ...current,
          [database.id]: `Connected in ${result.latency_ms ?? "?"} ms`,
        }));
        toast.success("Connection OK", `${database.name} answered in ${result.latency_ms ?? "?"} ms.`);
      } else {
        setTestResult((current) => ({ ...current, [database.id]: result.error ?? "Connection failed" }));
        toast.error("Connection failed", result.error ?? "The target did not answer.");
      }
    } catch (error) {
      toast.error("Connection test failed", isApiError(error) ? error.message : "Unexpected error.");
    } finally {
      setTestingId(null);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await adminApi.deleteDatabase(deleteTarget.id);
      toast.success(
        "Connection removed",
        "Databases with history are soft-deleted so execution evidence stays intact.",
      );
      setDeleteTarget(null);
      await invalidate();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  if (databasesQuery.isLoading) return <PageLoader label="Loading connections" />;

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow="Administration"
        title="Target connections"
        description="PostgreSQL targets managed by LeafyGo. Credentials are encrypted with AES-256-GCM; the API never returns the password."
        actions={
          <Button onClick={openCreate}>
            <Plus className="h-4 w-4" /> New connection
          </Button>
        }
      />

      {databasesQuery.isError ? (
        <ErrorBanner error={databasesQuery.error} onRetry={() => void databasesQuery.refetch()} />
      ) : null}

      <TableWrap>
        <table className="table-base min-w-[980px]">
          <thead>
            <tr>
              <th className="th">Connection</th>
              <th className="th">Target</th>
              <th className="th">User</th>
              <th className="th">SSL</th>
              <th className="th">Updated</th>
              <th className="th text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {databases.map((database) => (
              <tr key={database.id} className="tr-hover">
                <td className="td">
                  <div className="flex items-center gap-2.5">
                    <span className="grid h-8 w-8 place-items-center rounded-md bg-leaf-50 text-leaf-700">
                      <DatabaseIcon className="h-4 w-4" />
                    </span>
                    <div>
                      <p className="text-sm font-medium text-ink">{database.name}</p>
                      <p className="font-mono text-[10px] text-muted">{database.id}</p>
                    </div>
                  </div>
                </td>
                <td className="td">
                  <p className="font-mono text-xs text-ink">
                    {database.host}:{database.port}
                  </p>
                  <p className="font-mono text-[10px] text-muted">{database.db_name}</p>
                </td>
                <td className="td font-mono text-xs">{database.username}</td>
                <td className="td">
                  <span className="badge badge-neutral font-mono">{database.ssl_mode}</span>
                </td>
                <td className="td text-xs text-muted">{formatDateTime(database.updated_at)}</td>
                <td className="td">
                  <div className="flex flex-col items-end gap-1">
                    <div className="flex gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        loading={testingId === database.id}
                        onClick={() => void testConnection(database)}
                      >
                        <PlugZap className="h-3.5 w-3.5" /> Test
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => openEdit(database)}>
                        <Pencil className="h-3.5 w-3.5" /> Edit
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-[#8b3323] hover:bg-[#fbebe6]"
                        onClick={() => { setDeleteTarget(database); setDeleteError(null); }}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                    {testResult[database.id] ? (
                      <span
                        className={`text-[10px] ${
                          testResult[database.id].startsWith("Connected")
                            ? "text-leaf-600"
                            : "text-[#8b3323]"
                        }`}
                      >
                        {testResult[database.id]}
                      </span>
                    ) : null}
                  </div>
                </td>
              </tr>
            ))}
            {databases.length === 0 ? (
              <TableEmpty colSpan={6}>No connections yet. Register the first target database.</TableEmpty>
            ) : null}
          </tbody>
        </table>
      </TableWrap>

      <Modal
        open={formOpen}
        onClose={() => setFormOpen(false)}
        title={editing ? `Edit · ${editing.name}` : "New target connection"}
        description="The API stores this password encrypted. Leave it blank when editing to keep the current credential."
        footer={
          <>
            <Button variant="secondary" onClick={() => setFormOpen(false)} disabled={saving}>
              Cancel
            </Button>
            <Button type="submit" form="database-form" loading={saving}>
              {editing ? "Save changes" : "Create connection"}
            </Button>
          </>
        }
      >
        <form id="database-form" className="space-y-4" onSubmit={submit}>
          {formError ? <ErrorBanner error={formError} /> : null}
          <Field label="Display name" htmlFor="db-name" required>
            <TextInput
              id="db-name"
              value={form.name}
              onChange={(event) => update("name", event.target.value)}
              placeholder="Inventory Production"
              required
              autoFocus
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-[1fr_120px]">
            <Field label="Host" htmlFor="db-host" required>
              <TextInput
                id="db-host"
                value={form.host}
                onChange={(event) => update("host", event.target.value)}
                placeholder="targets-db"
                className="font-mono"
                required
              />
            </Field>
            <Field label="Port" htmlFor="db-port" required>
              <TextInput
                id="db-port"
                type="number"
                min={1}
                max={65535}
                value={form.port}
                onChange={(event) => update("port", event.target.value)}
                className="font-mono"
                required
              />
            </Field>
          </div>
          <Field label="Database name" htmlFor="db-database" required>
            <TextInput
              id="db-database"
              value={form.db_name}
              onChange={(event) => update("db_name", event.target.value)}
              placeholder="leafygo_targets"
              className="font-mono"
              required
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Username" htmlFor="db-username" required>
              <TextInput
                id="db-username"
                value={form.username}
                onChange={(event) => update("username", event.target.value)}
                placeholder="leafygo_app"
                className="font-mono"
                required
              />
            </Field>
            <Field
              label={editing ? "Password (optional)" : "Password"}
              htmlFor="db-password"
              required={!editing}
              hint={editing ? "Blank keeps the existing secret." : undefined}
            >
              <TextInput
                id="db-password"
                type="password"
                value={form.password}
                onChange={(event) => update("password", event.target.value)}
                autoComplete="new-password"
                required={!editing}
              />
            </Field>
          </div>
          <Field label="SSL mode" htmlFor="db-ssl" hint="Use disable for local Docker targets; require or verify-full outside.">
            <Select
              id="db-ssl"
              value={form.ssl_mode}
              onChange={(event) => update("ssl_mode", event.target.value)}
            >
              <option value="disable">disable</option>
              <option value="prefer">prefer</option>
              <option value="require">require</option>
              <option value="verify-ca">verify-ca</option>
              <option value="verify-full">verify-full</option>
            </Select>
          </Field>
        </form>
      </Modal>

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => { setDeleteTarget(null); setDeleteError(null); }}
        onConfirm={() => void confirmDelete()}
        title={`Remove ${deleteTarget?.name ?? "connection"}?`}
        description={
          <div className="space-y-3">
            <p>
              A database with an active lock or queued/running job is refused with{" "}
              <code className="font-mono text-xs">DB_HAS_ACTIVE_WORK</code>. Databases with history
              are soft-deleted so migration evidence is preserved.
            </p>
            {deleteError ? <ErrorBanner error={deleteError} /> : null}
          </div>
        }
        confirmLabel="Remove connection"
        loading={deleting}
      />
    </div>
  );
}
