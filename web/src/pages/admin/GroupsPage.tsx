import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";

import { isApiError } from "../../api/client";
import { adminApi } from "../../api/endpoints";
import type { Group } from "../../api/types";
import {
  Button,
  EmptyState,
  ErrorBanner,
  Field,
  OptionalTag,
  PageHeader,
  PageLoader,
  Select,
  TableWrap,
  Textarea,
  TextInput,
} from "../../components/ui";
import { useToast } from "../../hooks/useToast";

function GroupsListView() {
  const groupsQuery = useQuery({ queryKey: ["admin", "groups"], queryFn: adminApi.groups });

  if (groupsQuery.isLoading) return <PageLoader label="Loading groups" />;

  const groups = groupsQuery.data?.groups ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="Groups and access"
        actions={
          groups.length > 0 ? (
            <Link to="/admin/groups/new" className="btn btn-primary">
              <Plus className="h-4 w-4" /> Create group
            </Link>
          ) : null
        }
      />

      {groupsQuery.isError ? (
        <ErrorBanner error={groupsQuery.error} onRetry={() => void groupsQuery.refetch()} />
      ) : null}

      <section aria-labelledby="groups-caption">
        {groups.length > 0 ? (
          <TableWrap>
            <table className="table-base table-stack">
              <caption id="groups-caption">
                {groups.length} {groups.length === 1 ? "group" : "groups"}
              </caption>
              <thead>
                <tr>
                  <th scope="col" className="th">Group</th>
                  <th scope="col" className="th">Leader</th>
                  <th scope="col" className="th">Members</th>
                  <th scope="col" className="th">Database access</th>
                  <th scope="col" className="th text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {groups.map((group) => {
                  const editDbs = group.access.filter((g) => g.level === "edit");
                  const viewDbs = group.access.filter((g) => g.level === "view");
                  return (
                    <tr key={group.id}>
                      <th scope="row" data-label="Group" className="td cell-title col-main">
                        <div>
                          <Link
                            to={`/admin/groups/${group.id}`}
                            className="font-semibold text-leaf-800 hover:text-leaf-900"
                          >
                            {group.name}
                          </Link>
                          {group.description ? (
                            <span className="mt-1 block text-xs font-normal text-muted">
                              {group.description}
                            </span>
                          ) : null}
                        </div>
                      </th>
                      <td data-label="Leader" className="td">
                        {group.leader_username ? (
                          <span className="badge badge-success">{group.leader_username}</span>
                        ) : (
                          <span className="badge badge-danger">No leader</span>
                        )}
                      </td>
                      <td
                        data-label="Members"
                        className="td cell-wide max-w-64 truncate"
                        title={group.members.map((m) => m.username).join(", ")}
                      >
                        {group.members.length > 0 ? (
                          group.members.map((m) => m.username).join(", ")
                        ) : (
                          <span className="text-muted">None</span>
                        )}
                      </td>
                      <td data-label="Database access" className="td cell-wide">
                        {group.access.length > 0 ? (
                          <dl className="m-0 space-y-1.5">
                            {editDbs.length > 0 ? (
                              <div>
                                <dt className="text-xs font-semibold tracking-wider text-muted uppercase">
                                  View + Edit
                                </dt>
                                <dd className="m-0 text-sm text-ink">
                                  {editDbs.map((g) => g.db_name).join(", ")}
                                </dd>
                              </div>
                            ) : null}
                            {viewDbs.length > 0 ? (
                              <div>
                                <dt className="text-xs font-semibold tracking-wider text-muted uppercase">
                                  View Only
                                </dt>
                                <dd className="m-0 text-sm text-ink">
                                  {viewDbs.map((g) => g.db_name).join(", ")}
                                </dd>
                              </div>
                            ) : null}
                          </dl>
                        ) : (
                          <span className="text-muted">None</span>
                        )}
                      </td>
                      <td data-label="Actions" className="td cell-actions text-right whitespace-nowrap">
                        <Link
                          to={`/admin/groups/${group.id}`}
                          className="btn btn-sm"
                        >
                          Manage<span className="sr-only"> {group.name}</span>
                        </Link>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </TableWrap>
        ) : (
          <EmptyState
            title="No groups yet"
            description="Create a group, add members, then grant it database access."
            action={
              <Link to="/admin/groups/new" className="btn btn-primary">
                <Plus className="h-4 w-4" /> Create group
              </Link>
            }
          />
        )}
      </section>
    </div>
  );
}

