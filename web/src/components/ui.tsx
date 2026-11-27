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
  Fragment,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
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

interface StatusBadgeProps {
  status: JobStatus | ProposalStatus | LockState | string | null | undefined;
  kind?: "job" | "proposal" | "lock" | "auto";
  className?: string;
}

export function StatusBadge({ status, kind = "job", className }: StatusBadgeProps) {
  let tone: Tone = "neutral";
  let label = "—";
  if (status) {
    if (kind === "lock") {
      const meta = LOCK_STATE_META[status as LockState];
      if (meta) {
        tone = meta.tone;
        label = meta.label;
      }
    } else if (
      kind === "proposal" ||
      (kind === "auto" &&
        !JOB_STATUS_META[status as JobStatus] &&
        PROPOSAL_STATUS_META[status as ProposalStatus])
    ) {
      const meta = PROPOSAL_STATUS_META[status as ProposalStatus];
      if (meta) {
        tone = meta.tone;
        label = meta.label;
      }
    } else {
      const meta = JOB_STATUS_META[status as JobStatus];
      if (meta) {
        tone = meta.tone;
        label = meta.label;
      }
    }
  }
  return <span className={cn("badge", toneBadge[tone], className)}>{label}</span>;
}

export function ActionBadge({ action }: { action: ActionType | string | null | undefined }) {
  const label = action ? (ACTION_LABELS[action as ActionType] ?? action) : "—";
  return <span className="badge badge-neutral font-mono">{label}</span>;
}

export function LevelBadge({ level }: { level: "view" | "edit" | undefined | null }) {
  if (!level) return <span className="badge badge-muted">No access</span>;
  return (
    <span className={cn("badge", level === "edit" ? "badge-success" : "badge-info")}>
      {level === "edit" ? "View + Edit" : "View Only"}
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
    <div className="card p-4">
      <div className="flex items-start justify-between gap-3">
        <p className="stat-label">{label}</p>
        {Icon ? <Icon className="h-4 w-4 text-ink-soft" aria-hidden /> : null}
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
    neutral: "alert-neutral",
    success: "alert-success",
    warning: "alert-warning",
    danger: "alert-danger",
    info: "alert-info",
    plum: "alert-warning",
  };
  const iconColors: Record<Tone, string> = {
    neutral: "text-ink-soft",
    success: "text-leaf-700",
    warning: "text-amber",
    danger: "text-crimson",
    info: "text-cobalt",
    plum: "text-amber",
  };
  const icons: Record<Tone, LucideIcon> = {
    neutral: Info,
    success: CheckCircle2,
    warning: AlertTriangle,
    danger: AlertTriangle,
    info: Info,
    plum: AlertTriangle,
  };
  const Icon = icons[tone];
  return (
    <div className={cn("alert text-sm max-sm:flex-wrap", styles[tone])} role={tone === "danger" ? "alert" : "status"}>
      <Icon className={cn("mt-0.5 h-5 w-5 shrink-0", iconColors[tone])} aria-hidden />
      <div className="min-w-0 flex-1">
        {title ? <p className="text-base font-semibold text-ink">{title}</p> : null}
        <div className={cn("leading-relaxed text-ink", title ? "mt-1" : null)}>{children}</div>
      </div>
      {action ? <div className="shrink-0 max-sm:basis-full max-sm:pl-8">{action}</div> : null}
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
    <div className={cn("grid content-start gap-1", className)}>
      <label className="label mb-0" htmlFor={htmlFor}>
        {label}
        {!required ? <span className="ml-1.5 text-sm font-normal text-muted">(optional)</span> : null}
      </label>
      {!error && hint ? <p className="hint m-0">{hint}</p> : null}
      {children}
      {error ? <p className="m-0 text-sm font-semibold text-crimson">{error}</p> : null}
    </div>
  );
}

export function TextInput({ className, ...rest }: ComponentProps<"input">) {
  return <input className={cn("input", className)} {...rest} />;
}

export function Select({ className, children, ...rest }: ComponentProps<"select">) {
  return (
    <div className="relative">
      <select className={cn("input appearance-none pr-10", className)} {...rest}>
        {children}
      </select>
      <span
        aria-hidden="true"
        className="pointer-events-none absolute top-1/2 right-3.5 h-2 w-2 -translate-y-2/3 rotate-45 border-r-2 border-b-2 border-ink"
      />
    </div>
  );
}

