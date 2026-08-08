import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";

import { Button, Input } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { useAuth } from "./AuthContext";
import { AuthLayout } from "./AuthLayout";

export function RegisterPage() {
  const { register } = useAuth();
  const navigate = useNavigate();

  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setFieldErrors({});

    // Mirrors the backend's rules so the person sees the problem before a
    // round trip. The server still validates: this is convenience, not trust.
    const errors: Record<string, string> = {};

    if (!fullName.trim()) {
      errors.fullName = "Ingresá tu nombre completo";
    }
    if (!email.trim()) {
      errors.email = "Ingresá tu correo";
    } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      errors.email = "El correo no tiene un formato válido";
    }
    if (password.length < 8) {
      errors.password = "La contraseña debe tener al menos 8 caracteres";
    } else if (!/[a-zA-Z]/.test(password) || !/\d/.test(password)) {
      errors.password = "La contraseña debe incluir letras y números";
    }

    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      return;
    }

    setSubmitting(true);
    try {
      await register(email, password, fullName);
      navigate("/dashboard", { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
        if (err.fields) setFieldErrors(err.fields);
      } else {
        setError("No pudimos conectarnos. Revisá tu conexión e intentá de nuevo.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <AuthLayout
      title="Creá tu cuenta"
      subtitle="Te abrimos una cuenta bancaria al instante"
    >
      <form onSubmit={handleSubmit} noValidate className="flex flex-col gap-4">
        {error && (
          <div
            role="alert"
            className="rounded-md border px-3 py-2.5 text-sm"
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
          label="Nombre completo"
          type="text"
          autoComplete="name"
          placeholder="Isabel Hernández Álvarez"
          value={fullName}
          onChange={(e) => setFullName(e.target.value)}
          error={fieldErrors.fullName}
          disabled={submitting}
        />

        <Input
          label="Correo electrónico"
          type="email"
          autoComplete="email"
          placeholder="tu@correo.com"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          error={fieldErrors.email}
          disabled={submitting}
        />

        <Input
          label="Contraseña"
          type="password"
          autoComplete="new-password"
          placeholder="••••••••"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          error={fieldErrors.password}
          hint="Al menos 8 caracteres, con letras y números"
          disabled={submitting}
        />

        <Button type="submit" loading={submitting} fullWidth className="mt-2">
          Crear cuenta
        </Button>
      </form>

      <p className="mt-6 text-center text-sm" style={{ color: "var(--text-secondary)" }}>
        ¿Ya tenés cuenta?{" "}
        <Link
          to="/ingresar"
          className="font-medium underline underline-offset-2"
          style={{ color: "var(--text-accent)" }}
        >
          Ingresá
        </Link>
      </p>
    </AuthLayout>
  );
}