function GroupCreateView() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();

  const usersQuery = useQuery({ queryKey: ["admin", "users"], queryFn: adminApi.users });
  const eligibleLeaders = useMemo(
    () => (usersQuery.data?.users ?? []).filter((u) => !u.is_platform_admin && u.is_active),
    [usersQuery.data],
  );

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [leaderId, setLeaderId] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    if (!leaderId && eligibleLeaders.length > 0) {
      setLeaderId(eligibleLeaders[0].id);
    }
  }, [leaderId, eligibleLeaders]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setCreating(true);
    setError(null);
    try {
      const group = await adminApi.createGroup({
        name: name.trim(),
        description,
        leader_id: leaderId,
      });
      await queryClient.invalidateQueries({ queryKey: ["admin", "groups"] });
      toast.success("Group created", "Add members and grant database access.");
      navigate(`/admin/groups/${group.id}`);
    } catch (cause) {
      setError(cause);
    } finally {
      setCreating(false);
    }
  };

  if (usersQuery.isLoading) return <PageLoader label="Loading users" />;

  return (
    <div className="space-y-6">
      <nav aria-label="Breadcrumb">
        <ol className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
          <li>
            <Link to="/admin/groups" className="text-leaf-800 hover:text-leaf-900">
              Groups
            </Link>
          </li>
          <li className="flex items-center gap-2">
            <span aria-hidden="true" className="text-muted">
              /
            </span>
            <span aria-current="page" className="font-semibold text-ink-soft">
              New group
            </span>
          </li>
        </ol>
      </nav>

      <PageHeader title="Create group" />

      <section className="card card-pad space-y-4" aria-labelledby="group-form-title">
        <h2 id="group-form-title" className="section-title">
          Details
        </h2>
        {error ? <ErrorBanner error={error} /> : null}
        {eligibleLeaders.length === 0 ? (
          <ErrorBanner
            error={
              new Error(
                "Create at least one active non-admin user before creating a group (a group requires a leader).",
              )
            }
          />
        ) : null}

        <form className="grid gap-4" onSubmit={submit}>
          <Field label="Group name" htmlFor="f-group-name" required>
            <TextInput
              id="f-group-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoComplete="off"
              required
              autoFocus
            />
          </Field>

          <Field
            label="Group leader"
            htmlFor="f-group-leader"
            required
            hint="The leader is automatically added as the first member and approves proposals for this group."
          >
            <Select
              id="f-group-leader"
              value={leaderId}
              onChange={(event) => setLeaderId(event.target.value)}
              required
            >
              <option value="">Choose a leader</option>
              {eligibleLeaders.map((user) => (
                <option key={user.id} value={user.id}>
                  {user.username}
                </option>
              ))}
            </Select>
          </Field>

          <Field label="Description" htmlFor="f-group-description">
            <Textarea
              id="f-group-description"
              rows={2}
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              className="min-h-20"
            />
          </Field>

          <p className="hint m-0">
            Additional members and database access are added after the group is created.
          </p>

          <div className="flex flex-wrap items-center gap-3 pt-2">
            <Button type="submit" loading={creating} disabled={!leaderId}>
              Create group
            </Button>
            <Link to="/admin/groups" className="btn btn-ghost">
              Cancel
            </Link>
          </div>
        </form>
      </section>
    </div>
  );
}

