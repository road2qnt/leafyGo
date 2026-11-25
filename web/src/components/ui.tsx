import {
  AlertTriangle,
  Check,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Copy,
  Info,
  Loader2,
  Search,
  X,
  type LucideIcon,
} from "lucide-react";
import {
  useEffect,
  useId,
  useState,
  type ComponentProps,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { NavLink } from "react-router";

import { isApiError } from "../api/client";
import { errorGuidance, type Tone } from "../lib/status";
import {
  ACTION_LABELS,
  JOB_STATUS_META,
  LOCK_STATE_META,
  PROPOSAL_STATUS_META,
} from "../lib/status";
import type { ActionType, JobStatus, LockState, ProposalStatus } from "../api/types";

export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

/* ------------------------------------------------------------------ buttons */

const buttonVariants = {
  primary: "btn-primary",
  secondary: "btn-secondary",
  ghost: "btn-ghost",
  danger: "btn-danger",
  lime: "btn-lime",
} as const;

type ButtonVariant = keyof typeof buttonVariants;

export interface ButtonProps extends Omit<ComponentProps<"button">, "size"> {
  variant?: ButtonVariant;
  size?: "sm" | "md";
  loading?: boolean;
}

export function Button({
  variant = "primary",
  size = "md",
  loading = false,
  className,
  children,
  disabled,
  ...rest
}: ButtonProps) {
  return (
    <button
      className={cn("btn", buttonVariants[variant], size === "sm" && "btn-sm", className)}
      disabled={disabled || loading}
      {...rest}
    >
      {loading ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden /> : null}
      {children}
    </button>
  );
}

export function buttonClass(variant: ButtonVariant = "primary", extra?: string) {
  return cn("btn", buttonVariants[variant], extra);
}

/* ------------------------------------------------------------------- badges */

const toneBadge: Record<Tone, string> = {
  neutral: "badge-neutral",
  success: "badge-success",
  warning: "badge-warning",
  danger: "badge-danger",
  info: "badge-info",
  plum: "badge-plum",
};

const toneDot: Record<Tone, string> = {
  neutral: "bg-[#8a948d]",
  success: "bg-leaf-500",
  warning: "bg-[#b38327]",
  danger: "bg-[#a33a2b]",
  info: "bg-ocean",
  plum: "bg-plum",
};

interface StatusBadgeProps {
  status: JobStatus | ProposalStatus | LockState | string | null | undefined;
  kind?: "job" | "proposal" | "lock" | "auto";
  className?: string;
}

export function StatusBadge({ status, kind = "job", className }: StatusBadgeProps) {
  let tone: Tone = "neutral";
  let label = "—";
  let active = false;
  if (status) {
    if (kind === "lock") {
      const meta = LOCK_STATE_META[status as LockState];
      if (meta) {
        tone = meta.tone;
        label = meta.label;
        active = meta.active;
      }
    } else if (kind === "proposal" || (kind === "auto" && !JOB_STATUS_META[status as JobStatus] && PROPOSAL_STATUS_META[status as ProposalStatus])) {
      const meta = PROPOSAL_STATUS_META[status as ProposalStatus];
      if (meta) {
        tone = meta.tone;
        label = meta.label;
        active = meta.active;
      }
    } else {
      const meta = JOB_STATUS_META[status as JobStatus];
      if (meta) {
        tone = meta.tone;
        label = meta.label;
        active = meta.active;
      }
    }
  }
  return (
    <span className={cn("badge", toneBadge[tone], className)}>
      <span
        className={cn(
          "h-1.5 w-1.5 rounded-full",
          toneDot[tone],
          active && tone === "info" && "animate-pulse",
        )}
        aria-hidden
      />
      {label}
    </span>
  );
}

export function ActionBadge({ action }: { action: ActionType | string | null | undefined }) {
  const label = action ? (ACTION_LABELS[action as ActionType] ?? action) : "—";
  return <span className="badge badge-neutral font-mono">{label}</span>;
}

export function LevelBadge({ level }: { level: "view" | "edit" | undefined | null }) {
  if (!level) return <span className="badge badge-neutral">No access</span>;
  return (
    <span className={cn("badge", level === "edit" ? "badge-success" : "badge-info")}>
      {level === "edit" ? "View + Edit" : "View only"}
    </span>
  );
}

export function OptionalTag({ label = "Optional" }: { label?: string }) {
  return (
    <span className="tag-optional" title="Optional M2 feature; safe fallback exists if omitted.">
      {label}
    </span>
  );
}

/* -------------------------------------------------------------------- cards */

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("card", className)}>{children}</div>;
}

