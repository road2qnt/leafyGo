import {
  ChevronDown,
  LogOut,
  Menu,
  RotateCcw,
  User as UserIcon,
  X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";

import { isMockMode } from "../../api/mock/mode";
import { useAuth } from "../../auth/AuthContext";
import { cn } from "../ui";

const isMock = isMockMode();

export function LeafyLogo({
  className = "h-8 w-8",
  variant = "header",
}: {
  className?: string;
  variant?: "header" | "auth";
}) {
  const leafColor = variant === "header" ? "#c9ecc4" : "#2a8f4a";
  const cutColor = variant === "header" ? "#145a2c" : "#ffffff";
  const bodyColor = variant === "header" ? "#ffffff" : "#0d3b1e";

  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0.235 0.27 24 24"
      className={className}
      aria-hidden="true"
      focusable="false"
    >
      <path
        fill={bodyColor}
        d="M5 13 C4 7 8 2.5 13.5 2.5 C18 2.5 21 5.5 21 10 L21 17 C21 18.5 22 19.2 23 18.6 L23 20.8 C20.6 22 18 20.6 18 17.6 L18 15 C16 16.5 13 17 10.5 16.5 C8 16 5.6 15.2 5 13 Z"
      />
      <path
        fill={leafColor}
        stroke={cutColor}
        strokeWidth="1"
        d="M14 5.5 C7.5 4.5 2.5 10 2 21.5 C10 21 15 15 14 5.5 Z"
      />
      <path
        fill={cutColor}
        d="M13 5.6 L14 5.5 L14.1 7 C10.4 11.2 6.9 15.6 4 20 C6.3 15.3 9.6 10.2 13 5.6 Z"
      />
      <circle fill={cutColor} cx="17.4" cy="8.2" r="1.2" />
    </svg>
  );
}

