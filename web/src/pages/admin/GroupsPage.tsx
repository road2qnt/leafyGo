import { useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Pencil, Plus, ShieldCheck, Trash2, UsersRound } from "lucide-react";
import { useState, type FormEvent } from "react";

import { isApiError } from "../../api/client";
import { adminApi } from "../../api/endpoints";
import type { Database, Group, User } from "../../api/types";
import {
  Button,
  CheckboxRow,
  ConfirmDialog,
  ErrorBanner,
  Field,
  Modal,
  OptionalTag,
  PageHeader,
  PageLoader,
  Select,
  TableEmpty,
  TableWrap,
  Textarea,
  TextInput,
} from "../../components/ui";
import { useToast } from "../../hooks/useToast";
import { formatDateTime } from "../../lib/format";

type GrantValue = "none" | "view" | "edit";

export default function GroupsPage() {
  const queryClient = useQueryClient();
  const toast = useToast();

  const groupsQuery = useQuery({ queryKey: ["admin", "groups"], queryFn: adminApi.groups });
  const usersQuery = useQuery({ queryKey: ["admin", "users"], queryFn: adminApi.users });
  const databasesQuery = useQuery({ queryKey: ["admin", "databases"], queryFn: adminApi.databases });

  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createDescription, setCreateDescription] = useState("");
  const [createError, setCreateError] = useState<unknown>(null);
  const [creating, setCreating] = useState(false);

  const [editing, setEditing] = useState<Group | null>(null);
  const [editName, setEditName] = useState("");
  const [editDescription, setEditDescription] = useState("");
  const [leaderId, setLeaderId] = useState("");
  const [memberIds, setMemberIds] = useState<Set<string>>(new Set());
  const [grants, setGrants] = useState<Record<string, GrantValue>>({});
  const [editError, setEditError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<Group | null>(null);
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);

  const groups = groupsQuery.data?.groups ?? [];
  const users = usersQuery.data?.users ?? [];
  const databases = databasesQuery.data?.databases ?? [];

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["admin", "groups"] }),
      queryClient.invalidateQueries({ queryKey: ["databases"] }),
    ]);
  };

  const openCreate = () => {
    setCreateName("");
    setCreateDescription("");
    setCreateError(null);
    setCreateOpen(true);
  };

  const createGroup = async (event: FormEvent) => {
    event.preventDefault();
    setCreating(true);
    setCreateError(null);
    try {
      const group = await adminApi.createGroup({ name: createName.trim(), description: createDescription });
      toast.success("Group created", "Add members, a leader, and database access next.");
      setCreateOpen(false);
      await invalidate();
      openEdit(group);
    } catch (error) {
      setCreateError(error);
    } finally {
      setCreating(false);
    }
  };

  const openEdit = (group: Group) => {
    setEditing(group);
    setEditName(group.name);
    setEditDescription(group.description);
    setLeaderId(group.leader_id ?? "");
    setMemberIds(new Set(group.members.map((member) => member.id)));
    const nextGrants: Record<string, GrantValue> = {};
    for (const database of databases) nextGrants[database.id] = "none";
    for (const grant of group.access) nextGrants[grant.db_id] = grant.level;
    setGrants(nextGrants);
    setEditError(null);
  };

  const toggleMember = (userId: string, checked: boolean) => {
    setMemberIds((current) => {
      const next = new Set(current);
      if (checked) next.add(userId);
      else {
        next.delete(userId);
        if (leaderId === userId) setLeaderId("");
      }
      return next;
    });
  };

  const saveEdit = async (event: FormEvent) => {
    event.preventDefault();
    if (!editing) return;
    setSaving(true);
    setEditError(null);
    try {
      const ids = Array.from(memberIds);
      if (leaderId && !ids.includes(leaderId)) ids.push(leaderId);
      await adminApi.updateGroup(editing.id, { name: editName.trim(), description: editDescription });
      await adminApi.setMembers(editing.id, ids);
      await adminApi.setLeader(editing.id, leaderId || null);
      await adminApi.setAccess(
        editing.id,
        Object.entries(grants)
          .filter(([, value]) => value !== "none")
          .map(([dbId, value]) => ({ db_id: dbId, level: value as "view" | "edit" })),
      );
      toast.success("Group saved", "Leader, members, and access grants are up to date.");
      setEditing(null);
      await invalidate();
    } catch (error) {
      setEditError(error);
    } finally {
      setSaving(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await adminApi.deleteGroup(deleteTarget.id);
      toast.success("Group deleted", `${deleteTarget.name} had no approval history.`);
      setDeleteTarget(null);
      await invalidate();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  const deactivateInstead = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await adminApi.deactivateGroup(deleteTarget.id);
      toast.success("Group deactivated", "Access and leadership were cleared; history is preserved.");
      setDeleteTarget(null);
      await invalidate();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  const deactivateFromEditor = async () => {
    if (!editing) return;
    setSaving(true);
    setEditError(null);
    try {
      await adminApi.deactivateGroup(editing.id);
      toast.success("Group deactivated", `${editing.name} no longer participates in approvals.`);
      setEditing(null);
      await invalidate();
    } catch (error) {
      setEditError(error);
    } finally {
      setSaving(false);
    }
  };

  if (groupsQuery.isLoading) return <PageLoader label="Loading groups" />;

  const editMembers = users.filter(
    (user) => user.is_active || memberIds.has(user.id),
  );

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow="Administration"
        title="Groups & access"
        description="Every database approval requires its group leaders. Grants are View or View + Edit, and a leader must be a member."
        actions={
          <Button onClick={openCreate}>
            <Plus className="h-4 w-4" /> New group
          </Button>
        }
      />

      {groupsQuery.isError ? (
        <ErrorBanner error={groupsQuery.error} onRetry={() => void groupsQuery.refetch()} />
      ) : null}

      <div className="grid gap-4 lg:grid-cols-2">
        {groups.map((group) => (
          <div key={group.id} className="card flex flex-col p-5">
            <div className="flex items-start justify-between gap-3">
              <div className="flex min-w-0 items-start gap-3">
                <span className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-leaf-50 text-leaf-700">
                  <UsersRound className="h-5 w-5" />
                </span>
                <div className="min-w-0">
                  <h2 className="truncate text-sm font-semibold text-ink">{group.name}</h2>
                  <p className="mt-0.5 line-clamp-2 text-xs leading-relaxed text-muted">
                    {group.description || "No description."}
                  </p>
                </div>
              </div>
              <div className="flex shrink-0 gap-1">
                <Button variant="ghost" size="sm" onClick={() => openEdit(group)}>
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-[#8b3323] hover:bg-[#fbebe6]"
                  onClick={() => { setDeleteTarget(group); setDeleteError(null); }}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            </div>

            <div className="mt-4 flex flex-wrap items-center gap-2 text-xs">
              {group.leader_username ? (
                <span className="badge badge-plum">
                  <ShieldCheck className="h-3 w-3" /> Leader: {group.leader_username}
                </span>
              ) : (
                <span className="badge badge-danger">No leader</span>
              )}
              <span className="badge badge-neutral">{group.members.length} members</span>
              <span className="badge badge-neutral">{group.access.length} grants</span>
            </div>

            <div className="mt-3 flex flex-wrap gap-1.5">
              {group.access.map((grant) => (
                <span
                  key={grant.db_id}
                  className={`badge ${grant.level === "edit" ? "badge-success" : "badge-info"}`}
                >
                  {grant.db_name} · {grant.level === "edit" ? "edit" : "view"}
                </span>
              ))}
              {group.access.length === 0 ? (
                <span className="text-[11px] text-muted">No database grants yet.</span>
              ) : null}
            </div>

            <p className="mt-auto pt-4 text-[10px] text-muted">Created {formatDateTime(group.created_at)}</p>
          </div>
        ))}
      </div>

      {groups.length === 0 ? (
        <div className="card p-8 text-center text-sm text-muted">
          No groups yet. Create one, then assign a leader and grant database access.
        </div>
      ) : null}

      {!usersQuery.isLoading && users.length === 0 ? (
        <ErrorBanner error={new Error("Create at least one user before assigning group leaders.")} />
      ) : null}

      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="Create group"
        description="A group becomes an approver when it has access to the target database."
        size="sm"
        footer={
          <>
            <Button variant="secondary" onClick={() => setCreateOpen(false)} disabled={creating}>
              Cancel
            </Button>
            <Button type="submit" form="create-group-form" loading={creating}>
              Create group
            </Button>
          </>
        }
      >
        <form id="create-group-form" className="space-y-4" onSubmit={createGroup}>
          {createError ? <ErrorBanner error={createError} /> : null}
          <Field label="Group name" htmlFor="group-name" required>
            <TextInput
              id="group-name"
              value={createName}
              onChange={(event) => setCreateName(event.target.value)}
              required
              autoFocus
            />
          </Field>
          <Field label="Description" htmlFor="group-description">
            <Textarea
              id="group-description"
              value={createDescription}
              onChange={(event) => setCreateDescription(event.target.value)}
              placeholder="What does this group own?"
            />
          </Field>
        </form>
      </Modal>

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={`Edit · ${editing?.name ?? "group"}`}
        description="Leader, membership, and access are snapshotted when a proposal is submitted."
        size="lg"
        footer={
          <>
            <Button variant="secondary" onClick={() => setEditing(null)} disabled={saving}>
              Cancel
            </Button>
            <Button type="submit" form="edit-group-form" loading={saving}>
              Save group
            </Button>
          </>
        }
      >
        {editing ? (
          <form id="edit-group-form" className="space-y-5" onSubmit={saveEdit}>
            {editError ? <ErrorBanner error={editError} /> : null}
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Group name" htmlFor="edit-group-name" required>
                <TextInput
                  id="edit-group-name"
                  value={editName}
                  onChange={(event) => setEditName(event.target.value)}
                  required
                />
              </Field>
              <Field
                label="Group leader"
                htmlFor="edit-group-leader"
                hint="Leaders approve or reject proposals for this group."
              >
                <Select
                  id="edit-group-leader"
                  value={leaderId}
                  onChange={(event) => {
                    const value = event.target.value;
                    setLeaderId(value);
                    if (value) {
                      setMemberIds((current) => new Set(current).add(value));
                    }
                  }}
                >
                  <option value="">No leader</option>
                  {editMembers.map((user) => (
                    <option key={user.id} value={user.id}>
                      {user.username}
                      {user.is_active ? "" : " (inactive)"}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
            <Field label="Description" htmlFor="edit-group-description">
              <Textarea
                id="edit-group-description"
                value={editDescription}
                onChange={(event) => setEditDescription(event.target.value)}
              />
            </Field>

            <div>
              <p className="label">Members</p>
              <div className="grid max-h-48 gap-1 overflow-y-auto rounded-lg border border-line bg-[#faf9f4] p-2 sm:grid-cols-2">
                {editMembers.map((user) => (
                  <CheckboxRow
                    key={user.id}
                    label={
                      <span className="inline-flex items-center gap-1.5">
                        {user.username}
                        {!user.is_active ? <span className="badge badge-danger">Inactive</span> : null}
                        {user.id === leaderId ? <span className="badge badge-plum">Leader</span> : null}
                      </span>
                    }
                    checked={memberIds.has(user.id)}
                    onChange={(checked) => toggleMember(user.id, checked)}
                  />
                ))}
                {editMembers.length === 0 ? (
                  <p className="p-2 text-xs text-muted">No users to add.</p>
                ) : null}
              </div>
            </div>

            <div>
              <p className="label">Database access</p>
              <div className="overflow-hidden rounded-lg border border-line">
                <table className="table-base">
                  <thead>
                    <tr>
                      <th className="th">Database</th>
                      <th className="th w-40">Grant</th>
                    </tr>
                  </thead>
                  <tbody>
                    {databases.map((database: Database) => (
                      <tr key={database.id}>
                        <td className="td">
                          <p className="text-sm">{database.name}</p>
                          <p className="font-mono text-[10px] text-muted">{database.db_name}</p>
                        </td>
                        <td className="td">
                          <Select
                            value={grants[database.id] ?? "none"}
                            onChange={(event) =>
                              setGrants((current) => ({
                                ...current,
                                [database.id]: event.target.value as GrantValue,
                              }))
                            }
                          >
                            <option value="none">No access</option>
                            <option value="view">View only</option>
                            <option value="edit">View + Edit</option>
                          </Select>
                        </td>
                      </tr>
                    ))}
                    {databases.length === 0 ? (
                      <TableEmpty colSpan={2}>No connections registered yet.</TableEmpty>
                    ) : null}
                  </tbody>
                </table>
              </div>
            </div>

            <div className="rounded-xl border border-dashed border-[#c9a7d8] bg-[#faf5fc] p-4">
              <div className="flex flex-wrap items-center gap-2">
                <KeyRound className="h-4 w-4 text-[#6d4b8f]" />
                <p className="text-sm font-semibold text-[#54376f]">Deactivate group</p>
                <OptionalTag />
              </div>
              <p className="mt-1 text-xs leading-relaxed text-[#54376f]/75">
                Clears access and leadership while preserving approval history. This is the safe
                fallback when a hard delete returns <code className="font-mono">GROUP_HAS_HISTORY</code>.
              </p>
              <Button
                variant="secondary"
                size="sm"
                className="mt-2"
                onClick={() => void deactivateFromEditor()}
                disabled={saving}
              >
                Deactivate this group
              </Button>
            </div>
          </form>
        ) : null}
      </Modal>

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => { setDeleteTarget(null); setDeleteError(null); }}
        onConfirm={() => void confirmDelete()}
        title={`Delete ${deleteTarget?.name ?? "group"}?`}
        description={
          <div className="space-y-3">
            <p>
              Groups with approval history cannot be hard-deleted. The API returns{" "}
              <code className="font-mono text-xs">GROUP_HAS_HISTORY</code>.
            </p>
            {deleteError ? (
              <>
                <ErrorBanner error={deleteError} />
                {isApiError(deleteError) && deleteError.code === "GROUP_HAS_HISTORY" ? (
                  <Button variant="secondary" onClick={() => void deactivateInstead()} loading={deleting}>
                    Deactivate group instead <OptionalTag />
                  </Button>
                ) : null}
              </>
            ) : null}
          </div>
        }
        confirmLabel="Delete group"
        loading={deleting}
      />
    </div>
  );
}
