import { Eye, EyeOff } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router";

import { useAuth } from "../auth/AuthContext";
import { isMockMode } from "../api/mock/mode";
import { LeafyLogo } from "../components/layout/AppShell";
import { Button, ErrorBanner, Field, TextInput } from "../components/ui";

const isMock = isMockMode();

const demoAccounts = [
  { username: "jibril", password: "password123", label: "Jibril", role: "Platform Core leader" },
  { username: "putra", password: "password123", label: "Putra", role: "Data Ops leader" },
  { username: "ega", password: "password123", label: "Ega", role: "Analytics Guild leader" },
  { username: "admin", password: "admin12345", label: "Admin", role: "Platform Admin" },
];

export default function LoginPage() {
  const { user, login, loading } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? "/";

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  if (!loading && user) {
    return <Navigate to={from} replace />;
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await login(username.trim(), password);
      navigate(from, { replace: true });
    } catch (cause) {
      setError(cause);
    } finally {
      setBusy(false);
    }
  };

  const useDemo = (account: (typeof demoAccounts)[number]) => {
    setUsername(account.username);
    setPassword(account.password);
    setError(null);
  };

  return (
    <div className="grid min-h-dvh place-items-center bg-canvas px-4 py-6 sm:py-8">
      <div className="w-full max-w-[26rem]">
        <div className="card px-4 py-6 sm:px-6 sm:py-8">
          <h1 className="m-0 mb-2 flex items-center justify-center gap-3 text-[clamp(1.625rem,1.3rem+1.4vw,2.25rem)] font-bold tracking-[-0.01em] text-ink">
            <LeafyLogo className="h-11 w-11" variant="auth" />
            <span>
              <span className="sr-only">Sign in to </span>
              leafy<span className="text-leaf-600">Go</span>
            </span>
          </h1>
          <p className="m-0 mb-5 text-center text-sm text-ink-soft">
            Sign in with your platform account.
          </p>

          <form className="grid gap-4" onSubmit={submit}>
            {error ? <ErrorBanner error={error} /> : null}
            <Field label="Username" htmlFor="username" required>
              <TextInput
                id="username"
                name="username"
                autoComplete="username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                required
                autoFocus
              />
            </Field>
            <Field label="Password" htmlFor="password" required>
              <div className="relative">
                <TextInput
                  id="password"
                  name="password"
                  type={showPassword ? "text" : "password"}
                  autoComplete="current-password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  className="pr-12"
                  required
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((current) => !current)}
                  aria-pressed={showPassword}
                  className="absolute top-1/2 right-1 grid h-9 w-9 pointer-coarse:right-0.5 pointer-coarse:h-10 pointer-coarse:w-11 -translate-y-1/2 cursor-pointer place-items-center border-0 bg-transparent text-ink-soft hover:bg-leaf-50 hover:text-ink"
                  aria-label={showPassword ? "Hide password" : "Show password"}
                >
                  {showPassword ? <EyeOff className="h-5 w-5" /> : <Eye className="h-5 w-5" />}
                </button>
              </div>
            </Field>
            <div className="pt-2">
              <Button type="submit" loading={busy} className="w-full">
                Sign in
              </Button>
            </div>
          </form>

          {isMock ? (
            <div className="mt-6 border-t border-line-soft pt-4">
              <div className="flex items-center justify-between gap-2">
                <p className="m-0 text-xs font-semibold tracking-wider text-ink-soft uppercase">
                  Demo accounts
                </p>
                <span className="tag-optional">Mock API</span>
              </div>
              <div className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
                {demoAccounts.map((account) => (
                  <button
                    key={account.username}
                    type="button"
                    onClick={() => useDemo(account)}
                    className="cursor-pointer border-2 border-ink bg-paper px-3 py-2 text-left shadow-[2px_2px_0_rgb(15_31_20/0.18)] transition-[transform,box-shadow,background-color] duration-75 hover:bg-leaf-50 active:translate-x-0.5 active:translate-y-0.5 active:shadow-none"
                  >
                    <span className="block text-xs font-semibold text-ink">{account.label}</span>
                    <span className="mt-0.5 block text-[11px] leading-snug text-muted">
                      {account.role}
                    </span>
                  </button>
                ))}
              </div>
              <p className="mt-2.5 mb-0 text-xs leading-relaxed text-muted">
                Tip: sign in as <strong>putra</strong> to give the final approval on the pending
                Inventory proposal, or <strong>jibril</strong> to hit the blocked People Ops
                predecessor gate.
              </p>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