export function AppShell() {
  const { user, logout, leaderOfGroups } = useAuth();
  const location = useLocation();
  const isAdmin = Boolean(user?.is_platform_admin);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const userMenuRef = useRef<HTMLDivElement>(null);
  const [navOpen, setNavOpen] = useState(false);
  const headerRef = useRef<HTMLElement>(null);
  const navToggleRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    setNavOpen(false);
    setUserMenuOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    const onClickOutside = (event: MouseEvent) => {
      if (userMenuRef.current && !userMenuRef.current.contains(event.target as Node)) {
        setUserMenuOpen(false);
      }
      if (headerRef.current && !headerRef.current.contains(event.target as Node)) {
        setNavOpen(false);
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setUserMenuOpen(false);
        if (navToggleRef.current?.getAttribute("aria-expanded") === "true") {
          navToggleRef.current.focus();
        }
        setNavOpen(false);
      }
    };
    document.addEventListener("mousedown", onClickOutside);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onClickOutside);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  const roleLabel = user?.is_platform_admin
    ? "Platform Admin"
    : leaderOfGroups.length > 0
      ? `Leader · ${leaderOfGroups.map((group) => group.name).join(", ")}`
      : "Member";

  const navItems = [
    { to: "/", end: true, label: isAdmin ? "Overview" : "Databases" },
    ...(isAdmin
      ? [
          { to: "/admin/users", end: false, label: "Users" },
          { to: "/admin/groups", end: false, label: "Groups" },
          { to: "/admin/databases", end: false, label: "Connections" },
        ]
      : []),
  ];

  return (
    <div className="flex min-h-dvh flex-col bg-canvas text-ink">
      <a
        href="#main"
        className="fixed top-2 left-2 z-50 -translate-y-[200%] border-2 border-ink bg-lime px-4 py-2 font-semibold text-ink focus:translate-y-0"
      >
        Skip to main content
      </a>

      <header className="site-header" ref={headerRef}>
        <div className="site-container flex items-center gap-4 py-2 md:py-3 lg:gap-6">
          <Link
            to="/"
            className="inline-flex min-h-[2.75rem] items-center gap-2 text-lg font-bold tracking-[-0.01em] text-paper no-underline hover:text-paper"
            aria-label="leafyGo home"
          >
            <LeafyLogo className="h-8 w-8" variant="header" />
            <span className="relative -top-[0.05em]">
              leafy<span className="text-lime">Go</span>
            </span>
          </Link>

          <nav className="hidden flex-1 items-center gap-1 md:flex" aria-label="Main">
            {navItems.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                className={({ isActive }) => cn("site-nav-link", isActive && "site-nav-link-active")}
              >
                {item.label}
              </NavLink>
            ))}
          </nav>

          <button
            ref={navToggleRef}
            type="button"
            onClick={() => setNavOpen((open) => !open)}
            aria-expanded={navOpen}
            aria-controls="mobile-nav"
            className={cn(
              "ml-auto inline-flex min-h-[2.75rem] cursor-pointer items-center gap-2 border-2 bg-transparent px-3 font-semibold text-paper md:hidden",
              navOpen ? "border-paper" : "border-lime",
            )}
          >
            {navOpen ? <X className="h-5 w-5" aria-hidden /> : <Menu className="h-5 w-5" aria-hidden />}
            Menu
          </button>

          <div className="ml-auto hidden items-center gap-2 md:flex">
            {isMock ? (
              <button
                type="button"
                onClick={() => window.__leafygoResetMock?.()}
                className="inline-flex min-h-[2.25rem] cursor-pointer items-center gap-1.5 border-2 border-dashed border-lime bg-leaf-900/50 px-2.5 py-1 text-xs font-semibold text-lime hover:bg-leaf-900 max-lg:min-h-[2.75rem] max-lg:min-w-[2.75rem] max-lg:justify-center"
                title="Mock API active — click to reset demo state"
              >
                <RotateCcw className="h-3.5 w-3.5" aria-hidden />
                <span className="max-lg:sr-only">Reset Demo</span>
              </button>
            ) : null}

            <div className="relative" ref={userMenuRef}>
              <button
                type="button"
                onClick={() => setUserMenuOpen((open) => !open)}
                aria-expanded={userMenuOpen}
                className={cn(
                  "inline-flex min-h-[2.75rem] cursor-pointer items-center gap-2 border-2 border-transparent bg-transparent px-3 py-1 font-semibold text-paper hover:border-paper",
                  userMenuOpen && "border-paper",
                )}
              >
                <UserIcon className="h-4 w-4" aria-hidden />
                <span className="sr-only">Account menu, signed in as </span>
                <span>{user?.username}</span>
                <ChevronDown
                  className={cn("h-4 w-4 transition-transform", userMenuOpen && "rotate-180")}
                  aria-hidden
                />
              </button>

              {userMenuOpen ? (
                <div className="absolute top-[calc(100%+0.5rem)] right-0 z-40 min-w-52 border-2 border-ink bg-paper text-sm text-ink shadow-[4px_4px_0_rgb(15_31_20/0.18)]">
                  <div className="border-b-2 border-ink bg-leaf-50 px-3 py-2">
                    <p className="font-semibold text-ink">{user?.username}</p>
                    <p className="mt-0.5 text-xs text-ink-soft">{roleLabel}</p>
                  </div>
                  <button
                    type="button"
                    onClick={() => void logout()}
                    className="flex w-full cursor-pointer items-center gap-2 border-0 bg-transparent px-3 py-2.5 text-left font-semibold text-ink hover:bg-leaf-50"
                  >
                    <LogOut className="h-4 w-4" aria-hidden />
                    Sign out
                  </button>
                </div>
              ) : null}
            </div>
          </div>
        </div>

        {navOpen ? (
          <div
            id="mobile-nav"
            className="absolute inset-x-0 top-full max-h-[calc(100dvh-4rem)] overflow-y-auto border-y-4 border-ink bg-paper text-ink shadow-[0_6px_0_rgb(15_31_20/0.18)] md:hidden"
          >
            <div className="site-container py-3">
              <nav className="grid gap-1" aria-label="Main">
                {navItems.map((item) => (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    end={item.end}
                    onClick={() => setNavOpen(false)}
                    className="flex min-h-[2.75rem] items-center border-2 border-transparent px-3 font-semibold text-ink no-underline hover:bg-leaf-50 aria-[current=page]:border-ink aria-[current=page]:bg-leaf-200"
                  >
                    {item.label}
                  </NavLink>
                ))}
              </nav>
              <div className="mt-3 border-t border-line-soft px-3 pt-3">
                <p className="m-0 flex items-center gap-2 font-semibold break-all">
                  <UserIcon className="h-4 w-4 shrink-0" aria-hidden />
                  <span className="sr-only">Signed in as </span>
                  {user?.username}
                </p>
                <p className="m-0 mt-0.5 text-xs text-ink-soft">{roleLabel}</p>
                <div className="mt-3 flex flex-wrap gap-2">
                  <button type="button" onClick={() => void logout()} className="btn btn-secondary flex-1">
                    <LogOut className="h-4 w-4" aria-hidden />
                    Sign out
                  </button>
                  {isMock ? (
                    <button
                      type="button"
                      onClick={() => window.__leafygoResetMock?.()}
                      className="btn btn-secondary flex-1 border-dashed"
                      title="Mock API active — click to reset demo state"
                    >
                      <RotateCcw className="h-4 w-4" aria-hidden />
                      Reset Demo
                    </button>
                  ) : null}
                </div>
              </div>
            </div>
          </div>
        ) : null}
      </header>

      <main id="main" className="flex-1 pt-5 pb-10 sm:pt-8 sm:pb-12" tabIndex={-1}>
        <div className="site-container">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
