import { AlertTriangle, CheckCircle2, Info, X } from "lucide-react";
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

type ToastTone = "success" | "error" | "info";

interface ToastItem {
  id: number;
  tone: ToastTone;
  title: string;
  message?: string;
}

interface ToastContextValue {
  push: (tone: ToastTone, title: string, message?: string) => void;
  success: (title: string, message?: string) => void;
  error: (title: string, message?: string) => void;
  info: (title: string, message?: string) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

const toneStyles: Record<ToastTone, { box: string; bar: string; icon: string }> = {
  success: {
    box: "border-leaf-600 bg-leaf-100 text-ink",
    bar: "bg-leaf-600",
    icon: "text-leaf-700",
  },
  error: {
    box: "border-crimson bg-crimson-wash text-ink",
    bar: "bg-crimson",
    icon: "text-crimson",
  },
  info: {
    box: "border-cobalt bg-cobalt-wash text-ink",
    bar: "bg-cobalt",
    icon: "text-cobalt",
  },
};

const toneIcons = {
  success: CheckCircle2,
  error: AlertTriangle,
  info: Info,
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const counter = useRef(0);

  const remove = useCallback((id: number) => {
    setItems((current) => current.filter((item) => item.id !== id));
  }, []);

  const push = useCallback(
    (tone: ToastTone, title: string, message?: string) => {
      counter.current += 1;
      const id = counter.current;
      setItems((current) => [...current.slice(-3), { id, tone, title, message }]);
      if (tone !== "error") {
        window.setTimeout(() => remove(id), 6000);
      }
    },
    [remove],
  );

  const value = useMemo<ToastContextValue>(
    () => ({
      push,
      success: (title, message) => push("success", title, message),
      error: (title, message) => push("error", title, message),
      info: (title, message) => push("info", title, message),
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="pointer-events-none fixed inset-x-3 bottom-3 z-[70] flex flex-col items-end gap-2 sm:inset-x-auto sm:right-5 sm:bottom-5 sm:w-[26rem]">
        {items.map((item) => {
          const Icon = toneIcons[item.tone];
          const style = toneStyles[item.tone];
          return (
            <div
              key={item.id}
              role={item.tone === "error" ? "alert" : "status"}
              className={`group pointer-events-auto relative flex w-full items-start gap-3 overflow-hidden border-2 border-l-8 pt-4 pr-2 pb-3 pl-4 shadow-[2px_2px_0_rgb(15_31_20/0.18)] ${style.box}`}
            >
              {item.tone !== "error" ? (
                <div
                  aria-hidden="true"
                  className={`absolute top-0 right-0 left-0 h-1 origin-left animate-[flash-timer_6s_linear_forwards] group-hover:[animation-play-state:paused] group-focus-within:[animation-play-state:paused] ${style.bar}`}
                />
              ) : null}
              <Icon className={`mt-0.5 h-5 w-5 shrink-0 ${style.icon}`} aria-hidden />
              <div className="min-w-0 flex-1 py-0.5">
                <p className="text-sm font-semibold">{item.title}</p>
                {item.message ? <p className="mt-0.5 text-sm leading-relaxed text-ink-soft">{item.message}</p> : null}
              </div>
              <button
                type="button"
                onClick={() => remove(item.id)}
                className="grid h-9 w-9 shrink-0 cursor-pointer place-items-center border-2 border-transparent bg-transparent text-ink hover:border-ink-soft hover:bg-paper pointer-coarse:h-11 pointer-coarse:w-11"
                aria-label="Dismiss notification"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const context = useContext(ToastContext);
  if (!context) {
    throw new Error("useToast must be used inside <ToastProvider>");
  }
  return context;
}
