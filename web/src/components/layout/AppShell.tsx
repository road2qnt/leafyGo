import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  ChevronRight,
  Database as DatabaseIcon,
  LayoutDashboard,
  Leaf,
  LogOut,
  Menu,
  RotateCcw,
  ShieldCheck,
  Users,
  UsersRound,
  X,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";

import { databaseApi } from "../../api/endpoints";
import { isMockMode } from "../../api/mock/mode";
import { useAuth } from "../../auth/AuthContext";
import { initials } from "../../lib/format";
import { cn, OptionalTag } from "../ui";

const isMock = isMockMode();

function Brand() {
  return (
    <Link to="/" className="flex items-center gap-2.5 rounded-lg px-1 py-1">
      <span className="grid h-9 w-9 place-items-center rounded-xl bg-lime text-leaf-900 shadow-inner">
        <Leaf className="h-5 w-5" strokeWidth={2.2} />
      </span>
      <span className="leading-none">
        <span className="block text-[17px] font-semibold tracking-tight text-white">
          leafy<span className="text-lime">Go</span>
        </span>
        <span className="mt-1 block text-[9px] font-medium tracking-[0.22em] text-white/45 uppercase">
          Migration control
        </span>
      </span>
    </Link>
  );
}

function navClass(active: boolean) {
  return cn(
    "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium transition",
    active
      ? "bg-lime/15 text-lime"
      : "text-white/65 hover:bg-white/5 hover:text-white",
  );
}

function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const { user, logout, leaderOfGroups } = useAuth();
  const location = useLocation();
  const isAdmin = Boolean(user?.is_platform_admin);

  const databasesQuery = useQuery({
    queryKey: ["databases", "sidebar"],
    queryFn: databaseApi.list,
    enabled: Boolean(user) && !isAdmin,
    staleTime: 30_000,
  });
  const databases = databasesQuery.data?.databases ?? [];

  const roleLabel = user?.is_platform_admin
    ? "Platform Admin"
    : leaderOfGroups.length > 0
      ? `Leader · ${leaderOfGroups.map((group) => group.name).join(", ")}`
      : "Member";

  return (
    <div className="flex h-full flex-col">
      <div className="px-4 pt-5 pb-4">
        <Brand />
      </div>

      <nav className="scrollbar-thin min-h-0 flex-1 overflow-y-auto px-3 pb-4" aria-label="Main">
        <p className="px-3 pt-4 pb-1.5 text-[10px] font-semibold tracking-[0.2em] text-white/35 uppercase">
          Workspace
        </p>
        <NavLink to="/" end className={({ isActive }) => navClass(isActive)} onClick={onNavigate}>
          <LayoutDashboard className="h-4 w-4" />
          Dashboard
        </NavLink>
        <NavLink to="/workers" className={({ isActive }) => navClass(isActive)} onClick={onNavigate}>
          <Activity className="h-4 w-4" />
          <span className="flex-1">Workers</span>
          <span className="rounded border border-dashed border-white/25 px-1 py-0.5 text-[8px] tracking-wider text-white/40">
            OPT
          </span>
        </NavLink>

        {!isAdmin ? (
          <>
            <p className="px-3 pt-5 pb-1.5 text-[10px] font-semibold tracking-[0.2em] text-white/35 uppercase">
              Databases
            </p>
            {databases.map((database) => {
              const active = location.pathname.startsWith(`/databases/${database.id}`);
              return (
                <NavLink
                  key={database.id}
                  to={`/databases/${database.id}/overview`}
                  className={navClass(active)}
                  onClick={onNavigate}
                >
                  <DatabaseIcon className="h-4 w-4 shrink-0" />
                  <span className="min-w-0 flex-1 truncate">{database.name}</span>
                  {database.lock_state ? (
                    <span
                      className={cn(
                        "h-1.5 w-1.5 shrink-0 rounded-full",
                        database.lock_state === "uncertain_hold"
                          ? "bg-[#c9a7d8]"
                          : "bg-[#e4c266]",
                      )}
                      title={database.lock_state}
                    />
                  ) : null}
                </NavLink>
              );
            })}
            {databases.length === 0 && !databasesQuery.isLoading ? (
              <p className="px-3 py-2 text-xs leading-relaxed text-white/35">
                No database access yet.
              </p>
            ) : null}
          </>
        ) : (
          <>
            <p className="px-3 pt-5 pb-1.5 text-[10px] font-semibold tracking-[0.2em] text-white/35 uppercase">
              Administration
            </p>
            <NavLink to="/admin/users" className={({ isActive }) => navClass(isActive)} onClick={onNavigate}>
              <Users className="h-4 w-4" /> Users
            </NavLink>
            <NavLink to="/admin/groups" className={({ isActive }) => navClass(isActive)} onClick={onNavigate}>
              <UsersRound className="h-4 w-4" /> Groups & access
            </NavLink>
            <NavLink to="/admin/databases" className={({ isActive }) => navClass(isActive)} onClick={onNavigate}>
              <DatabaseIcon className="h-4 w-4" /> Connections
            </NavLink>
            <div className="mx-3 mt-4 rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2.5">
              <p className="flex items-center gap-1.5 text-[11px] font-medium text-white/55">
                <ShieldCheck className="h-3.5 w-3.5" /> Platform Admin
              </p>
              <p className="mt-1 text-[10px] leading-relaxed text-white/35">
                Admin accounts manage the platform, not target database content.
              </p>
            </div>
          </>
        )}

        {isMock ? (
          <div className="mx-3 mt-5 rounded-lg border border-dashed border-lime/30 bg-lime/5 px-3 py-2.5">
            <p className="flex items-center gap-1.5 text-[10px] font-semibold tracking-wider text-lime/80 uppercase">
              Demo data
            </p>
            <p className="mt-1 text-[10px] leading-relaxed text-white/40">
              API is mocked in the browser. Sign in as any demo leader.
            </p>
            <button
              type="button"
              onClick={() => window.__leafygoResetMock?.()}
              className="mt-2 inline-flex items-center gap-1 text-[10px] font-medium text-lime/80 hover:text-lime"
            >
              <RotateCcw className="h-3 w-3" /> Reset demo state
            </button>
          </div>
        ) : null}
      </nav>

      <div className="border-t border-white/10 px-3 py-3">
        <div className="flex items-center gap-2.5 rounded-lg px-2 py-2">
          <span className="grid h-9 w-9 shrink-0 place-items-center rounded-full bg-white/10 text-xs font-semibold text-lime">
            {initials(user?.username ?? "?")}
          </span>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium text-white">{user?.username}</p>
            <p className="truncate text-[10px] text-white/40" title={roleLabel}>
              {roleLabel}
            </p>
          </div>
          <button
            type="button"
            onClick={() => void logout()}
            className="rounded-md p-1.5 text-white/45 transition hover:bg-white/10 hover:text-white"
            aria-label="Sign out"
            title="Sign out"
          >
            <LogOut className="h-4 w-4" />
          </button>
        </div>
      </div>
    </div>
  );
}