export function StatCard({
  label,
  value,
  hint,
  icon: Icon,
}: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  icon?: LucideIcon;
}) {
  return (
    <div className="card p-4 sm:p-5">
      <div className="flex items-start justify-between gap-3">
        <p className="stat-label">{label}</p>
        {Icon ? <Icon className="h-4 w-4 text-leaf-500" aria-hidden /> : null}
      </div>
      <p className="stat-value">{value}</p>
      {hint ? <p className="mt-1 text-xs text-muted">{hint}</p> : null}
    </div>
  );
}

export function Callout({
  tone = "info",
  title,
  children,
  action,
}: {
  tone?: Tone;
  title?: ReactNode;
  children: ReactNode;
  action?: ReactNode;
}) {
  const styles: Record<Tone, string> = {
    neutral: "border-line bg-[#f4f3ec] text-ink",
    success: "border-leaf-200 bg-leaf-50 text-leaf-900",
    warning: "border-[#ead9ab] bg-[#fbf4dd] text-[#5f4810]",
    danger: "border-[#e9c3b9] bg-[#fbebe6] text-[#722c1f]",
    info: "border-[#bdd8e4] bg-[#edf6fa] text-[#204f66]",
    plum: "border-[#d5c6e6] bg-[#f4eefa] text-[#54376f]",
  };
  const icons: Record<Tone, LucideIcon> = {
    neutral: Info,
    success: CheckCircle2,
    warning: AlertTriangle,
    danger: AlertTriangle,
    info: Info,
    plum: Info,
  };
  const Icon = icons[tone];
  return (
    <div className={cn("rounded-xl border px-4 py-3 text-sm", styles[tone])}>
      <div className="flex items-start gap-3">
        <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
        <div className="min-w-0 flex-1">
          {title ? <p className="font-semibold">{title}</p> : null}
          <div className={cn("leading-relaxed", title ? "mt-1 opacity-90" : null)}>{children}</div>
        </div>
        {action}
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------- forms */

export function Field({
  label,
  htmlFor,
  hint,
  error,
  required,
  children,
  className,
}: {
  label: string;
  htmlFor?: string;
  hint?: ReactNode;
  error?: string | null;
  required?: boolean;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={className}>
      <label className="label" htmlFor={htmlFor}>
        {label}
        {required ? <span className="ml-1 text-[#a33a2b]">*</span> : null}
      </label>
      {children}
      {error ? <p className="mt-1 text-xs font-medium text-[#8b3323]">{error}</p> : null}
      {!error && hint ? <p className="mt-1 text-xs text-muted">{hint}</p> : null}
    </div>
  );
}

export function TextInput({ className, ...rest }: ComponentProps<"input">) {
  return <input className={cn("input", className)} {...rest} />;
}

export function Select({ className, children, ...rest }: ComponentProps<"select">) {
  return (
    <select className={cn("input appearance-none pr-8", className)} {...rest}>
      {children}
    </select>
  );
}

export function Textarea({ className, ...rest }: ComponentProps<"textarea">) {
  return <textarea className={cn("input min-h-24 resize-y", className)} {...rest} />;
}

export function CheckboxRow({
  label,
  description,
  checked,
  onChange,
  disabled,
}: {
  label: ReactNode;
  description?: ReactNode;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <label className={cn("flex cursor-pointer items-start gap-3 rounded-lg px-1 py-1.5", disabled && "cursor-not-allowed opacity-60")}>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        className="mt-0.5 h-4 w-4 shrink-0 accent-[#235237]"
      />
      <span>
        <span className="block text-sm font-medium text-ink">{label}</span>
        {description ? <span className="mt-0.5 block text-xs leading-relaxed text-muted">{description}</span> : null}
      </span>
    </label>
  );
}

export function SearchInput({
  value,
  onChange,
  placeholder = "Search…",
  className,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
}) {
  return (
    <div className={cn("relative", className)}>
      <Search className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-muted" aria-hidden />
      <input
        className="input pl-9"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        type="search"
      />
    </div>
  );
}

/* ------------------------------------------------------------------ feedback */

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cn("h-5 w-5 animate-spin text-leaf-600", className)} aria-label="Loading" />;
}

export function PageLoader({ label = "Loading" }: { label?: string }) {
  return (
    <div className="flex min-h-48 flex-col items-center justify-center gap-3 text-muted">
      <Spinner />
      <p className="text-sm">{label}…</p>
    </div>
  );
}

