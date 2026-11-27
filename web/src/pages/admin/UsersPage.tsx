import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, Plus, Trash2, UserX } from "lucide-react";
import { useState, type FormEvent } from "react";

import { isApiError } from "../../api/client";
import { adminApi } from "../../api/endpoints";
import type { User } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import {
  Button,
  CheckboxRow,
  EmptyState,
  ErrorBanner,
  Field,
  PageHeader,
  PageLoader,
  TableWrap,
  TextInput,
} from "../../components/ui";
import { reveal } from "../../components/reveal";
import { useToast } from "../../hooks/useToast";
import { formatDateTime } from "../../lib/format";

export default function UsersPage() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const { user: currentUser } = useAuth();
  const usersQuery = useQuery({ queryKey: ["admin", "users"], queryFn: adminApi.users });

  const [selectedUserId, setSelectedUserId] = useState<string | null>(null);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(false);
  const [createError, setCreateError] = useState<unknown>(null);
  const [creating, setCreating] = useState(false);

  const [newPassword, setNewPassword] = useState("");
  const [resetError, setResetError] = useState<unknown>(null);
  const [resetting, setResetting] = useState(false);

  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);
  const [toggling, setToggling] = useState(false);

  const users = usersQuery.data?.users ?? [];
  const adminCount = users.filter((u) => u.is_platform_admin).length;
  const activeAdmins = users.filter((u) => u.is_platform_admin && u.is_active).length;
  const editingUser = users.find((u) => u.id === selectedUserId) ?? null;

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["admin", "users"] });

  const selectForManage = (user: User | null) => {
    setSelectedUserId(user ? user.id : null);
    setNewPassword("");
    setResetError(null);
    setDeleteError(null);
    if (user) reveal("user-form");
  };

  const createUser = async (event: FormEvent) => {
    event.preventDefault();
    setCreating(true);
    setCreateError(null);
    try {
      await adminApi.createUser({
        username: username.trim(),
        password,
        is_platform_admin: isAdmin,
      });
      toast.success("User created", `${username.trim()} can sign in now.`);
      setUsername("");
      setPassword("");
      setIsAdmin(false);
      await invalidate();
    } catch (error) {
      setCreateError(error);
    } finally {
      setCreating(false);
    }
  };

  const resetPassword = async (event: FormEvent) => {
    event.preventDefault();
    if (!editingUser) return;
    setResetting(true);
    setResetError(null);
    try {
      await adminApi.resetPassword(editingUser.id, newPassword);
      toast.success("Password reset", `A new password was set for ${editingUser.username}.`);
      setNewPassword("");
    } catch (error) {
      setResetError(error);
    } finally {
      setResetting(false);
    }
  };

  const toggleActive = async (user: User, nextActive: boolean) => {
    setToggling(true);
    setDeleteError(null);
    try {
      await adminApi.setActive(user.id, nextActive);
      toast.success(
        nextActive ? "User activated" : "User deactivated",
        `${user.username} was ${nextActive ? "activated" : "deactivated"}.`,
      );
      await invalidate();
    } catch (error) {
      toast.error(
        "Could not update user",
        isApiError(error) ? error.message : "Unexpected error.",
      );
    } finally {
      setToggling(false);
    }
  };

  const confirmDelete = async (user: User) => {
    setDeleting(true);
    setDeleteError(null);
    try {
      await adminApi.deleteUser(user.id);
      toast.success("User deleted", `${user.username} had no history and was removed.`);
      setSelectedUserId(null);
      await invalidate();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  if (usersQuery.isLoading) return <PageLoader label="Loading users" />;

  const isSelf = Boolean(editingUser && editingUser.id === currentUser?.id);
  const isLastActiveAdmin = Boolean(
    editingUser && editingUser.is_platform_admin && editingUser.is_active && activeAdmins <= 1,
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title="Users"
        description="Create accounts, reset passwords, and activate or deactivate users."
        actions={
          editingUser ? (
            <Button variant="secondary" onClick={() => selectForManage(null)}>
              <Plus className="h-4 w-4" /> Add new user
            </Button>
          ) : null
        }
      />

      {usersQuery.isError ? (
        <ErrorBanner error={usersQuery.error} onRetry={() => void usersQuery.refetch()} />
      ) : null}

      <div className="split-layout">
        <section aria-labelledby="users-caption" className="min-w-0">
          {users.length > 0 ? (
            <TableWrap>
              <table className="table-base table-stack">
                <caption id="users-caption">
                  {users.length} {users.length === 1 ? "user" : "users"}, {adminCount}{" "}
                  {adminCount === 1 ? "admin" : "admins"}
                </caption>
                <thead>
                  <tr>
                    <th scope="col" className="th">User</th>
                    <th scope="col" className="th">Role</th>
                    <th scope="col" className="th">Status</th>
                    <th scope="col" className="th text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((user) => {
                    const isSelected = editingUser?.id === user.id;
                    return (
                      <tr key={user.id} className={isSelected ? "is-selected" : undefined}>
                        <th scope="row" data-label="User" className="td cell-title">
                          <div>
                            <span className="font-semibold text-ink">{user.username}</span>
                            {user.id === currentUser?.id ? (
                              <span className="ml-1.5 text-sm font-normal text-muted">(you)</span>
                            ) : null}
                            <span className="mt-1 block text-xs font-normal text-muted">
                              created {formatDateTime(user.created_at)}
                            </span>
                          </div>
                        </th>
                        <td data-label="Role" className="td">
                          {user.is_platform_admin ? (
                            <span className="badge badge-neutral">Platform Admin</span>
                          ) : (
                            <span className="badge badge-muted">User</span>
                          )}
                        </td>
                        <td data-label="Status" className="td">
                          {user.is_active ? (
                            <span className="badge badge-success">Active</span>
                          ) : (
                            <span className="badge badge-danger">Inactive</span>
                          )}
                        </td>
                        <td data-label="Actions" className="td cell-actions text-right whitespace-nowrap">
                          <div>
                          <Button
                            variant="secondary"
                            size="sm"
                            aria-current={isSelected ? "true" : undefined}
                            onClick={() => selectForManage(user)}
                          >
                            Manage<span className="sr-only"> {user.username}</span>
                          </Button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </TableWrap>
          ) : (
            <EmptyState
              title="No users yet"
              description="Add the first user with the form beside this list."
            />
          )}
        </section>

        <section
          id="user-form"
          aria-labelledby="user-form-title"
          className="card card-pad flex flex-col gap-6 lg:sticky lg:top-20 lg:self-start"
        >
          {editingUser ? (
            <>
              <div>
                <h2 id="user-form-title" className="m-0 text-xl font-semibold text-ink">
                  Manage {editingUser.username}
                  {isSelf ? <span className="ml-1.5 text-sm font-normal text-muted">(you)</span> : null}
                </h2>
                <p className="m-0 mt-1 text-sm text-muted">
                  {editingUser.is_platform_admin ? "Platform Admin" : "User"} ·{" "}
                  {editingUser.is_active ? "active" : "inactive"}
                </p>
              </div>

              <form className="grid gap-4" onSubmit={resetPassword}>
                <h3 className="section-title">Reset password</h3>
                {resetError ? <ErrorBanner error={resetError} /> : null}
                <Field
                  label="New password"
                  htmlFor="reset-password"
                  required
                  hint={
                    isSelf
                      ? "At least 8 characters. You will be asked to sign in again."
                      : "At least 8 characters. All active sessions of this user will end."
                  }
                >
                  <TextInput
                    id="reset-password"
                    type="password"
                    value={newPassword}
                    onChange={(event) => setNewPassword(event.target.value)}
                    autoComplete="new-password"
                    required
                    minLength={8}
                  />
                </Field>
                <div>
                  <Button type="submit" loading={resetting}>
                    Save password
                  </Button>
                </div>
              </form>

              <div className="space-y-3 border-t border-line-soft pt-4">
                <h3 className="section-title">Account status</h3>
                {isLastActiveAdmin ? (
                  <p className="hint m-0">The last active admin cannot be deactivated.</p>
                ) : isSelf ? (
                  <p className="hint m-0">You cannot deactivate or delete your own account.</p>
                ) : editingUser.is_active ? (
                  <details className="group">
                    <summary className="inline-flex min-h-[2.75rem] cursor-pointer list-none items-center gap-2 border-2 border-ink bg-paper px-4 py-2 font-semibold text-crimson shadow-[2px_2px_0_rgb(15_31_20/0.18)] hover:bg-crimson-wash">
                      <UserX className="h-4 w-4" aria-hidden />
                      <span>Deactivate {editingUser.username}</span>
                      <ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180" />
                    </summary>
                    <div className="mt-2 space-y-3 border-2 border-ink bg-paper p-3 text-sm">
                      <p className="m-0">
                        <strong>{editingUser.username}</strong> can no longer sign in, and all
                        active sessions end. You can activate the account again later.
                      </p>
                      <Button
                        variant="danger"
                        size="sm"
                        type="button"
                        loading={toggling}
                        onClick={() => void toggleActive(editingUser, false)}
                      >
                        Yes, deactivate
                      </Button>
                    </div>
                  </details>
                ) : (
                  <div className="space-y-1.5">
                    <Button
                      variant="secondary"
                      type="button"
                      loading={toggling}
                      onClick={() => void toggleActive(editingUser, true)}
                    >
                      Activate {editingUser.username}
                    </Button>
                    <p className="hint m-0">
                      {editingUser.username} will be able to sign in again.
                    </p>
                  </div>
                )}

                {!isSelf ? (
                  <details className="group pt-2">
                    <summary className="inline-flex min-h-[2.25rem] pointer-coarse:min-h-11 cursor-pointer list-none items-center gap-2 border-2 border-ink bg-paper px-3 py-1 text-sm font-semibold text-crimson shadow-[2px_2px_0_rgb(15_31_20/0.18)] hover:bg-crimson-wash">
                      <Trash2 className="h-4 w-4" aria-hidden />
                      <span>Delete {editingUser.username}</span>
                      <ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180" />
                    </summary>
                    <div className="mt-2 space-y-3 border-2 border-ink bg-paper p-3 text-sm">
                      <p className="m-0">
                        Hard delete only succeeds when the user has never appeared in a proposal,
                        approval, or execution (<code className="font-mono text-xs">USER_HAS_HISTORY</code>).
                      </p>
                      {deleteError ? <ErrorBanner error={deleteError} /> : null}
                      <div className="flex flex-wrap gap-2">
                        <Button
                          variant="danger"
                          size="sm"
                          type="button"
                          loading={deleting}
                          onClick={() => void confirmDelete(editingUser)}
                        >
                          Yes, delete user
                        </Button>
                        {isApiError(deleteError) &&
                        deleteError.code === "USER_HAS_HISTORY" &&
                        editingUser.is_active ? (
                          <Button
                            variant="secondary"
                            size="sm"
                            type="button"
                            loading={toggling}
                            onClick={() => void toggleActive(editingUser, false)}
                          >
                            Deactivate instead
                          </Button>
                        ) : null}
                      </div>
                    </div>
                  </details>
                ) : null}
              </div>

              <div className="border-t border-line-soft pt-3">
                <Button variant="ghost" type="button" onClick={() => selectForManage(null)}>
                  Close
                </Button>
              </div>
            </>
          ) : (
            <>
              <h2 id="user-form-title" className="m-0 text-xl font-semibold text-ink">
                Add user
              </h2>
              {createError ? <ErrorBanner error={createError} /> : null}
              <form className="grid gap-4" onSubmit={createUser}>
                <Field
                  label="Username"
                  htmlFor="new-username"
                  required
                  hint="3 to 64 characters: letters, digits, dots, underscores, or hyphens."
                >
                  <TextInput
                    id="new-username"
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    autoComplete="off"
                    required
                  />
                </Field>
                <Field
                  label="Password"
                  htmlFor="new-password"
                  required
                  hint="At least 8 characters."
                >
                  <TextInput
                    id="new-password"
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    autoComplete="new-password"
                    required
                    minLength={8}
                  />
                </Field>
                <div className="space-y-1">
                  <CheckboxRow
                    label="Create as Platform Admin"
                    description="Permanent. Platform Admins only manage the platform and cannot access databases."
                    checked={isAdmin}
                    onChange={setIsAdmin}
                  />
                </div>
                <div className="pt-2">
                  <Button type="submit" loading={creating}>
                    Add user
                  </Button>
                </div>
              </form>
            </>
          )}
        </section>
      </div>
    </div>
  );
}