export function AppShell() {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const location = useLocation();

  useEffect(() => {
    setDrawerOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!drawerOpen) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [drawerOpen]);

  return (
    <div className="min-h-dvh">
      <aside className="fixed inset-y-0 left-0 z-40 hidden w-64 flex-col bg-leaf-900 lg:flex">        <SidebarContent />
      </aside>

      <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-line bg-surface/95 px-4 backdrop-blur lg:hidden">
        <button
          type="button"
          className="btn btn-ghost btn-icon"
          onClick={() => setDrawerOpen(true)}
          aria-label="Open navigation"
        >
          <Menu className="h-5 w-5" />
        </button>
        <span className="text-sm font-semibold tracking-tight">
          leafy<span className="text-leaf-600">Go</span>
        </span>
        <span className="w-9" aria-hidden />
      </header>

      {drawerOpen ? (
        <div className="fixed inset-0 z-50 lg:hidden">
          <button
            type="button"
            className="absolute inset-0 bg-leaf-900/55 backdrop-blur-[2px]"
            onClick={() => setDrawerOpen(false)}
            aria-label="Close navigation"
          />
          <div className="absolute inset-y-0 left-0 w-[86%] max-w-xs bg-leaf-900 shadow-2xl">
            <button
              type="button"
              onClick={() => setDrawerOpen(false)}
              className="absolute top-3 right-3 z-10 rounded-md p-2 text-white/50 hover:bg-white/10 hover:text-white"
              aria-label="Close navigation"
            >
              <X className="h-4 w-4" />
            </button>
            <SidebarContent onNavigate={() => setDrawerOpen(false)} />
          </div>
        </div>
      ) : null}

      <div className="min-w-0 lg:pl-64">
        <main className="mx-auto w-full max-w-[1500px] min-w-0 px-4 py-6 sm:px-6 lg:px-10 lg:py-8">
          <Outlet />
        </main>
        <footer className="mx-auto flex w-full max-w-[1500px] flex-wrap items-center justify-between gap-2 px-4 pb-6 text-[11px] text-muted sm:px-6 lg:px-10">
          <span className="inline-flex items-center gap-1.5">
            <Leaf className="h-3 w-3 text-leaf-500" /> LeafyGo · IF3110 Milestone 2
          </span>
          <span className="inline-flex items-center gap-1">
            SPA preview <ChevronRight className="h-3 w-3" /> <span className="font-mono">/app</span>
            <OptionalTag label="M2" />
          </span>
        </footer>
      </div>
    </div>
  );
}