function GroupEditView({ groupId }: { groupId: string }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();

  const groupsQuery = useQuery({ queryKey: ["admin", "groups"], queryFn: adminApi.groups });
  const usersQuery = useQuery({ queryKey: ["admin", "users"], queryFn: adminApi.users });
  const databasesQuery = useQuery({
    queryKey: ["admin", "databases"],
    queryFn: adminApi.databases,
  });

  const group: Group | null =
    (groupsQuery.data?.groups ?? []).find((g) => g.id === groupId) ?? null;
  const users = usersQuery.data?.users ?? [];
  const databases = databasesQuery.data?.databases ?? [];

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [leaderId, setLeaderId] = useState("");
  const [hydratedId, setHydratedId] = useState<string | null>(null);
  const [detailsError, setDetailsError] = useState<unknown>(null);
  const [savingDetails, setSavingDetails] = useState(false);

  const [memberFilter, setMemberFilter] = useState("");
  const [addMemberUserId, setAddMemberUserId] = useState("");
  const [memberError, setMemberError] = useState<unknown>(null);
  const [busyMemberId, setBusyMemberId] = useState<string | null>(null);
  const [addingMember, setAddingMember] = useState(false);

  const [accessDrafts, setAccessDrafts] = useState<Record<string, "view" | "edit">>({});
  const [grantDbId, setGrantDbId] = useState("");
  const [grantLevel, setGrantLevel] = useState<"view" | "edit">("view");
  const [accessError, setAccessError] = useState<unknown>(null);
  const [busyAccessDbId, setBusyAccessDbId] = useState<string | null>(null);
  const [grantingAccess, setGrantingAccess] = useState(false);

  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    if (group && hydratedId !== group.id) {
      setName(group.name);
      setDescription(group.description);
      setLeaderId(group.leader_id ?? "");
      setHydratedId(group.id);
    }
  }, [group, hydratedId]);

  useEffect(() => {
    if (!group) return;
    const nextDrafts: Record<string, "view" | "edit"> = {};
    for (const grant of group.access) {
      nextDrafts[grant.db_id] = grant.level;
    }
    setAccessDrafts(nextDrafts);
  }, [group]);

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["admin", "groups"] }),
      queryClient.invalidateQueries({ queryKey: ["admin", "users"] }),
      queryClient.invalidateQueries({ queryKey: ["databases"] }),
    ]);
  };

  if (groupsQuery.isLoading || usersQuery.isLoading || databasesQuery.isLoading) {
    return <PageLoader label="Loading group" />;
  }

  if (!group) {
    return (
      <div className="space-y-4">
        <nav aria-label="Breadcrumb">
          <ol className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
            <li>
              <Link to="/admin/groups" className="text-leaf-800 hover:text-leaf-900">
                Groups
              </Link>
            </li>
          </ol>
        </nav>
        <ErrorBanner error={new Error("Group not found.")} />
      </div>
    );
  }

  const memberIdSet = new Set(group.members.map((m) => m.id));
  const availableUsersToAdd = users.filter(
    (u) => !u.is_platform_admin && u.is_active && !memberIdSet.has(u.id),
  );
  const filteredMembers = group.members.filter((m) =>
    m.username.toLowerCase().includes(memberFilter.trim().toLowerCase()),
  );

  const grantedDbIdSet = new Set(group.access.map((a) => a.db_id));
  const availableDatabasesToGrant = databases.filter((db) => !grantedDbIdSet.has(db.id));

  const saveDetails = async (event: FormEvent) => {
    event.preventDefault();
    setSavingDetails(true);
    setDetailsError(null);
    try {
      await adminApi.updateGroup(group.id, {
        name: name.trim(),
        description,
      });
      if (leaderId && leaderId !== group.leader_id) {
        await adminApi.setLeader(group.id, leaderId);
      }
      await invalidate();
      toast.success("Details saved", `${name.trim()} was updated.`);
    } catch (cause) {
      setDetailsError(cause);
    } finally {
      setSavingDetails(false);
    }
  };

  const handleAddMember = async (event: FormEvent) => {
    event.preventDefault();
    if (!addMemberUserId) return;
    setAddingMember(true);
    setMemberError(null);
    try {
      await adminApi.addMember(group.id, addMemberUserId);
      setAddMemberUserId("");
      await invalidate();
      toast.success("Member added", "User session will rotate on next request.");
    } catch (cause) {
      setMemberError(cause);
    } finally {
      setAddingMember(false);
    }
  };

  const handleRemoveMember = async (userId: string, username: string) => {
    setBusyMemberId(userId);
    setMemberError(null);
    try {
      await adminApi.removeMember(group.id, userId);
      await invalidate();
      toast.success("Member removed", `${username} was removed from ${group.name}.`);
    } catch (cause) {
      setMemberError(cause);
    } finally {
      setBusyMemberId(null);
    }
  };

  const handleUpdateAccess = async (dbId: string, dbName: string) => {
    const level = accessDrafts[dbId] ?? "view";
    setBusyAccessDbId(dbId);
    setAccessError(null);
    try {
      await adminApi.setDatabaseAccess(group.id, dbId, level);
      await invalidate();
      toast.success(
        "Access updated",
        `${dbName} set to ${level === "edit" ? "View + Edit" : "View Only"}.`,
      );
    } catch (cause) {
      setAccessError(cause);
    } finally {
      setBusyAccessDbId(null);
    }
  };

  const handleRevokeAccess = async (dbId: string, dbName: string) => {
    setBusyAccessDbId(dbId);
    setAccessError(null);
    try {
      await adminApi.setDatabaseAccess(group.id, dbId, "none");
      await invalidate();
      toast.success("Access revoked", `Revoked access on ${dbName}.`);
    } catch (cause) {
      setAccessError(cause);
    } finally {
      setBusyAccessDbId(null);
    }
  };

  const handleGrantAccess = async (event: FormEvent) => {
    event.preventDefault();
    if (!grantDbId) return;
    setGrantingAccess(true);
    setAccessError(null);
    try {
      await adminApi.setDatabaseAccess(group.id, grantDbId, grantLevel);
      setGrantDbId("");
      setGrantLevel("view");
      await invalidate();
      toast.success("Access granted", "Group members now have access.");
    } catch (cause) {
      setAccessError(cause);
    } finally {
      setGrantingAccess(false);
    }
  };

  const handleDeleteGroup = async () => {
    setDeleting(true);
    setDeleteError(null);
    try {
      await adminApi.deleteGroup(group.id);
      await invalidate();
      toast.success("Group deleted", `${group.name} was deleted.`);
      navigate("/admin/groups");
    } catch (cause) {
      setDeleteError(cause);
    } finally {
      setDeleting(false);
    }
  };

  const handleDeactivateGroup = async () => {
    setDeleting(true);
    setDeleteError(null);
    try {
      await adminApi.deactivateGroup(group.id);
      await invalidate();
      toast.success(
        "Group deactivated",
        `${group.name} access and leadership were cleared; approval history is preserved.`,
      );
      navigate("/admin/groups");
    } catch (cause) {
      setDeleteError(cause);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="space-y-8">
      <nav aria-label="Breadcrumb">
        <ol className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-sm">
          <li>
            <Link to="/admin/groups" className="text-leaf-800 hover:text-leaf-900">
              Groups
            </Link>
          </li>
          <li className="flex items-center gap-2">
            <span aria-hidden="true" className="text-muted">
              /
            </span>
            <span aria-current="page" className="font-semibold text-ink-soft">
              {group.name}
            </span>
          </li>
        </ol>
      </nav>

      <PageHeader title={group.name} description={group.description || undefined} />

      {/* 2-Column Top Grid: Details + Members */}
      <div className="columns-layout">
        {/* Card 1: Details */}
        <section className="card card-pad flex flex-col gap-3" aria-labelledby="details-title">
          <h2 id="details-title" className="section-title">
            Details
          </h2>
          {detailsError ? <ErrorBanner error={detailsError} /> : null}
          <form className="grid gap-4" onSubmit={saveDetails}>
            <Field label="Group name" htmlFor="f-group-name" required>
              <TextInput
                id="f-group-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                autoComplete="off"
                required
              />
            </Field>

            <Field label="Description" htmlFor="f-group-description">
              <Textarea
                id="f-group-description"
                rows={2}
                value={description}
                onChange={(event) => setDescription(event.target.value)}
                className="min-h-20"
              />
            </Field>

            <Field
              label="Group leader"
              htmlFor="f-group-leader"
              required
              hint="Leaders approve or reject migration proposals for this group. The leader must be a member."
            >
              <Select
                id="f-group-leader"
                value={leaderId}
                onChange={(event) => setLeaderId(event.target.value)}
                required
              >
                {!leaderId ? <option value="">Choose a member</option> : null}
                {group.members.map((member) => (
                  <option key={member.id} value={member.id}>
                    {member.username}
                  </option>
                ))}
              </Select>
            </Field>

            <div className="pt-2">
              <Button type="submit" loading={savingDetails}>
                Save details
              </Button>
            </div>
          </form>
        </section>

        {/* Card 2: Members */}
        <section className="card card-pad flex flex-col gap-3" aria-labelledby="members-title">
          <h2 id="members-title" className="section-title">
            Members
          </h2>
          <p className="hint m-0">
            Adding or removing a member signs that user out so their new access applies right away.
          </p>
          {memberError ? <ErrorBanner error={memberError} /> : null}

          {group.members.length > 0 ? (
            <>
              <div className="grid gap-1">
                <label className="label mb-0" htmlFor="member-filter-input">
                  Find member
                </label>
                <div className="relative flex items-center">
                  <TextInput
                    id="member-filter-input"
                    value={memberFilter}
                    onChange={(event) => setMemberFilter(event.target.value)}
                    placeholder="Type to filter members..."
                    autoComplete="off"
                    className="pr-9"
                  />
                  {memberFilter ? (
                    <button
                      type="button"
                      onClick={() => setMemberFilter("")}
                      className="absolute right-2 cursor-pointer border-0 bg-transparent p-1 text-lg leading-none text-muted hover:text-ink"
                      aria-label="Clear member filter"
                    >
                      ×
                    </button>
                  ) : null}
                </div>
              </div>

              <TableWrap>
                <table className="table-base">
                  <thead>
                    <tr>
                      <th scope="col" className="th">Username</th>
                      <th scope="col" className="th text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredMembers.map((member) => {
                      const isLeader = member.id === group.leader_id;
                      return (
                        <tr key={member.id}>
                          <th scope="row" className="td break-all">
                            <span className="font-semibold text-ink">{member.username}</span>
                            {isLeader ? (
                              <span className="badge badge-success ml-2">Leader</span>
                            ) : null}
                          </th>
                          <td className="td w-[1%] text-right whitespace-nowrap">
                            <Button
                              variant="danger"
                              size="sm"
                              type="button"
                              disabled={isLeader}
                              loading={busyMemberId === member.id}
                              title={
                                isLeader
                                  ? "Assign a different leader in Details before removing this member"
                                  : undefined
                              }
                              aria-label={`Remove ${member.username} from ${group.name}`}
                              onClick={() => void handleRemoveMember(member.id, member.username)}
                            >
                              Remove
                            </Button>
                          </td>
                        </tr>
                      );
                    })}
                    {filteredMembers.length === 0 ? (
                      <tr>
                        <td colSpan={2} className="td text-center text-muted">
                          No matching members.
                        </td>
                      </tr>
                    ) : null}
                  </tbody>
                </table>
              </TableWrap>
            </>
          ) : (
            <p className="m-0 text-sm text-muted">No members yet.</p>
          )}

          {availableUsersToAdd.length > 0 ? (
            <>
              <form
                className="flex flex-wrap items-end gap-2 pt-1 max-sm:[&>button]:w-full"
                onSubmit={handleAddMember}
              >
                <div className="min-w-48 flex-1">
                  <label className="label" htmlFor="add-member-select">
                    Add member
                  </label>
                  <Select
                    id="add-member-select"
                    value={addMemberUserId}
                    onChange={(event) => setAddMemberUserId(event.target.value)}
                    required
                  >
                    <option value="">Search or select user...</option>
                    {availableUsersToAdd.map((user) => (
                      <option key={user.id} value={user.id}>
                        {user.username}
                      </option>
                    ))}
                  </Select>
                </div>
                <Button variant="secondary" type="submit" loading={addingMember}>
                  Add member
                </Button>
              </form>
              <p className="hint m-0">Platform Admins cannot join groups.</p>
            </>
          ) : (
            <p className="hint m-0">Every eligible user is already a member.</p>
          )}
        </section>
      </div>

      {/* Card 3: Database access */}
      <section className="card card-pad flex flex-col gap-3" aria-labelledby="access-title">
        <h2 id="access-title" className="section-title">
          Database access
        </h2>
        <p className="hint m-0">
          Changing access signs out the group's members so their new access applies right away.
        </p>
        {accessError ? <ErrorBanner error={accessError} /> : null}

        {group.access.length > 0 ? (
          <TableWrap>
            <table className="table-base table-stack">
              <thead>
                <tr>
                  <th scope="col" className="th">Database</th>
                  <th scope="col" className="th w-[1%] whitespace-nowrap">Level</th>
                  <th scope="col" className="th w-[1%] text-right whitespace-nowrap">Actions</th>
                </tr>
              </thead>
              <tbody>
                {group.access.map((grant) => {
                  const draftLevel = accessDrafts[grant.db_id] ?? grant.level;
                  return (
                    <tr key={grant.db_id}>
                      <th scope="row" data-label="Database" className="td cell-title font-semibold text-ink">
                        {grant.db_name}
                      </th>
                      <td data-label="Level" className="td cell-wide w-[1%] whitespace-nowrap">
                        <div className="inline-flex items-center gap-2">
                          <label className="sr-only" htmlFor={`lvl-${grant.db_id}`}>
                            Access level on {grant.db_name}
                          </label>
                          <Select
                            id={`lvl-${grant.db_id}`}
                            value={draftLevel}
                            onChange={(event) =>
                              setAccessDrafts((current) => ({
                                ...current,
                                [grant.db_id]: event.target.value as "view" | "edit",
                              }))
                            }
                            className="min-h-[2.25rem] w-auto py-1 pr-9 pl-2.5 text-sm"
                          >
                            <option value="view">View Only</option>
                            <option value="edit">View + Edit</option>
                          </Select>
                          <Button
                            variant="secondary"
                            size="sm"
                            type="button"
                            loading={busyAccessDbId === grant.db_id}
                            onClick={() => void handleUpdateAccess(grant.db_id, grant.db_name)}
                          >
                            Save<span className="sr-only"> level on {grant.db_name}</span>
                          </Button>
                        </div>
                      </td>
                      <td data-label="Actions" className="td cell-actions w-[1%] text-right whitespace-nowrap">
                        <Button
                          variant="danger"
                          size="sm"
                          type="button"
                          loading={busyAccessDbId === grant.db_id}
                          onClick={() => void handleRevokeAccess(grant.db_id, grant.db_name)}
                        >
                          Revoke<span className="sr-only"> access on {grant.db_name}</span>
                        </Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </TableWrap>
        ) : (
          <p className="m-0 text-sm text-muted">No database access yet.</p>
        )}

        {availableDatabasesToGrant.length > 0 ? (
          <form className="flex flex-wrap items-end gap-2 pt-1 max-sm:[&>button]:w-full" onSubmit={handleGrantAccess}>
            <div className="min-w-48 flex-1">
              <label className="label" htmlFor="grant-db">
                Grant access to
              </label>
              <Select
                id="grant-db"
                value={grantDbId}
                onChange={(event) => setGrantDbId(event.target.value)}
                required
              >
                <option value="">Choose a database</option>
                {availableDatabasesToGrant.map((db) => (
                  <option key={db.id} value={db.id}>
                    {db.name}
                  </option>
                ))}
              </Select>
            </div>
            <div className="min-w-48 flex-1">
              <label className="label" htmlFor="grant-level">
                Level
              </label>
              <Select
                id="grant-level"
                value={grantLevel}
                onChange={(event) => setGrantLevel(event.target.value as "view" | "edit")}
              >
                <option value="view">View Only</option>
                <option value="edit">View + Edit</option>
              </Select>
            </div>
            <Button variant="secondary" type="submit" loading={grantingAccess}>
              Grant access
            </Button>
          </form>
        ) : group.access.length > 0 ? (
          <p className="hint m-0">This group already has access to every connection.</p>
        ) : (
          <p className="hint m-0">
            No connections exist yet. <Link to="/admin/databases">Add a connection</Link> first.
          </p>
        )}
      </section>

      {/* Card 4: Delete group (card--danger) */}
      <section className="card card-danger card-pad flex flex-col gap-3" aria-labelledby="delete-title">
        <h2 id="delete-title" className="section-title">
          Delete group
        </h2>
        <p className="m-0 text-sm leading-relaxed text-ink">
          Group <strong>{group.name}</strong> and its memberships and access grants will be deleted.
          Its members are signed out. The databases themselves are untouched.
        </p>
        <div>
          <details className="group inline-block max-w-full">
            <summary className="inline-flex min-h-[2.75rem] cursor-pointer list-none items-center gap-2 border-2 border-ink bg-paper px-4 py-2 font-semibold text-crimson shadow-[2px_2px_0_rgb(15_31_20/0.18)] hover:bg-crimson-wash">
              <Trash2 className="h-4 w-4" aria-hidden />
              <span>Delete {group.name}</span>
              <ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180" />
            </summary>
            <div className="mt-2 min-w-[min(18rem,80vw)] space-y-3 border-2 border-ink bg-paper p-3 text-sm">
              <p className="m-0">This cannot be undone.</p>
              {deleteError ? <ErrorBanner error={deleteError} /> : null}
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="danger"
                  size="sm"
                  type="button"
                  loading={deleting}
                  onClick={() => void handleDeleteGroup()}
                >
                  Yes, delete group
                </Button>
                {isApiError(deleteError) && deleteError.code === "GROUP_HAS_HISTORY" ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    type="button"
                    loading={deleting}
                    onClick={() => void handleDeactivateGroup()}
                  >
                    Deactivate group instead <OptionalTag />
                  </Button>
                ) : null}
              </div>
            </div>
          </details>
        </div>
      </section>
    </div>
  );
}

export default function GroupsPage() {
  const { groupId } = useParams();
  const location = useLocation();
  const isNew = location.pathname.endsWith("/admin/groups/new");

  if (isNew) {
    return <GroupCreateView />;
  }
  if (groupId) {
    return <GroupEditView groupId={groupId} />;
  }
  return <GroupsListView />;
}
