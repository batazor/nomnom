import { AnimatePresence, m } from "motion/react";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

export type ToastContent = Readonly<{
  title: string;
  description?: string;
}>;

type Toast = ToastContent & Readonly<{ id: number }>;

type ShowToast = (toast: ToastContent) => void;

const ToastContext = createContext<ShowToast | null>(null);

// How long a toast stays while nobody looks at the stack.
const lifetime = 4000;
// Toasts beyond this many hide behind the stack.
const visible = 3;
// A horizontal throw this far dismisses a toast.
const throwDistance = 80;

// Short confirmations that stack at the bottom of the screen: the newest in
// front, older ones peek out above it, narrower and fainter. Hover or focus fans the stack
// out and holds the timers. Screen readers hear each message once.
export function ToastProvider({
  closeLabel,
  regionLabel,
  children,
}: {
  closeLabel: string;
  regionLabel: string;
  children: ReactNode;
}) {
  const [toasts, setToasts] = useState<readonly Toast[]>([]);
  const [expanded, setExpanded] = useState(false);
  const nextId = useRef(0);

  const show = useCallback<ShowToast>((content) => {
    nextId.current += 1;
    const id = nextId.current;
    setToasts((current) => [{ ...content, id }, ...current]);
  }, []);
  const dismiss = useCallback((id: number) => {
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }, []);

  const value = useMemo(() => show, [show]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <section
        aria-label={regionLabel}
        className="nomnom-toasts"
        data-expanded={expanded || undefined}
        onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget)) {
            setExpanded(false);
          }
        }}
        onFocus={() => setExpanded(true)}
        onMouseEnter={() => setExpanded(true)}
        onMouseLeave={() => setExpanded(false)}
      >
        <ol aria-live="polite" className="nomnom-toast-list">
          <AnimatePresence initial={false}>
            {toasts.map((toast, index) => (
              <ToastItem
                closeLabel={closeLabel}
                depth={index}
                expanded={expanded}
                key={toast.id}
                onDismiss={() => dismiss(toast.id)}
                toast={toast}
              />
            ))}
          </AnimatePresence>
        </ol>
      </section>
    </ToastContext.Provider>
  );
}

function ToastItem({
  toast,
  depth,
  expanded,
  closeLabel,
  onDismiss,
}: {
  toast: Toast;
  depth: number;
  expanded: boolean;
  closeLabel: string;
  onDismiss: () => void;
}) {
  // The timer runs only while the toast is shown and the stack is folded.
  useEffect(() => {
    if (expanded) return;
    const timer = window.setTimeout(onDismiss, lifetime);
    return () => window.clearTimeout(timer);
  }, [expanded, onDismiss]);

  const hidden = !expanded && depth >= visible;
  const placement = expanded
    ? { y: `calc(${-depth * 100}% - ${depth * 0.5}rem)`, scale: 1, opacity: 1 }
    : {
        y: `${-depth * 0.625}rem`,
        scale: 1 - depth * 0.05,
        opacity: hidden ? 0 : 1 - depth * 0.25,
      };

  return (
    <m.li
      animate={{ x: 0, ...placement }}
      className="nomnom-toast"
      drag="x"
      dragSnapToOrigin
      exit={{ opacity: 0, scale: 0.9, transition: { duration: 0.15 } }}
      initial={{ opacity: 0, y: "100%", scale: 0.95 }}
      onDragEnd={(_, info) => {
        if (Math.abs(info.offset.x) > throwDistance) onDismiss();
      }}
      data-behind={(!expanded && depth > 0) || undefined}
      style={{ zIndex: 100 - depth }}
      transition={{ type: "spring", bounce: 0.2, duration: 0.4 }}
      {...(hidden ? { "aria-hidden": true } : {})}
    >
      <span aria-hidden="true" className="nomnom-toast-icon">
        <svg
          aria-hidden="true"
          fill="none"
          focusable="false"
          viewBox="0 0 16 16"
        >
          <path
            d="m3.5 8.5 3 3 6-7"
            stroke="currentColor"
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth="1.75"
          />
        </svg>
      </span>
      <div className="nomnom-toast-text">
        <p className="nomnom-toast-title">{toast.title}</p>
        {toast.description ? (
          <p className="nomnom-toast-description">{toast.description}</p>
        ) : null}
      </div>
      <button
        aria-label={closeLabel}
        className="nomnom-toast-close"
        onClick={onDismiss}
        tabIndex={hidden ? -1 : undefined}
        type="button"
      >
        <svg
          aria-hidden="true"
          fill="none"
          focusable="false"
          viewBox="0 0 16 16"
        >
          <path
            d="m4 4 8 8M12 4l-8 8"
            stroke="currentColor"
            strokeLinecap="round"
            strokeWidth="1.5"
          />
        </svg>
      </button>
    </m.li>
  );
}

export function useToast(): ShowToast {
  const show = useContext(ToastContext);
  if (!show) throw new Error("useToast needs a ToastProvider");
  return show;
}