export function Textarea({ className, ...rest }: ComponentProps<"textarea">) {
  return <textarea className={cn("input min-h-28 resize-y", className)} {...rest} />;
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
    <label
      className={cn(
        "flex cursor-pointer items-start gap-2.5 py-1",
        disabled && "cursor-not-allowed opacity-60",
      )}
    >
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        className="mt-0.5 h-5 w-5 shrink-0 accent-leaf-700"
      />
      <span>
        <span className="block text-sm font-semibold text-ink">{label}</span>
        {description ? (
          <span className="mt-0.5 block text-xs leading-relaxed text-muted">{description}</span>
        ) : null}
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
      <Search
        className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-muted"
        aria-hidden
      />
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
  return <Loader2 className={cn("h-5 w-5 animate-spin text-leaf-700", className)} aria-label="Loading" />;
}

export function PageLoader({ label = "Loading" }: { label?: string }) {
  return (
    <div className="card flex min-h-48 flex-col items-center justify-center gap-3 p-6 text-ink-soft">
      <Spinner />
      <p className="text-sm font-semibold">{label}…</p>
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
    <div className="border-2 border-dashed border-ink-soft bg-paper px-6 py-8 text-center">
      <div className="mx-auto mb-3 inline-grid h-10 w-10 place-items-center border-2 border-ink bg-leaf-50 text-leaf-800">
        <Icon className="h-5 w-5" aria-hidden />
      </div>
      <h2 className="text-lg font-semibold text-ink">{title}</h2>
      {description ? (
        <p className="mx-auto mt-2 max-w-[48ch] text-sm leading-relaxed text-ink-soft">{description}</p>
      ) : null}
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
    <div className="alert alert-danger max-sm:flex-wrap" role="alert">
      <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-crimson" aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          {code ? (
            <code className="border border-crimson bg-paper px-1.5 py-0.5 font-mono text-xs font-semibold text-crimson">
              {code}
            </code>
          ) : null}
          <p className="min-w-0 text-base font-semibold break-words text-ink">{message}</p>
        </div>
        {guidance ? <p className="mt-1 text-sm leading-relaxed text-ink">{guidance}</p> : null}
      </div>
      <div className={cn("flex shrink-0 items-center gap-2", (onRetry || onDismiss) && "max-sm:basis-full max-sm:pl-8")}>
        {onRetry ? (
          <button type="button" onClick={onRetry} className="btn btn-sm">
            Retry
          </button>
        ) : null}
        {onDismiss ? (
          <button
            type="button"
            onClick={onDismiss}
            className="grid h-9 w-9 cursor-pointer place-items-center border-2 border-transparent bg-transparent text-ink hover:border-ink-soft hover:bg-paper pointer-coarse:h-11 pointer-coarse:w-11"
            aria-label="Dismiss error"
          >
            <X className="h-4 w-4" />
          </button>
        ) : null}
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
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-ink/50 p-3 sm:p-4">
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
          "relative flex max-h-[92dvh] w-full flex-col overflow-hidden [@media(max-height:40rem)]:overflow-y-auto [@media(max-height:40rem)]:[&>*]:shrink-0 border-2 border-ink bg-paper shadow-[6px_6px_0_rgb(15_31_20/0.28)]",
          modalSizes[size],
        )}
      >
        <div className="flex items-start justify-between gap-4 border-b-2 border-ink bg-leaf-50 px-4 py-3 sm:px-5 sm:py-3.5">
          <div className="min-w-0">
            <h2 id={titleId} className="text-lg font-semibold text-ink">
              {title}
            </h2>
            {description ? (
              <p className="mt-1 text-xs leading-relaxed text-ink-soft">{description}</p>
            ) : null}
          </div>
          {dismissible ? (
            <button
              type="button"
              onClick={onClose}
              className="grid h-9 w-9 shrink-0 cursor-pointer place-items-center border-2 border-transparent bg-transparent text-ink hover:border-ink hover:bg-paper pointer-coarse:h-11 pointer-coarse:w-11"
              aria-label="Close"
            >
              <X className="h-4 w-4" />
            </button>
          ) : null}
        </div>
        <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-4 sm:p-5 [@media(max-height:40rem)]:flex-none [@media(max-height:40rem)]:overflow-visible">{children}</div>
        {footer ? (
          <div className="flex flex-wrap justify-end gap-2 border-t-2 border-ink bg-canvas px-4 py-3 max-sm:[&>*]:flex-1 max-sm:[&>*]:basis-32 sm:px-5">
            {footer}
          </div>
        ) : null}
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
          <Button
            variant={tone === "danger" ? "danger" : "primary"}
            onClick={onConfirm}
            loading={loading}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="text-sm leading-relaxed text-ink">{description}</div>
    </Modal>
  );
}

