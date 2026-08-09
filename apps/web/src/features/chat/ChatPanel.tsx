import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";

import { Button, EmptyState, Skeleton } from "@/components/ui";
import { ApiError, chat } from "@/lib/api";
import type { ChatMessage } from "@/types/api";
import { PendingOperationCard } from "./PendingOperationCard";

/**
 * How many messages load at a time.
 *
 * Small enough that the first paint is immediate, large enough that the panel
 * does not ask for another page the moment it opens.
 */
const PAGE_SIZE = 20;

/** Distance from the top, in pixels, that triggers loading older messages. */
const LOAD_THRESHOLD = 80;

/**
 * Conversational assistant panel.
 *
 * When the assistant proposes something that moves money, the reply carries a
 * pending operation and the panel renders a confirmation card. The money does
 * not move until the person presses Confirmar — the model cannot do it.
 *
 * Messages load the way a messaging app loads them: the newest arrive first,
 * and older ones are fetched as the person scrolls up. A long conversation
 * would otherwise mean fetching and rendering the entire thread on open.
 */
export function ChatPanel({ onOperationResolved }: { onOperationResolved?: () => void }) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [hasOlder, setHasOlder] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const scrollRef = useRef<HTMLDivElement>(null);

  /**
   * Scroll height captured just before older messages are prepended.
   *
   * Prepending content pushes everything down while the scroll offset stays
   * put, so the view appears to jump backwards. Restoring the delta after the
   * DOM updates keeps the message the person was reading exactly where it was.
   */
  const anchorHeight = useRef<number | null>(null);

  /** Guards the initial auto-scroll so it only runs once per mount. */
  const initialised = useRef(false);

  useEffect(() => {
    let cancelled = false;

    chat
      .history(PAGE_SIZE)
      .then((result) => {
        if (cancelled) return;
        setMessages(result.messages);
        setHasOlder(result.hasMore);
      })
      .catch(() => {
        // A conversation that fails to load is not worth blocking the panel:
        // the person can still start a new one.
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  const loadOlder = useCallback(async () => {
    const container = scrollRef.current;
    const oldest = messages[0];

    if (!container || !oldest || loadingOlder || !hasOlder) return;

    setLoadingOlder(true);
    anchorHeight.current = container.scrollHeight;

    try {
      const result = await chat.history(PAGE_SIZE, oldest.id);

      setMessages((current) => {
        // Ids already on screen are filtered out: a message arriving mid-fetch
        // could otherwise appear twice and break React's key uniqueness.
        const known = new Set(current.map((m) => m.id));
        const fresh = result.messages.filter((m) => !known.has(m.id));
        return [...fresh, ...current];
      });
      setHasOlder(result.hasMore);
    } catch {
      // Failing to load history is not worth an error banner: the conversation
      // on screen still works, and the attempt repeats on the next scroll.
      anchorHeight.current = null;
    } finally {
      setLoadingOlder(false);
    }
  }, [messages, loadingOlder, hasOlder]);

  // Restores the reading position after older messages are prepended.
  // useLayoutEffect, not useEffect: this must run before the browser paints,
  // or the jump is visible.
  useLayoutEffect(() => {
    const container = scrollRef.current;
    if (!container || anchorHeight.current === null) return;

    container.scrollTop = container.scrollHeight - anchorHeight.current;
    anchorHeight.current = null;
  }, [messages]);

  // Keeps the newest message in view.
  //
  // Only when the person is already near the bottom: yanking the view down
  // while they are reading older messages would undo their scrolling.
  useEffect(() => {
    const container = scrollRef.current;
    if (!container) return;

    // Prepending older messages must not trigger this.
    if (anchorHeight.current !== null) return;

    // The very first render has an empty list — nothing has loaded yet — so
    // there is nothing to scroll to. Marking `initialised` here was the bug:
    // by the time the real messages arrived, the guard already believed the
    // initial scroll had happened and skipped it, leaving the view at the
    // top showing the oldest messages instead of the newest.
    if (messages.length === 0) return;

    if (!initialised.current) {
      container.scrollTop = container.scrollHeight;
      initialised.current = true;
      return;
    }

    const distanceFromBottom =
      container.scrollHeight - container.scrollTop - container.clientHeight;

    if (distanceFromBottom < 160) {
      container.scrollTo({ top: container.scrollHeight, behavior: "smooth" });
    }
  }, [messages, sending, loading]);

  function handleScroll() {
    const container = scrollRef.current;
    if (!container) return;

    if (container.scrollTop < LOAD_THRESHOLD && hasOlder && !loadingOlder) {
      void loadOlder();
    }
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();

    const text = input.trim();
    if (!text || sending) return;

    setError(null);
    setInput("");

    // The user's message is shown immediately rather than waiting for the
    // server: the assistant can take seconds, and a chat that swallows what
    // you typed feels broken.
    const optimistic: ChatMessage = {
      id: `pending-${Date.now()}`,
      role: "user",
      content: text,
      pendingOperation: null,
      createdAt: new Date().toISOString(),
    };
    setMessages((current) => [...current, optimistic]);
    setSending(true);

    try {
      const reply = await chat.send(text);
      setMessages((current) => [...current, reply]);
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "No pudimos contactar al asistente. Intente de nuevo.",
      );
      // The optimistic message is rolled back and the text returned to the box,
      // so nothing typed is lost.
      setMessages((current) => current.filter((m) => m.id !== optimistic.id));
      setInput(text);
    } finally {
      setSending(false);
    }
  }

  function handleResolved(transferId: string, status: "confirmed" | "rejected") {
    setMessages((current) =>
      current.map((message) =>
        message.pendingOperation?.transferId === transferId
          ? {
              ...message,
              pendingOperation: { ...message.pendingOperation, status },
            }
          : message,
      ),
    );
    onOperationResolved?.();
  }

  return (
    <div className="flex h-full flex-col">
      <div
        className="flex items-center justify-between border-b px-4 py-3"
        style={{ borderColor: "var(--border-subtle)" }}
      >
        <h2
          className="text-sm font-semibold"
          style={{ color: "var(--text-primary)", fontFamily: "var(--font-display)" }}
        >
          Asistente
        </h2>
        <span className="text-xs" style={{ color: "var(--text-muted)" }}>
          Usted confirma las operaciones
        </span>
      </div>

      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="flex-1 overflow-y-auto px-4 py-4"
      >
        {loading ? (
          <div className="flex flex-col gap-3">
            <Skeleton className="h-12 w-3/4" />
            <Skeleton className="ml-auto h-12 w-2/3" />
            <Skeleton className="h-16 w-4/5" />
          </div>
        ) : messages.length === 0 ? (
          <EmptyState
            title="Pregunte lo que necesite"
            description="Pruebe con «¿cuánto dinero tengo?», «muéstrame mis últimos movimientos» o «transfiere 100 a la cuenta 4001-...»."
          />
        ) : (
          <div className="flex flex-col gap-4">
            {/* Spinner at the top while older messages arrive, so the wait is
                visible where the content will appear. */}
            {loadingOlder && (
              <div className="flex justify-center py-1">
                <span
                  aria-label="Cargando mensajes anteriores"
                  className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-t-transparent"
                  style={{ borderColor: "var(--border-default)", borderTopColor: "transparent" }}
                />
              </div>
            )}

            {!hasOlder && messages.length > PAGE_SIZE && (
              <p className="text-center text-xs" style={{ color: "var(--text-muted)" }}>
                Inicio de la conversación
              </p>
            )}

            {messages.map((message) => (
              <MessageBubble
                key={message.id}
                message={message}
                onResolved={handleResolved}
              />
            ))}

            {sending && <TypingIndicator />}
          </div>
        )}
      </div>

      {error && (
        <div
          role="alert"
          className="animate-slide-in mx-4 mb-2 rounded-md border px-3 py-2 text-sm"
          style={{
            backgroundColor: "var(--surface-sunken)",
            borderColor: "var(--color-danger)",
            color: "var(--color-danger)",
          }}
        >
          {error}
        </div>
      )}

      <form
        onSubmit={handleSubmit}
        className="flex gap-2 border-t px-4 py-3"
        style={{ borderColor: "var(--border-subtle)" }}
      >
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Escriba su consulta…"
          aria-label="Mensaje para el asistente"
          disabled={sending}
          className="flex-1 rounded-md border px-3 py-2 text-sm outline-none
            transition-[border-color,box-shadow] duration-[var(--duration-base)]
            ease-[var(--ease-standard)]
            focus:border-[var(--color-violet-600)]
            focus:shadow-[0_0_0_3px_var(--color-violet-100)]"
          style={{
            backgroundColor: "var(--surface-base)",
            borderColor: "var(--border-default)",
            color: "var(--text-primary)",
          }}
        />
        <Button type="submit" loading={sending} disabled={!input.trim()}>
          Enviar
        </Button>
      </form>
    </div>
  );
}

function MessageBubble({
  message,
  onResolved,
}: {
  message: ChatMessage;
  onResolved: (transferId: string, status: "confirmed" | "rejected") => void;
}) {
  const isUser = message.role === "user";

  return (
    <div className={`animate-slide-in flex flex-col ${isUser ? "items-end" : "items-start"}`}>
      <div
        className="max-w-[85%] rounded-lg px-3.5 py-2.5 text-sm leading-relaxed"
        style={{
          backgroundColor: isUser ? "var(--color-violet-600)" : "var(--surface-sunken)",
          color: isUser ? "#ffffff" : "var(--text-primary)",
          boxShadow: isUser ? "var(--shadow-sm)" : "none",
          // Tail on the corner nearest the sender, the shape people read as
          // "this side of the conversation".
          borderBottomRightRadius: isUser ? "var(--radius-sm)" : undefined,
          borderBottomLeftRadius: isUser ? undefined : "var(--radius-sm)",
        }}
      >
        {/* The model replies in markdown-ish prose; bold markers are stripped
            rather than rendered, to avoid injecting HTML from model output. */}
        <p className="whitespace-pre-wrap">{stripMarkdown(message.content)}</p>
      </div>

      {message.pendingOperation && (
        <div className="mt-2 w-full max-w-[85%]">
          <PendingOperationCard
            operation={message.pendingOperation}
            onResolved={onResolved}
          />
        </div>
      )}
    </div>
  );
}

function TypingIndicator() {
  return (
    <div
      className="animate-fade-in flex items-center gap-1.5 px-1"
      aria-label="El asistente está escribiendo"
    >
      {[0, 1, 2].map((index) => (
        <span
          key={index}
          className="inline-block h-1.5 w-1.5 animate-bounce rounded-full"
          style={{
            backgroundColor: "var(--text-muted)",
            animationDelay: `${index * 150}ms`,
          }}
        />
      ))}
    </div>
  );
}

/**
 * Removes markdown emphasis from model output.
 *
 * The text is rendered as plain text, never as HTML: anything a model produces
 * is untrusted input and must not become markup.
 */
function stripMarkdown(text: string): string {
  return text.replace(/\*\*(.+?)\*\*/g, "$1").replace(/\*(.+?)\*/g, "$1");
}