export function EmptyState({
  icon: Icon = Info,
  title,
  description,
  action,
}: {
  icon?: LucideIcon;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-line bg-surface/60 px-6 py-12 text-center">
      <span className="mb-3 grid h-11 w-11 place-items-center rounded-full bg-leaf-50 text-leaf-600">
        <Icon className="h-5 w-5" aria-hidden />
      </span>
      <h3 className="text-base font-semibold text-ink">{title}</h3>
      {description ? <p className="mt-1 max-w-md text-sm leading-relaxed text-muted">{description}</p> : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}

export function ErrorBanner({
  error,
  onRetry,
  onDismiss,
}: {
  error: unknown;
  onRetry?: () => void;
  onDismiss?: () => void;
}) {
  if (!error) return null;
  const apiError = isApiError(error) ? error : null;
  const message = apiError?.message ?? (error instanceof Error ? error.message : "Something went wrong.");
  const code = apiError?.code;
  const guidance = errorGuidance(code);
  return (
    <div className="rounded-xl border border-[#e9c3b9] bg-[#fbebe6] p-4 text-[#722c1f]">
      <div className="flex items-start gap-3">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            {code ? <code className="rounded bg-white/60 px-1.5 py-0.5 font-mono text-[11px] font-semibold">{code}</code> : null}
            <p className="text-sm font-semibold">{message}</p>
          </div>
          {guidance ? <p className="mt-1.5 text-xs leading-relaxed opacity-85">{guidance}</p> : null}
          {apiError && (apiError.correlationId || apiError.traceId) ? (
            <p className="mt-2 font-mono text-[10px] opacity-70">
              {apiError.correlationId ? <span>correlation: {apiError.correlationId} </span> : null}
              {apiError.traceId ? <span>trace: {apiError.traceId}</span> : null}
            </p>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {onRetry ? (
            <button type="button" onClick={onRetry} className="btn btn-sm border border-current/20 bg-white/40 hover:bg-white/70">
              Retry
            </button>
          ) : null}
          {onDismiss ? (
            <button type="button" onClick={onDismiss} className="rounded p-1.5 hover:bg-white/40" aria-label="Dismiss error">
              <X className="h-4 w-4" />
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------- modal */

const modalSizes = {
  sm: "max-w-md",
  md: "max-w-xl",
  lg: "max-w-3xl",
  xl: "max-w-5xl",
} as const;

export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  size = "md",
  dismissible = true,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  size?: keyof typeof modalSizes;
  dismissible?: boolean;
}) {
  const titleId = useId();

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && dismissible) onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.body.style.overflow = previous;
    };
  }, [open, dismissible, onClose]);

  if (!open) return null;

  return createPortal(
    <div className="fixed inset-0 z-[60] flex items-end justify-center bg-leaf-900/45 p-0 backdrop-blur-[2px] sm:items-center sm:p-6">
      <button
        type="button"
        className="absolute inset-0 cursor-default"
        aria-label="Close dialog"
        onClick={dismissible ? onClose : undefined}
        tabIndex={-1}
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={cn(
          "relative flex max-h-[92vh] w-full flex-col overflow-hidden rounded-t-2xl border border-line bg-surface shadow-2xl sm:rounded-2xl",
          modalSizes[size],
        )}
      >
        <div className="flex items-start justify-between gap-4 border-b border-line px-5 py-4">
          <div>
            <h2 id={titleId} className="text-base font-semibold text-ink">
              {title}
            </h2>
            {description ? <p className="mt-1 text-xs leading-relaxed text-muted">{description}</p> : null}
          </div>
          {dismissible ? (
            <button type="button" onClick={onClose} className="btn btn-ghost btn-icon -mr-1 shrink-0" aria-label="Close">
              <X className="h-4 w-4" />
            </button>
          ) : null}
        </div>
        <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</div>
        {footer ? <div className="flex flex-wrap justify-end gap-2 border-t border-line bg-[#f7f6f0] px-5 py-3">{footer}</div> : null}
      </div>
    </div>,
    document.body,
  );
}

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmLabel = "Confirm",
  tone = "danger",
  loading,
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: ReactNode;
  description: ReactNode;
  confirmLabel?: string;
  tone?: "danger" | "primary";
  loading?: boolean;
}) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      size="sm"
      dismissible={!loading}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button variant={tone === "danger" ? "danger" : "primary"} onClick={onConfirm} loading={loading}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="text-sm leading-relaxed text-muted">{description}</div>
    </Modal>
  );
}

/* ------------------------------------------------------------------- tables */

export function TableWrap({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("table-wrap scrollbar-thin", className)}>{children}</div>;
}

export function TableEmpty({ colSpan, children }: { colSpan: number; children: ReactNode }) {
  return (
    <tr>
      <td colSpan={colSpan} className="px-4 py-10 text-center text-sm text-muted">
        {children}
      </td>
    </tr>
  );
}

export function Pagination({
  page,
  totalPages,
  onChange,
}: {
  page: number;
  totalPages: number;
  onChange: (page: number) => void;
}) {
  if (totalPages <= 1) return null;
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-3 py-2.5 text-xs text-muted sm:px-4">
      <span>
        Page <strong className="text-ink">{page}</strong> of {totalPages}
      </span>
      <div className="flex items-center gap-1">
        <Button variant="secondary" size="sm" onClick={() => onChange(page - 1)} disabled={page <= 1}>
          <ChevronLeft className="h-3.5 w-3.5" /> Prev
        </Button>
        <Button variant="secondary" size="sm" onClick={() => onChange(page + 1)} disabled={page >= totalPages}>
          Next <ChevronRight className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  );
}

