-- Revierte el esquema inicial.
-- El orden importa: primero las tablas que dependen de otras.

DROP TRIGGER IF EXISTS users_set_updated_at ON users;
DROP FUNCTION IF EXISTS set_updated_at();

DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS transaction_metadata;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS users;
