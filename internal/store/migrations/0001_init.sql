-- VoltPanel schema v1
-- Grundregel: alles was einem Tenant gehört, trägt tenant_id NOT NULL.
-- Der Repository-Layer erzwingt den Scope, siehe internal/store/scope.go.

CREATE TABLE tenants (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    slug        TEXT    NOT NULL UNIQUE,
    plan_id     INTEGER REFERENCES plans(id) ON DELETE SET NULL,
    status      TEXT    NOT NULL DEFAULT 'active',  -- active | suspended
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Hosting-Pakete: Vorlage für die Quotas eines Tenants (Phase 4).
CREATE TABLE plans (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL UNIQUE,
    max_sites       INTEGER NOT NULL DEFAULT 0,   -- 0 = unbegrenzt
    max_databases   INTEGER NOT NULL DEFAULT 0,
    max_ftp         INTEGER NOT NULL DEFAULT 0,
    max_mailboxes   INTEGER NOT NULL DEFAULT 0,
    max_cronjobs    INTEGER NOT NULL DEFAULT 0,
    disk_quota_mb   INTEGER NOT NULL DEFAULT 0,
    traffic_quota_mb INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

CREATE TABLE users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email           TEXT    NOT NULL,
    display_name    TEXT    NOT NULL DEFAULT '',
    password_hash   TEXT    NOT NULL,
    role            TEXT    NOT NULL,             -- owner | admin | reseller | customer
    totp_secret     TEXT    NOT NULL DEFAULT '',  -- verschlüsselt, leer = 2FA aus
    totp_enabled    INTEGER NOT NULL DEFAULT 0,
    must_change_pw  INTEGER NOT NULL DEFAULT 0,
    locale          TEXT    NOT NULL DEFAULT 'de',
    last_login_at   INTEGER,
    failed_logins   INTEGER NOT NULL DEFAULT 0,
    locked_until    INTEGER,
    status          TEXT    NOT NULL DEFAULT 'active',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_tenant ON users(tenant_id);

CREATE TABLE sessions (
    id          TEXT    PRIMARY KEY,              -- zufälliger Token-Hash
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id   INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_agent  TEXT    NOT NULL DEFAULT '',
    ip          TEXT    NOT NULL DEFAULT '',
    expires_at  INTEGER NOT NULL,
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);

CREATE TABLE sites (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    domain          TEXT    NOT NULL,
    aliases         TEXT    NOT NULL DEFAULT '',  -- JSON-Array
    type            TEXT    NOT NULL,             -- static | php | proxy
    system_user     TEXT    NOT NULL,             -- eigener Linux-User pro Site
    root_path       TEXT    NOT NULL,
    document_root   TEXT    NOT NULL,             -- relativ zu root_path, z.B. "public"
    php_version     TEXT    NOT NULL DEFAULT '',  -- "8.3" bei type=php
    proxy_target    TEXT    NOT NULL DEFAULT '',  -- z.B. http://127.0.0.1:3000 bei type=proxy
    ssl_enabled     INTEGER NOT NULL DEFAULT 0,
    force_https     INTEGER NOT NULL DEFAULT 1,
    hsts            INTEGER NOT NULL DEFAULT 0,
    status          TEXT    NOT NULL DEFAULT 'active',  -- active | suspended
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_sites_domain ON sites(domain);
CREATE INDEX idx_sites_tenant ON sites(tenant_id);

CREATE TABLE php_pools (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id           INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    site_id             INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    php_version         TEXT    NOT NULL,
    pool_name           TEXT    NOT NULL,
    socket_path         TEXT    NOT NULL,
    pm                  TEXT    NOT NULL DEFAULT 'ondemand',
    max_children        INTEGER NOT NULL DEFAULT 10,
    memory_limit        TEXT    NOT NULL DEFAULT '256M',
    max_execution_time  INTEGER NOT NULL DEFAULT 30,
    upload_max_filesize TEXT    NOT NULL DEFAULT '64M',
    open_basedir        TEXT    NOT NULL DEFAULT '',
    disable_functions   TEXT    NOT NULL DEFAULT '',
    extra_ini           TEXT    NOT NULL DEFAULT '',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_php_pools_name ON php_pools(pool_name);
CREATE INDEX idx_php_pools_tenant ON php_pools(tenant_id);

CREATE TABLE databases (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    site_id     INTEGER REFERENCES sites(id) ON DELETE SET NULL,
    name        TEXT    NOT NULL,
    engine      TEXT    NOT NULL DEFAULT 'mariadb',
    charset     TEXT    NOT NULL DEFAULT 'utf8mb4',
    collation   TEXT    NOT NULL DEFAULT 'utf8mb4_unicode_ci',
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_databases_name ON databases(name);
CREATE INDEX idx_databases_tenant ON databases(tenant_id);

CREATE TABLE db_users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    database_id     INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    username        TEXT    NOT NULL,
    host_pattern    TEXT    NOT NULL DEFAULT 'localhost',
    grants          TEXT    NOT NULL DEFAULT 'ALL',
    password_enc    TEXT    NOT NULL DEFAULT '',  -- verschlüsselt, für Anzeige/Export
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_db_users_name ON db_users(username, host_pattern);
CREATE INDEX idx_db_users_tenant ON db_users(tenant_id);

CREATE TABLE certs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    site_id         INTEGER REFERENCES sites(id) ON DELETE CASCADE,
    domains         TEXT    NOT NULL,             -- JSON-Array
    issuer          TEXT    NOT NULL DEFAULT 'letsencrypt',
    challenge       TEXT    NOT NULL DEFAULT 'http-01',  -- http-01 | dns-01
    cert_path       TEXT    NOT NULL DEFAULT '',
    key_path        TEXT    NOT NULL DEFAULT '',
    not_before      INTEGER,
    not_after       INTEGER,
    last_renewal_at INTEGER,
    last_error      TEXT    NOT NULL DEFAULT '',
    auto_renew      INTEGER NOT NULL DEFAULT 1,
    status          TEXT    NOT NULL DEFAULT 'pending',  -- pending | active | failed
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_certs_tenant ON certs(tenant_id);
CREATE INDEX idx_certs_expiry ON certs(not_after);

CREATE TABLE ftp_accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    site_id         INTEGER REFERENCES sites(id) ON DELETE CASCADE,
    username        TEXT    NOT NULL,
    password_hash   TEXT    NOT NULL,
    home_dir        TEXT    NOT NULL,
    uid             INTEGER NOT NULL,
    gid             INTEGER NOT NULL,
    quota_mb        INTEGER NOT NULL DEFAULT 0,
    status          TEXT    NOT NULL DEFAULT 'active',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_ftp_username ON ftp_accounts(username);
CREATE INDEX idx_ftp_tenant ON ftp_accounts(tenant_id);

CREATE TABLE cronjobs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    site_id         INTEGER REFERENCES sites(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    schedule        TEXT    NOT NULL,             -- 5-Feld-Crontab
    command         TEXT    NOT NULL,
    run_as          TEXT    NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    last_run_at     INTEGER,
    last_exit_code  INTEGER,
    last_output     TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_cronjobs_tenant ON cronjobs(tenant_id);

CREATE TABLE backups (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    site_id         INTEGER REFERENCES sites(id) ON DELETE SET NULL,
    database_id     INTEGER REFERENCES databases(id) ON DELETE SET NULL,
    kind            TEXT    NOT NULL,             -- site | database | config | full
    destination     TEXT    NOT NULL DEFAULT 'local',  -- local | s3 | b2 | ftp
    path            TEXT    NOT NULL DEFAULT '',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    checksum        TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL DEFAULT 'pending',  -- pending | running | ok | failed
    error           TEXT    NOT NULL DEFAULT '',
    started_at      INTEGER,
    finished_at     INTEGER,
    created_at      INTEGER NOT NULL
);
CREATE INDEX idx_backups_tenant ON backups(tenant_id);

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER,                          -- NULL = systemweite Aktion
    user_id     INTEGER,
    actor       TEXT    NOT NULL DEFAULT '',      -- E-Mail oder "system"/"cli"
    action      TEXT    NOT NULL,                 -- z.B. site.create
    target_type TEXT    NOT NULL DEFAULT '',
    target_id   TEXT    NOT NULL DEFAULT '',
    detail      TEXT    NOT NULL DEFAULT '',      -- JSON, ohne Secrets
    ip          TEXT    NOT NULL DEFAULT '',
    result      TEXT    NOT NULL DEFAULT 'ok',    -- ok | error
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_audit_tenant ON audit_log(tenant_id, created_at);
CREATE INDEX idx_audit_action ON audit_log(action, created_at);

CREATE TABLE settings (
    key         TEXT    PRIMARY KEY,
    value       TEXT    NOT NULL,
    updated_at  INTEGER NOT NULL
);
