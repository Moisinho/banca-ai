import { useEffect, useRef, useState, type FormEvent } from "react";

import { Button, EmptyState, Skeleton } from "@/components/ui";
import { ApiError, chat } from "@/lib/api";
import type { ChatMessage } from "@/types/api";
import { PendingOperationCard } from "./PendingOperationCard";

/**
 * Conversational assistant panel.
 *
 * When the assistant proposes something that moves money, the reply carries a
 * pending operation and the panel renders a confirmation card. The money does
 * not move until the person presses Confirmar — the model cannot do it.
 */
export function ChatPanel({ onOperationResolved }: { onOperationResolved?: () => void }) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(true);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;

    chat
      .history()
      .then((result) => {
        if (!cancelled) setMessages(result.messages);
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

  // Keeps the newest message in view as the conversation grows.
  useEffect(() => {
    scrollRef.current?.scrollTo({
      top: scrollRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [messages, sending]);

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
          : "No pudimos contactar al asistente. Intentá de nuevo.",
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
        <h2 className="text-sm font-medium" style={{ color: "var(--text-primary)" }}>
          Asistente
        </h2>
        <span className="text-xs" style={{ color: "var(--text-muted)" }}>
          Confirmás vos las operaciones
        </span>
      </div>

      <div ref={scrollRef} className="flex-1 overflow-y-auto px-4 py-4">
        {loading ? (
          <div className="flex flex-col gap-3">
            <Skeleton className="h-12 w-3/4" />
            <Skeleton className="ml-auto h-12 w-2/3" />
            <Skeleton className="h-16 w-4/5" />
          </div>
        ) : messages.length === 0 ? (
          <EmptyState
            title="Preguntale lo que necesites"
            description="Probá con «¿cuánto dinero tengo?», «mostrame mis últimos movimientos» o «transferí 100 a la cuenta 4001-...»."
          />
        ) : (
          <div className="flex flex-col gap-4">
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
          className="mx-4 mb-2 rounded-md border px-3 py-2 text-sm"
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
          placeholder="Escribí tu consulta…"
          aria-label="Mensaje para el asistente"
          disabled={sending}
          className="flex-1 rounded-md border px-3 py-2 text-sm outline-none"
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
    <div className={`flex flex-col ${isUser ? "items-end" : "items-start"}`}>
      <div
        className="max-w-[85%] rounded-lg px-3.5 py-2.5 text-sm leading-relaxed"
        style={{
          backgroundColor: isUser ? "var(--color-violet-600)" : "var(--surface-sunken)",
          color: isUser ? "#ffffff" : "var(--text-primary)",
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
    <div className="flex items-center gap-1.5 px-1" aria-label="El asistente está escribiendo">
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
