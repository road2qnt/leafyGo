import { ArrowRight, Eye, EyeOff, GitBranch, Leaf, Layers, ShieldCheck } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router";

import { useAuth } from "../auth/AuthContext";
import { isMockMode } from "../api/mock/mode";
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
    <div className="grid min-h-dvh bg-paper lg:grid-cols-[1.05fr_1fr]">
      <section className="relative hidden overflow-hidden bg-leaf-900 p-10 text-white lg:flex lg:flex-col lg:justify-between">
        <div className="absolute -top-24 -right-24 h-80 w-80 rounded-full bg-lime/10 blur-3xl" />
        <div className="absolute -bottom-32 -left-20 h-96 w-96 rounded-full bg-leaf-500/20 blur-3xl" />
        <div
          className="absolute inset-0 opacity-[0.06]"
          style={{
            backgroundImage:
              "linear-gradient(to right, #fff 1px, transparent 1px), linear-gradient(to bottom, #fff 1px, transparent 1px)",
            backgroundSize: "44px 44px",
          }}
        />

        <div className="relative">
          <div className="flex items-center gap-2.5">
            <span className="grid h-10 w-10 place-items-center rounded-xl bg-lime text-leaf-900">
              <Leaf className="h-5 w-5" strokeWidth={2.2} />
            </span>
            <span className="text-xl font-semibold tracking-tight">
              leafy<span className="text-lime">Go</span>
            </span>
          </div>

          <h1 className="mt-16 max-w-md text-balance text-4xl font-semibold leading-[1.12] tracking-tight">
            Ship database changes without holding your breath.
          </h1>
          <p className="mt-4 max-w-md text-sm leading-relaxed text-white/55">
            Multi-group approvals, immutable migration history, LIFO rollback, and a live
            queue monitor — built on PostgreSQL, not another broker.
          </p>

          <ul className="mt-9 space-y-4 text-sm">
            <li className="flex items-start gap-3">
              <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-white/10 text-lime">
                <ShieldCheck className="h-4 w-4" />
              </span>
              <span>
                <span className="block font-medium text-white">Approval quorum, snapshotted</span>
                <span className="text-xs text-white/45">
                  Every group leader must approve; access changes are caught at revalidation.
                </span>
              </span>
            </li>
            <li className="flex items-start gap-3">
              <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-white/10 text-lime">
                <Layers className="h-4 w-4" />
              </span>
              <span>
                <span className="block font-medium text-white">Batch checkpoints you can resume</span>
                <span className="text-xs text-white/45">
                  Cursor-scoped batches, crash-safe evidence, partial and uncertain holds made visible.
                </span>
              </span>
            </li>
            <li className="flex items-start gap-3">
              <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-white/10 text-lime">
                <GitBranch className="h-4 w-4" />
              </span>
              <span>
                <span className="block font-medium text-white">Trace the whole chain</span>
                <span className="text-xs text-white/45">
                  API → queue → worker → lock → revalidation → target DB, correlated in Grafana.
                </span>
              </span>
            </li>
          </ul>
        </div>

        <div className="relative mt-12 rounded-xl border border-white/10 bg-white/[0.04] p-4 font-mono text-[10px] leading-relaxed text-white/45">
          <p className="mb-2 text-[9px] font-semibold tracking-[0.2em] text-lime/70 uppercase">
            traceparent
          </p>
          <p className="break-all text-white/70">00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01</p>
          <p className="mt-2">
            HTTP approve <span className="text-white/25">→</span> queue.publish{" "}
            <span className="text-white/25">→</span> worker.process_job{" "}
            <span className="text-white/25">→</span> target.db.query
          </p>
        </div>
      </section>

      <section className="flex items-center justify-center px-4 py-10 sm:px-8">
        <div className="w-full max-w-md">
          <div className="mb-8 flex items-center gap-2.5 lg:hidden">
            <span className="grid h-10 w-10 place-items-center rounded-xl bg-leaf-900 text-lime">
              <Leaf className="h-5 w-5" strokeWidth={2.2} />
            </span>
            <span className="text-xl font-semibold tracking-tight">
              leafy<span className="text-leaf-600">Go</span>
            </span>
          </div>

          <p className="text-[11px] font-semibold tracking-[0.18em] text-leaf-600 uppercase">
            Milestone 2 · SPA
          </p>
          <h2 className="mt-2 text-2xl font-semibold tracking-tight text-ink">Sign in to LeafyGo</h2>
          <p className="mt-1.5 text-sm text-muted">
            Use your platform account. Group leaders approve migrations; members submit them.
          </p>

          <form className="mt-7 space-y-4" onSubmit={submit}>
            {error ? <ErrorBanner error={error} /> : null}
            <Field label="Username" htmlFor="username" required>
              <TextInput
                id="username"
                name="username"
                autoComplete="username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                placeholder="jibril"
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
                  className="pr-10"
                  placeholder="••••••••"
                  required
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((current) => !current)}
                  className="absolute top-1/2 right-2 -translate-y-1/2 rounded p-1.5 text-muted hover:text-ink"
                  aria-label={showPassword ? "Hide password" : "Show password"}
                >
                  {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
            </Field>
            <Button type="submit" loading={busy} className="w-full">
              Sign in <ArrowRight className="h-4 w-4" />
            </Button>
          </form>

          {isMock ? (
            <div className="mt-8">
              <div className="flex items-center gap-2">
                <p className="text-[11px] font-semibold tracking-[0.14em] text-muted uppercase">
                  Demo accounts
                </p>
                <span className="tag-optional">Mock API</span>
              </div>
              <div className="mt-2 grid grid-cols-2 gap-2">
                {demoAccounts.map((account) => (
                  <button
                    key={account.username}
                    type="button"
                    onClick={() => useDemo(account)}
                    className="rounded-lg border border-line bg-surface px-3 py-2 text-left transition hover:border-leaf-300 hover:bg-leaf-50"
                  >
                    <span className="block text-xs font-semibold text-ink">{account.label}</span>
                    <span className="mt-0.5 block text-[10px] leading-snug text-muted">{account.role}</span>
                  </button>
                ))}
              </div>
              <p className="mt-2 text-[11px] leading-relaxed text-muted">
                Tip: sign in as <strong>putra</strong> to give the last approval on the pending
                Inventory proposal. Sign in as <strong>jibril</strong> to hit the blocked People Ops
                predecessor gate.
              </p>
            </div>
          ) : null}

          <p className="mt-8 text-center text-[11px] text-muted">
            Sessions are opaque, HttpOnly, and rotate automatically.
          </p>
        </div>
      </section>
    </div>
  );
}
