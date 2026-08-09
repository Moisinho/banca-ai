import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { Button, Input } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useAuth } from "./AuthContext";
import { AuthLayout } from "./AuthLayout";

export function LoginPage() {
  const { login } = useAuth();
  const navigate = useNavigate();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setFieldErrors({});

    // Client-side validation mirrors the server's, but never replaces it:
    // it only saves a round trip on obvious mistakes.
    const errors: Record<string, string> = {};
    if (!email.trim()) errors.email = "Ingrese su correo";
    if (!password) errors.password = "Ingrese su contraseña";

    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      return;
    }

    setSubmitting(true);
    try {
      await login(email, password);
      navigate("/dashboard", { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
        if (err.fields) setFieldErrors(err.fields);
      } else {
        setError("No pudimos conectarnos. Revise su conexión e intente de nuevo.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <AuthLayout
      title="Ingrese a su cuenta"
      subtitle="Gestione su dinero con la ayuda de un asistente"
    >
      <form onSubmit={handleSubmit} noValidate className="flex flex-col gap-4">
        {error && (
          <div
            role="alert"
            className="animate-slide-in rounded-md border px-3 py-2.5 text-sm"
            style={{
              backgroundColor: "var(--surface-sunken)",
              borderColor: "var(--color-danger)",
              color: "var(--color-danger)",
            }}
          >
            {error}
          </div>
        )}

        <Input
          label="Correo electrónico"
          type="email"
          autoComplete="email"
          placeholder="nombre@correo.com"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          error={fieldErrors.email}
          disabled={submitting}
        />

        <Input
          label="Contraseña"
          type="password"
          autoComplete="current-password"
          placeholder="••••••••"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          error={fieldErrors.password}
          disabled={submitting}
        />

        <Button type="submit" loading={submitting} fullWidth className="mt-2">
          Ingresar
        </Button>
      </form>

      <p className="mt-6 text-center text-sm" style={{ color: "var(--text-secondary)" }}>
        ¿No tiene cuenta?{" "}
        <Link
          to="/registro"
          className="font-medium underline underline-offset-2 transition-opacity hover:opacity-70"
          style={{ color: "var(--text-accent)" }}
        >
          Cree una
        </Link>
      </p>
    </AuthLayout>
  );
}
