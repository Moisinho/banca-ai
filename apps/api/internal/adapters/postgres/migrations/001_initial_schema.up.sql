-- ==============================================================================
-- Esquema inicial de Banca AI
--
-- Postgres guarda usuarios, credenciales y metadatos de cuentas.
-- NO guarda saldos ni transacciones: de eso se encarga TigerBeetle.
--
-- Si alguna vez ves una columna `balance` acá, algo se hizo mal.
-- ==============================================================================

-- Necesaria para gen_random_uuid().
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ------------------------------------------------------------------------------
-- users
--
-- El password se guarda como hash bcrypt. El costo queda embebido dentro del
-- propio hash, así que usuarios sembrados (costo menor) y usuarios registrados
-- por la aplicación (costo 12) conviven sin problema.
-- ------------------------------------------------------------------------------
CREATE TABLE users (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email          TEXT        NOT NULL,
    password_hash  TEXT        NOT NULL,
    full_name      TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT users_email_format CHECK (email LIKE '%_@_%._%')
);

-- El email es único sin distinguir mayúsculas: "Ana@mail.com" y "ana@mail.com"
-- son la misma persona. Un índice funcional lo garantiza a nivel de base,
-- no a nivel de aplicación.
CREATE UNIQUE INDEX users_email_lower_key ON users (LOWER(email));

-- ------------------------------------------------------------------------------
-- accounts
--
-- Esta tabla es un puente. El saldo real y las transacciones viven en
-- TigerBeetle; acá sólo guardamos lo que TigerBeetle no puede: el número de
-- cuenta legible por humanos, a quién pertenece y de qué tipo es.
--
-- tigerbeetle_id es un u128. Postgres no tiene ese tipo nativo, así que usamos
-- NUMERIC(39,0): 2^128-1 tiene 39 dígitos y NUMERIC es exacto, no aproximado.
-- ------------------------------------------------------------------------------
CREATE TABLE accounts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_number  TEXT          NOT NULL UNIQUE,
    tigerbeetle_id  NUMERIC(39,0) NOT NULL UNIQUE,
    account_type    TEXT          NOT NULL,
    currency        CHAR(3)       NOT NULL DEFAULT 'USD',
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),

    CONSTRAINT accounts_type_valid
        CHECK (account_type IN ('savings', 'checking', 'investment')),

    -- El número de cuenta sigue el formato 4001-XXXX-XXXX-XXXX de los datos de prueba.
    CONSTRAINT accounts_number_format
        CHECK (account_number ~ '^[0-9]{4}(-[0-9]{4}){3}$'),

    CONSTRAINT accounts_tigerbeetle_id_positive
        CHECK (tigerbeetle_id > 0)
);

CREATE INDEX accounts_user_id_idx ON accounts (user_id);

-- ------------------------------------------------------------------------------
-- refresh_tokens
--
-- Implementa rotación con detección de reutilización.
--
-- Cada refresh emitido pertenece a una "familia" (family_id). Al usarlo se marca
-- como usado y se emite uno nuevo de la misma familia. Si alguien intenta usar
-- un token ya marcado como usado, significa que fue robado: se revoca la familia
-- entera y se cierra la sesión.
--
-- Guardamos el hash SHA-256, nunca el token en claro. Si te roban la base de
-- datos, los tokens siguen siendo inútiles.
-- ------------------------------------------------------------------------------
CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id   UUID        NOT NULL,
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Trazabilidad para auditoría de seguridad.
    user_agent  TEXT,
    ip_address  INET
);

CREATE INDEX refresh_tokens_user_id_idx    ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_id_idx  ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_token_hash_idx ON refresh_tokens (token_hash);

-- Índice parcial para la limpieza de tokens vencidos: sólo indexa los que
-- siguen activos, que son muchos menos que el total histórico.
CREATE INDEX refresh_tokens_expires_at_idx ON refresh_tokens (expires_at)
    WHERE revoked_at IS NULL;

-- ------------------------------------------------------------------------------
-- transaction_metadata
--
-- TigerBeetle guarda el movimiento de dinero, pero no admite texto libre:
-- sus campos user_data son numéricos. La descripción que escribe el usuario
-- ("Pago de gimnasio") vive acá, enlazada por el id de la transferencia.
--
-- Esta separación es deliberada: el dinero es responsabilidad de TigerBeetle,
-- el texto es responsabilidad de Postgres. Cada base hace lo que sabe hacer.
-- ------------------------------------------------------------------------------
CREATE TABLE transaction_metadata (
    transfer_id  NUMERIC(39,0) PRIMARY KEY,
    description  TEXT,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),

    CONSTRAINT transaction_metadata_transfer_id_positive
        CHECK (transfer_id > 0)
);

-- ------------------------------------------------------------------------------
-- chat_messages
--
-- Historial de conversación con la IA. Se guarda para dar contexto al modelo
-- entre mensajes y para que el usuario vea su historial al recargar.
--
-- pending_transfer_id apunta a una transferencia en estado pendiente en
-- TigerBeetle cuando la IA propone una operación que mueve dinero. Queda
-- reservada hasta que el usuario confirme o rechace.
-- ------------------------------------------------------------------------------
CREATE TABLE chat_messages (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role                 TEXT        NOT NULL,
    content              TEXT        NOT NULL,

    -- Registro de la herramienta MCP invocada, si hubo alguna.
    tool_name            TEXT,
    tool_arguments       JSONB,

    -- Transferencia pendiente esperando confirmación del usuario.
    pending_transfer_id  NUMERIC(39,0),
    confirmation_status  TEXT,

    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chat_messages_role_valid
        CHECK (role IN ('user', 'assistant', 'tool')),

    CONSTRAINT chat_messages_confirmation_status_valid
        CHECK (confirmation_status IS NULL
               OR confirmation_status IN ('pending', 'confirmed', 'rejected', 'expired'))
);

-- El id entra en el índice porque la paginación del chat ordena por
-- (created_at DESC, id DESC): dos mensajes del mismo turno pueden compartir
-- timestamp, y sin el desempate por id la página siguiente se saltaría uno o lo
-- repetiría. Con el id en el índice, Postgres resuelve el recorrido sin ordenar.
CREATE INDEX chat_messages_user_id_created_at_idx
    ON chat_messages (user_id, created_at DESC, id DESC);

-- Índice parcial: busca rápido las confirmaciones que siguen esperando respuesta.
CREATE INDEX chat_messages_pending_idx
    ON chat_messages (user_id, pending_transfer_id)
    WHERE confirmation_status = 'pending';

-- ------------------------------------------------------------------------------
-- Mantiene updated_at al día en users sin depender de que la aplicación lo haga.
-- ------------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
