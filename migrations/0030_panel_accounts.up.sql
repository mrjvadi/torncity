-- 0030_panel_accounts — the operators of the web panel (cmd/panel): who may
-- sign in, their sessions, and the answers to requests already carried out.
--
-- ACCOUNTS. An account is created, re-keyed, disabled and enrolled in
-- two-factor ONLY from the server (`admin panel user ...`), each change with
-- its audit row. The password is stored as an argon2id hash in the PHC string
-- format, never as text. A run of failed sign-ins locks the account for a
-- while that doubles with every further failure (configs/config.yml panel.*).
--
-- SESSIONS. The browser holds a random token; the table holds only its
-- SHA-256, so a copy of this table signs nobody in. A session ends at the
-- first of: its absolute expiry, its idle expiry (last_seen_at), sign-out,
-- its account being disabled.
--
-- IDEMPOTENCY. Every mutating request carries a request id; its answer is
-- kept here, so a retried request is answered again instead of acting twice.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE panel_accounts (
    id                  uuid        PRIMARY KEY,
    username            text        NOT NULL,
    password_hash       text        NOT NULL,
    totp_secret         text        NULL,
    totp_enabled        boolean     NOT NULL,
    status              text        NOT NULL,
    failed_logins       int         NOT NULL,
    locked_until        timestamptz NULL,
    last_login_at       timestamptz NULL,
    password_changed_at timestamptz NOT NULL,
    created_by          text        NOT NULL,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,

    CONSTRAINT panel_accounts_username_key UNIQUE (username),
    CONSTRAINT panel_accounts_username_check CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,31}$'),
    CONSTRAINT panel_accounts_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT panel_accounts_failed_check CHECK (failed_logins >= 0),
    CONSTRAINT panel_accounts_totp_check CHECK (NOT totp_enabled OR totp_secret IS NOT NULL)
);

CREATE TABLE panel_sessions (
    token_hash    text        PRIMARY KEY,
    account_id    uuid        NOT NULL REFERENCES panel_accounts (id),
    csrf_token    text        NOT NULL,
    created_at    timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz NULL,
    client_ip     text        NOT NULL,
    user_agent    text        NOT NULL,

    CONSTRAINT panel_sessions_expiry_check CHECK (created_at < expires_at)
);
CREATE INDEX panel_sessions_account_idx ON panel_sessions (account_id) WHERE revoked_at IS NULL;

CREATE TABLE panel_requests (
    account_id  uuid        NOT NULL REFERENCES panel_accounts (id),
    request_id  text        NOT NULL,
    route       text        NOT NULL,
    status      int         NOT NULL,
    response    jsonb       NULL,
    created_at  timestamptz NOT NULL,

    CONSTRAINT panel_requests_pkey PRIMARY KEY (account_id, request_id),
    CONSTRAINT panel_requests_id_check CHECK (length(request_id) BETWEEN 8 AND 64)
);
CREATE INDEX panel_requests_created_idx ON panel_requests (created_at);

COMMIT;