/* ------------------------------------------------------------------- tables */

/** Below this width a record table is shown as cards even if its columns could be squeezed in. */
const STACK_BELOW_PX = 560;

export function TableWrap({ children, className }: { children: ReactNode; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);

  // `.table-stack` tables switch to cards when their content stops fitting, so the
  // breakpoint follows each table's columns and container, not the viewport.
  const fit = () => {
    const wrap = ref.current;
    const table = wrap?.querySelector<HTMLTableElement>("table.table-stack");
    if (!wrap || !table) return;
    // Hold the height while measuring so the page cannot scroll-jump.
    wrap.style.minHeight = `${wrap.offsetHeight}px`;
    wrap.removeAttribute("data-stacked");
    if (wrap.clientWidth < STACK_BELOW_PX || table.scrollWidth > wrap.clientWidth + 1) {
      wrap.setAttribute("data-stacked", "");
    }
    wrap.style.minHeight = "";
  };

  useLayoutEffect(fit);

  useEffect(() => {
    const wrap = ref.current;
    if (!wrap) return;
    let width = wrap.clientWidth;
    const observer = new ResizeObserver(() => {
      if (wrap.clientWidth === width) return;
      width = wrap.clientWidth;
      fit();
    });
    observer.observe(wrap);
    void document.fonts?.ready.then(fit);
    return () => observer.disconnect();
  }, []);

  return (
    <div ref={ref} className={cn("table-wrap scrollbar-thin", className)} tabIndex={0} role="region">
      {children}
    </div>
  );
}

export function TableEmpty({ colSpan, children }: { colSpan: number; children: ReactNode }) {
  return (
    <tr>
      <td colSpan={colSpan} className="px-4 py-8 text-center text-sm text-muted">
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

  const pages: Array<number | "gap"> = [];
  if (totalPages <= 7) {
    for (let i = 1; i <= totalPages; i++) pages.push(i);
  } else {
    let gap = false;
    for (let i = 1; i <= totalPages; i++) {
      if (i === 1 || i === totalPages || (i >= page - 1 && i <= page + 1)) {
        pages.push(i);
        gap = false;
      } else if (!gap) {
        pages.push("gap");
        gap = true;
      }
    }
  }

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-t-2 border-ink bg-canvas px-3 py-2.5 text-sm text-muted sm:px-4">
      <span>
        Page <strong className="text-ink">{page}</strong> of{" "}
        <strong className="text-ink">{totalPages}</strong>
      </span>
      <nav className="inline-flex flex-wrap items-center gap-1.5 max-sm:w-full max-sm:justify-between" aria-label="Pagination">
        <Button
          variant="secondary"
          size="sm"
          onClick={() => onChange(page - 1)}
          disabled={page <= 1}
        >
          <ChevronLeft className="h-4 w-4" /> Previous
        </Button>
        <ul className="m-0 flex list-none flex-wrap gap-1 p-0 max-sm:order-last max-sm:w-full max-sm:justify-center">
          {pages.map((p, index) =>
            p === "gap" ? (
              <li
                key={`gap-${index}`}
                className="grid h-9 min-w-9 place-items-center px-2 text-sm font-semibold text-muted"
                aria-hidden="true"
              >
                …
              </li>
            ) : (
              <li key={p}>
                <button
                  type="button"
                  onClick={() => onChange(p)}
                  aria-current={p === page ? "page" : undefined}
                  className={cn(
                    "grid h-9 min-w-9 cursor-pointer place-items-center border-2 border-ink px-2 text-sm font-semibold pointer-coarse:h-11 pointer-coarse:min-w-11",
                    p === page
                      ? "border-leaf-700 bg-leaf-700 text-paper"
                      : "bg-paper text-ink hover:bg-leaf-50",
                  )}
                >
                  {p}
                </button>
              </li>
            ),
          )}
        </ul>
        <Button
          variant="secondary"
          size="sm"
          onClick={() => onChange(page + 1)}
          disabled={page >= totalPages}
        >
          Next <ChevronRight className="h-4 w-4" />
        </Button>
      </nav>
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
    leaf: "bg-leaf-700",
    amber: "bg-amber",
    plum: "bg-amber",
    danger: "bg-crimson",
  };
  return (
    <div>
      {label || detail ? (
        <div className="mb-1.5 flex flex-wrap items-baseline justify-between gap-2 text-xs">
          <span className="font-semibold text-ink">{label}</span>
          <span className="font-mono text-ink-soft">{detail}</span>
        </div>
      ) : null}
      <div
        className="h-4 w-full overflow-hidden border-2 border-ink bg-canvas"
        role="progressbar"
        aria-valuenow={value}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div
          className={cn("h-full transition-[width] duration-300", bar[tone])}
          style={{ width: `${Math.max(0, Math.min(100, value))}%` }}
        />
      </div>
    </div>
  );
}

