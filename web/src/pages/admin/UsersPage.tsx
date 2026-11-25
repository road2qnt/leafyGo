import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RotateCcw, ShieldCheck, Trash2, UserCog, UserX } from "lucide-react";
import { useState, type FormEvent } from "react";

import { isApiError } from "../../api/client";
import { adminApi } from "../../api/endpoints";
import type { User } from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import {
  Button,
  CheckboxRow,
  ConfirmDialog,
  ErrorBanner,
  Field,
  Modal,
  PageHeader,
  PageLoader,
  TableEmpty,
  TableWrap,
  TextInput,
} from "../../components/ui";
import { useToast } from "../../hooks/useToast";
import { formatDateTime } from "../../lib/format";

export default function UsersPage() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const { user: currentUser } = useAuth();
  const usersQuery = useQuery({ queryKey: ["admin", "users"], queryFn: adminApi.users });

  const [createOpen, setCreateOpen] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(false);
  const [createError, setCreateError] = useState<unknown>(null);
  const [creating, setCreating] = useState(false);

  const [resetUser, setResetUser] = useState<User | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [resetError, setResetError] = useState<unknown>(null);
  const [resetting, setResetting] = useState(false);

  const [deleteUser, setDeleteUser] = useState<User | null>(null);
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleting, setDeleting] = useState(false);
  const [togglingId, setTogglingId] = useState<string | null>(null);

  const users = usersQuery.data?.users ?? [];

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["admin", "users"] });

  const createUser = async (event: FormEvent) => {
    event.preventDefault();
    setCreating(true);
    setCreateError(null);
    try {
      await adminApi.createUser({ username: username.trim(), password, is_platform_admin: isAdmin });
      toast.success("User created", `${username.trim()} can sign in now.`);
      setCreateOpen(false);
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
    if (!resetUser) return;
    setResetting(true);
    setResetError(null);
    try {
      await adminApi.resetPassword(resetUser.id, newPassword);
      toast.success("Password reset", `A new password was set for ${resetUser.username}.`);
      setResetUser(null);
      setNewPassword("");
    } catch (error) {
      setResetError(error);
    } finally {
      setResetting(false);
    }
  };

  const toggleActive = async (user: User) => {
    setTogglingId(user.id);
    try {
      await adminApi.setActive(user.id, !user.is_active);
      toast.success(
        user.is_active ? "User deactivated" : "User activated",
        `${user.username} was ${user.is_active ? "deactivated" : "activated"}.`,
      );
      await invalidate();
    } catch (error) {
      toast.error("Could not update user", isApiError(error) ? error.message : "Unexpected error.");
    } finally {
      setTogglingId(null);
    }
  };

  const confirmDelete = async () => {
    if (!deleteUser) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await adminApi.deleteUser(deleteUser.id);
      toast.success("User deleted", `${deleteUser.username} had no history and was removed.`);
      setDeleteUser(null);
      await invalidate();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  const deactivateInstead = async () => {
    if (!deleteUser) return;
    setDeleting(true);
    try {
      await adminApi.setActive(deleteUser.id, false);
      toast.success("User deactivated", `${deleteUser.username} keeps history but can no longer sign in.`);
      setDeleteUser(null);
      setDeleteError(null);
      await invalidate();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleting(false);
    }
  };

  if (usersQuery.isLoading) return <PageLoader label="Loading users" />;

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow="Administration"
        title="Users"
        description="Create accounts, rotate passwords, and deactivate users that already appear in approval or execution history."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="h-4 w-4" /> New user
          </Button>
        }
      />

      {usersQuery.isError ? (
        <ErrorBanner error={usersQuery.error} onRetry={() => void usersQuery.refetch()} />
      ) : null}

      <TableWrap>
        <table className="table-base min-w-[820px]">
          <thead>
            <tr>
              <th className="th">Username</th>
              <th className="th">Role</th>
              <th className="th">Status</th>
              <th className="th">Created</th>
              <th className="th text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {users.map((user) => (
              <tr key={user.id} className="tr-hover">
                <td className="td">
                  <div className="flex items-center gap-2.5">
                    <span className="grid h-8 w-8 place-items-center rounded-full bg-leaf-50 text-[10px] font-semibold text-leaf-700">
                      {user.username.slice(0, 2).toUpperCase()}
                    </span>
                    <div>
                      <p className="text-sm font-medium text-ink">{user.username}</p>
                      <p className="font-mono text-[10px] text-muted">{user.id}</p>
                    </div>
                  </div>
                </td>
                <td className="td">
                  {user.is_platform_admin ? (
                    <span className="badge badge-plum">
                      <ShieldCheck className="h-3 w-3" /> Platform Admin
                    </span>
                  ) : (
                    <span className="badge badge-neutral">Member</span>
                  )}
                </td>
                <td className="td">
                  <span className={`badge ${user.is_active ? "badge-success" : "badge-danger"}`}>
                    {user.is_active ? "Active" : "Inactive"}
                  </span>
                </td>
                <td className="td text-xs text-muted">{formatDateTime(user.created_at)}</td>
                <td className="td">
                  <div className="flex justify-end gap-1">
                    <Button variant="ghost" size="sm" onClick={() => { setResetUser(user); setNewPassword(""); setResetError(null); }}>
                      <RotateCcw className="h-3.5 w-3.5" /> Password
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      loading={togglingId === user.id}
                      disabled={user.id === currentUser?.id}
                      title={user.id === currentUser?.id ? "You cannot deactivate yourself" : undefined}
                      onClick={() => void toggleActive(user)}
                    >
                      {user.is_active ? <UserX className="h-3.5 w-3.5" /> : <UserCog className="h-3.5 w-3.5" />}
                      {user.is_active ? "Deactivate" : "Activate"}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-[#8b3323] hover:bg-[#fbebe6]"
                      disabled={user.id === currentUser?.id}
                      title={user.id === currentUser?.id ? "You cannot delete yourself" : undefined}
                      onClick={() => { setDeleteUser(user); setDeleteError(null); }}
                    >
                      <Trash2 className="h-3.5 w-3.5" /> Delete
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
            {users.length === 0 ? <TableEmpty colSpan={5}>No users found.</TableEmpty> : null}
          </tbody>
        </table>
      </TableWrap>

      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="Create user"
        description="Passwords are stored as Argon2id hashes. New accounts can sign in immediately."
        footer={
          <>
            <Button variant="secondary" onClick={() => setCreateOpen(false)} disabled={creating}>
              Cancel
            </Button>
            <Button type="submit" form="create-user-form" loading={creating}>
              Create user
            </Button>
          </>
        }
      >
        <form id="create-user-form" className="space-y-4" onSubmit={createUser}>
          {createError ? <ErrorBanner error={createError} /> : null}
          <Field label="Username" htmlFor="new-username" required hint="3–32 letters, digits, dots, underscores, or dashes.">
            <TextInput
              id="new-username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              autoComplete="off"
              required
            />
          </Field>
          <Field label="Initial password" htmlFor="new-password" required hint="At least 8 characters.">
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
          <CheckboxRow
            label="Platform Admin"
            description="Admins manage accounts and connections but cannot open target database tools."
            checked={isAdmin}
            onChange={setIsAdmin}
          />
        </form>
      </Modal>

      <Modal
        open={resetUser !== null}
        onClose={() => setResetUser(null)}
        title={`Reset password · ${resetUser?.username ?? ""}`}
        description="Existing sessions stay valid until they idle out; rotate credentials after the reset."
        size="sm"
        footer={
          <>
            <Button variant="secondary" onClick={() => setResetUser(null)} disabled={resetting}>
              Cancel
            </Button>
            <Button type="submit" form="reset-password-form" loading={resetting}>
              Reset password
            </Button>
          </>
        }
      >
        <form id="reset-password-form" className="space-y-4" onSubmit={resetPassword}>
          {resetError ? <ErrorBanner error={resetError} /> : null}
          <Field label="New password" htmlFor="reset-password" required hint="At least 8 characters.">
            <TextInput
              id="reset-password"
              type="password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
              autoComplete="new-password"
              required
              minLength={8}
              autoFocus
            />
          </Field>
        </form>
      </Modal>

      <ConfirmDialog
        open={deleteUser !== null}
        onClose={() => { setDeleteUser(null); setDeleteError(null); }}
        onConfirm={() => void confirmDelete()}
        title={`Delete ${deleteUser?.username ?? "user"}?`}
        description={
          <div className="space-y-3">
            <p>
              Hard delete only works when the user has never appeared in a proposal, approval, or
              execution. Otherwise the API returns <code className="font-mono text-xs">USER_HAS_HISTORY</code>.
            </p>
            {deleteError ? (
              <>
                <ErrorBanner error={deleteError} />
                {isApiError(deleteError) && deleteError.code === "USER_HAS_HISTORY" ? (
                  <Button variant="secondary" onClick={() => void deactivateInstead()} loading={deleting}>
                    <UserX className="h-4 w-4" /> Deactivate instead
                  </Button>
                ) : null}
              </>
            ) : null}
          </div>
        }
        confirmLabel="Delete user"
        loading={deleting}
      />
    </div>
  );
}