/* --------------------------------------------------------------- misc bits */

export function ProgressBar({
  value,
  tone = "leaf",
  label,
  detail,
}: {
  value: number;
  tone?: "leaf" | "amber" | "plum" | "danger";
  label?: ReactNode;
  detail?: ReactNode;
}) {
  const bar: Record<string, string> = {
    leaf: "bg-leaf-600",
    amber: "bg-[#b38327]",
    plum: "bg-plum",
    danger: "bg-[#a33a2b]",
  };
  return (
    <div>
      {label || detail ? (
        <div className="mb-1.5 flex flex-wrap items-baseline justify-between gap-2 text-xs">
          <span className="font-medium text-ink">{label}</span>
          <span className="text-muted">{detail}</span>
        </div>
      ) : null}
      <div className="h-2.5 w-full overflow-hidden rounded-full bg-[#e8e7dd]" role="progressbar" aria-valuenow={value} aria-valuemin={0} aria-valuemax={100}>
        <div className={cn("h-full rounded-full transition-[width] duration-500", bar[tone])} style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
      </div>
    </div>
  );
}

export function CopyButton({ value, label = "Copy", className }: { value: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
      } else {
        const area = document.createElement("textarea");
        area.value = value;
        area.style.position = "fixed";
        area.style.opacity = "0";
        document.body.appendChild(area);
        area.select();
        document.execCommand("copy");
        area.remove();
      }
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setCopied(false);
    }
  };

  return (
    <button type="button" onClick={copy} className={cn("btn btn-secondary btn-sm", className)}>
      {copied ? <Check className="h-3.5 w-3.5 text-leaf-600" /> : <Copy className="h-3.5 w-3.5" />}
      {copied ? "Copied" : label}
    </button>
  );
}

export function CodeBlock({
  code,
  label,
  maxHeight = "24rem",
}: {
  code: string;
  label?: string;
  maxHeight?: string;
}) {
  return (
    <div className="sqlbox">
      <div className="flex items-center justify-between gap-2 border-b border-line bg-[#f7f6f0] px-3 py-2">
        <span className="font-mono text-[11px] font-medium tracking-wide text-muted uppercase">{label ?? "SQL"}</span>
        <CopyButton value={code} />
      </div>
      <pre
        className="scrollbar-thin overflow-auto p-3 font-mono text-[12.5px] leading-relaxed whitespace-pre text-ink"
        style={{ maxHeight }}
      >
        <code>{code || "-- empty --"}</code>
      </pre>
    </div>
  );
}

export function KeyValue({ items }: { items: Array<{ label: string; value: ReactNode }> }) {
  return (
    <dl className="grid min-w-0 gap-x-6 gap-y-3 sm:grid-cols-2">
      {items.map((item) => (
        <div key={item.label} className="min-w-0">
          <dt className="text-[11px] font-semibold tracking-[0.12em] text-muted uppercase">{item.label}</dt>
          <dd className="mt-0.5 break-words text-sm text-ink">{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function TabNav({
  items,
}: {
  items: Array<{ to: string; label: string; end?: boolean; icon?: LucideIcon; badge?: ReactNode }>;
}) {
  return (
    <nav className="tabs scrollbar-thin" aria-label="Section">
      {items.map(({ to, label, end, icon: Icon, badge }) => (
        <NavLink key={to} to={to} end={end} className={({ isActive }) => cn("tab inline-flex items-center gap-1.5", isActive && "tab-active")}>
          {Icon ? <Icon className="h-3.5 w-3.5" aria-hidden /> : null}
          {label}
          {badge}
        </NavLink>
      ))}
    </nav>
  );
}

export function PageHeader({
  title,
  description,
  actions,
  eyebrow,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  eyebrow?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        {eyebrow ? <div className="mb-1.5 text-[11px] font-semibold tracking-[0.16em] text-leaf-600 uppercase">{eyebrow}</div> : null}
        <h1 className="text-balance text-xl font-semibold tracking-tight text-ink sm:text-2xl">{title}</h1>
        {description ? <p className="mt-1 max-w-3xl text-sm leading-relaxed text-muted">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}

export function SectionHeader({
  title,
  description,
  actions,
  optional,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  optional?: boolean;
}) {
  return (
    <div className="mb-3 flex flex-wrap items-end justify-between gap-2">
      <div>
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold tracking-wide text-ink">{title}</h2>
          {optional ? <OptionalTag /> : null}
        </div>
        {description ? <p className="mt-0.5 text-xs text-muted">{description}</p> : null}
      </div>
      {actions}
    </div>
  );
}