export function CopyButton({
  value,
  label = "Copy",
  className,
}: {
  value: string;
  label?: string;
  className?: string;
}) {
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
      {copied ? <Check className="h-3.5 w-3.5 text-leaf-700" /> : <Copy className="h-3.5 w-3.5" />}
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
  const text = code || "-- empty --";
  const lines = text.split("\n");
  return (
    <div className="sqlbox">
      <div className="flex items-center justify-between gap-2 border-b-2 border-ink bg-leaf-50 px-3 py-2">
        <span className="font-sans text-xs font-semibold tracking-wider text-ink-soft uppercase">
          {label ?? "SQL"}
        </span>
        <CopyButton value={code} />
      </div>
      <div className="scrollbar-thin overflow-auto" style={{ maxHeight }}>
        <pre className="m-0 grid min-w-full grid-cols-[max-content_minmax(0,1fr)] py-2 font-mono text-sm leading-[1.6] text-ink sm:w-max sm:grid-cols-[max-content_max-content]">
          {lines.map((line, idx) => (
            <Fragment key={idx}>
              <span
                aria-hidden="true"
                className="sticky left-0 min-w-[3.5ch] border-r-2 border-ink bg-canvas px-2 text-right text-muted select-none"
              >
                {idx + 1}
              </span>
              <code className="px-3 whitespace-pre-wrap [overflow-wrap:anywhere] sm:whitespace-pre">
                {line || " "}
              </code>
            </Fragment>
          ))}
        </pre>
      </div>
    </div>
  );
}

export function KeyValue({ items }: { items: Array<{ label: string; value: ReactNode }> }) {
  return (
    <dl className="grid min-w-0 grid-cols-2 gap-x-4 gap-y-3 sm:gap-x-6">
      {items.map((item) => (
        <div key={item.label} className="min-w-0">
          <dt className="text-xs font-semibold tracking-[0.04em] text-ink-soft uppercase">
            {item.label}
          </dt>
          <dd className="mt-1 break-words text-sm text-ink">{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function TabNav({
  items,
}: {
  items: Array<{ to: string; label: ReactNode; end?: boolean; icon?: LucideIcon; badge?: ReactNode }>;
}) {
  return (
    <nav className="tabs tabs-primary scrollbar-thin" aria-label="Database sections">
      {items.map(({ to, label, end, icon: Icon, badge }) => (
        <NavLink
          key={to}
          to={to}
          end={end}
          className={({ isActive }) => cn("tab", isActive && "tab-active")}
        >
          {Icon ? <Icon className="h-4 w-4" aria-hidden /> : null}
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
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  eyebrow?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0 flex-1 basis-80">
        <h1 className="m-0 text-balance break-words text-[clamp(1.625rem,1.3rem+1.4vw,2.25rem)] leading-[1.25] font-semibold text-ink">
          {title}
        </h1>
        {description ? (
          <p className="mt-2 mb-0 max-w-3xl text-sm leading-relaxed text-ink-soft">{description}</p>
        ) : null}
      </div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-center gap-2 max-sm:w-full max-sm:[&>*]:flex-1">{actions}</div>
      ) : null}
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
          <h2 className="m-0 text-lg leading-[1.25] font-semibold text-ink">{title}</h2>
          {optional ? <OptionalTag /> : null}
        </div>
        {description ? <p className="mt-1 mb-0 text-sm text-muted">{description}</p> : null}
      </div>
      {actions}
    </div>
  );
}
