-- Phase 3: Remote-Whitelist für Datenbankzugriffe von außen.
--
-- Ein MySQL-Konto ist immer ein Paar aus Benutzer und Herkunft: 'kunde'@'localhost'
-- und 'kunde'@'203.0.113.5' sind zwei verschiedene Konten mit zwei getrennten
-- Passwörtern. Genau das ist der Grund für eine eigene Tabelle statt einer
-- zweiten Zeile in db_users.
--
-- Ein Kunde, der sein Passwort ändert, meint alle seine Zugänge. Stünden die
-- Herkünfte als eigenständige db_users da, hätte er nach der Änderung einen
-- funktionierenden und drei tote Zugänge — und keinen Hinweis darauf, welcher
-- welcher ist. Hier hängen sie am Benutzer, und das Panel ändert sie zusammen.
CREATE TABLE db_remote_hosts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id    INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    db_user_id   INTEGER NOT NULL REFERENCES db_users(id) ON DELETE CASCADE,

    -- Die Herkunft, wie MariaDB sie sieht: eine Adresse oder eine Adresse mit
    -- Netzmaske. Kein Hostname und kein %. Warum, steht in repo_db_remote.go.
    host         TEXT    NOT NULL,

    -- Wofür der Zugang da ist. Eine Whitelist ohne Notiz ist nach einem Jahr
    -- eine Liste von Zahlen, die niemand mehr zu löschen traut.
    note         TEXT    NOT NULL DEFAULT '',

    created_at   INTEGER NOT NULL
);

-- Dieselbe Herkunft zweimal am selben Benutzer wäre in MariaDB ein Konflikt,
-- nicht ein zweites Konto.
CREATE UNIQUE INDEX idx_db_remote_hosts_pair ON db_remote_hosts(db_user_id, host);
CREATE INDEX idx_db_remote_hosts_tenant ON db_remote_hosts(tenant_id);
