import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from "react";

/**
 * Base UI components.
 *
 * Written directly rather than pulled from a library: the set is small, and
 * owning the markup keeps the design tokens and the accessibility details
 * under our control.
 */

// ---------------------------------------------------------------------------
// Button
// ---------------------------------------------------------------------------

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  loading?: boolean;
  fullWidth?: boolean;
}

const buttonVariants: Record<ButtonVariant, string> = {
  primary: "bg-[var(--color-violet-600)] text-white hover:bg-[var(--color-violet-500)]",
  secondary:
    "bg-[var(--surface-sunken)] text-[var(--text-primary)] hover:bg-[var(--surface-raised)] border border-[var(--border-default)]",
  ghost: "bg-transparent text-[var(--text-secondary)] hover:bg-[var(--surface-sunken)]",
  danger: "bg-[var(--color-danger)] text-white hover:opacity-90",
};

export function Button({
  variant = "primary",
  loading = false,
  fullWidth = false,
  disabled,
  children,
  className = "",
  ...props
}: ButtonProps) {
  return (
    <button
      // A loading button must not be clickable twice: in banking that could
      // mean two transfers.
      disabled={disabled || loading}
      // Announces the busy state to screen readers, which cannot see a spinner.
      aria-busy={loading}
      className={`inline-flex items-center justify-center gap-2 rounded-md px-4 py-2.5
        text-sm font-medium transition-colors
        disabled:opacity-50 disabled:cursor-not-allowed
        ${buttonVariants[variant]} ${fullWidth ? "w-full" : ""} ${className}`}
      {...props}
    >
      {loading && <Spinner />}
      {children}
    </button>
  );
}

function Spinner() {
  return (
    <span
      aria-hidden="true"
      className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent"
    />
  );
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
  error?: string;
  hint?: string;
  /** Renders the value in tabular figures, for amounts and account numbers. */
  numeric?: boolean;
}

export function Input({
  label,
  error,
  hint,
  numeric = false,
  id,
  className = "",
  ...props
}: InputProps) {
  const inputId = id ?? `input-${label.toLowerCase().replace(/\s+/g, "-")}`;
  const errorId = `${inputId}-error`;
  const hintId = `${inputId}-hint`;

  return (
    <div className="w-full">
      <label
        htmlFor={inputId}
        className="mb-1.5 block text-sm font-medium"
        style={{ color: "var(--text-secondary)" }}
      >
        {label}
      </label>

      <input
        id={inputId}
        // Tells assistive technology the field is invalid, not just that it
        // looks different.
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errorId : hint ? hintId : undefined}
        className={`w-full rounded-md border px-3 py-2.5 text-sm outline-none
          transition-colors
          ${numeric ? "amount" : ""} ${className}`}
        style={{
          backgroundColor: "var(--surface-raised)",
          borderColor: error ? "var(--color-danger)" : "var(--border-default)",
          color: "var(--text-primary)",
        }}
        {...props}
      />

      {error && (
        // role="alert" makes the message announced as soon as it appears.
        <p id={errorId} role="alert" className="mt-1.5 text-sm" style={{ color: "var(--color-danger)" }}>
          {error}
        </p>
      )}

      {!error && hint && (
        <p id={hintId} className="mt-1.5 text-xs" style={{ color: "var(--text-muted)" }}>
          {hint}
        </p>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Card
// ---------------------------------------------------------------------------

export function Card({
  children,
  className = "",
  padding = true,
}: {
  children: ReactNode;
  className?: string;
  padding?: boolean;
}) {
  return (
    <div
      className={`rounded-lg border ${padding ? "p-5" : ""} ${className}`}
      style={{
        backgroundColor: "var(--surface-raised)",
        borderColor: "var(--border-subtle)",
      }}
    >
      {children}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Amount
// ---------------------------------------------------------------------------

/**
 * Renders a monetary amount in tabular figures.
 *
 * Tabular numerals keep every digit the same width, so amounts line up on the
 * decimal in a list and a balance does not jitter as it updates. In banking
 * that steadiness is what makes the number feel trustworthy.
 */
export function Amount({
  children,
  size = "base",
  tone = "default",
  className = "",
}: {
  children: ReactNode;
  size?: "sm" | "base" | "lg" | "xl";
  tone?: "default" | "positive" | "negative" | "muted";
  className?: string;
}) {
  const sizes = {
    sm: "text-sm",
    base: "text-base",
    lg: "text-2xl",
    xl: "text-4xl",
  };

  const tones = {
    default: "var(--text-primary)",
    positive: "var(--color-success)",
    negative: "var(--text-primary)",
    muted: "var(--text-muted)",
  };

  return (
    <span className={`amount ${sizes[size]} ${className}`} style={{ color: tones[tone] }}>
      {children}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Status badge
// ---------------------------------------------------------------------------

type StatusTone = "success" | "warning" | "danger" | "neutral";

/**
 * Status indicator carrying a symbol alongside the colour.
 *
 * Colour alone is not an accessible signal: roughly one in twelve men cannot
 * distinguish red from green. The symbol makes the state readable regardless.
 */
export function Status({
  tone,
  children,
}: {
  tone: StatusTone;
  children: ReactNode;
}) {
  const config: Record<StatusTone, { color: string; symbol: string }> = {
    success: { color: "var(--color-success)", symbol: "●" },
    warning: { color: "var(--color-warning)", symbol: "◆" },
    danger: { color: "var(--color-danger)", symbol: "▲" },
    neutral: { color: "var(--text-muted)", symbol: "○" },
  };

  const { color, symbol } = config[tone];

  return (
    <span className="inline-flex items-center gap-1.5 text-sm font-medium" style={{ color }}>
      <span aria-hidden="true">{symbol}</span>
      {children}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Empty and error states
// ---------------------------------------------------------------------------

/**
 * Informative empty state.
 *
 * The test asks for these explicitly, and they matter: a blank screen makes
 * people think the app is broken rather than that they have no data yet.
 */
export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-12 text-center">
      <p className="mb-1 text-base font-medium" style={{ color: "var(--text-primary)" }}>
        {title}
      </p>
      {description && (
        <p className="mb-4 max-w-sm text-sm" style={{ color: "var(--text-muted)" }}>
          {description}
        </p>
      )}
      {action}
    </div>
  );
}

export function ErrorState({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}) {
  return (
    <div
      role="alert"
      className="flex flex-col items-center justify-center px-6 py-10 text-center"
    >
      <Status tone="danger">Algo salió mal</Status>
      <p className="mt-2 mb-4 max-w-sm text-sm" style={{ color: "var(--text-secondary)" }}>
        {message}
      </p>
      {onRetry && (
        <Button variant="secondary" onClick={onRetry}>
          Reintentar
        </Button>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Skeleton
// ---------------------------------------------------------------------------

/**
 * Loading placeholder.
 *
 * Preferred over a spinner because it hints at the shape of what is coming,
 * which makes the wait feel shorter and stops the layout from jumping.
 */
export function Skeleton({ className = "" }: { className?: string }) {
  return (
    <div
      aria-hidden="true"
      className={`animate-pulse rounded ${className}`}
      style={{ backgroundColor: "var(--surface-sunken)" }}
    />
  );
}
