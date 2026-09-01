-- Phase 3: Backup-Ziele.
--
-- Die Spalte backups.destination steht seit 0001 da und trägt den Kommentar
-- "local | s3 | b2 | ftp". Geschrieben wurde dort bisher immer 'local' — es gab
-- nichts anderes. Das ist die dritte Spalte in diesem Schema, die eine Absicht
-- festhielt, für die nie Code entstanden ist.
CREATE TABLE backup_targets (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id    INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Der Name, unter dem das Ziel in der Oberfläche steht. Frei wählbar; die
    -- Angaben darunter sagen einem Menschen nach einem halben Jahr nichts mehr.
    name         TEXT    NOT NULL,
    kind         TEXT    NOT NULL,                 -- s3 | b2 | ftp

    -- S3 und B2. B2 ist S3 mit einem anderen Endpunkt — dieselben Felder,
    -- deshalb keine zweite Tabelle.
    endpoint     TEXT    NOT NULL DEFAULT '',
    region       TEXT    NOT NULL DEFAULT '',
    bucket       TEXT    NOT NULL DEFAULT '',
    path_style   INTEGER NOT NULL DEFAULT 0,       -- MinIO & Co. brauchen das

    -- FTP.
    host         TEXT    NOT NULL DEFAULT '',
    port         INTEGER NOT NULL DEFAULT 0,
    use_tls      INTEGER NOT NULL DEFAULT 1,
    -- Ein selbstsigniertes Zertifikat durchlassen. Die Alternative wäre, TLS
    -- ganz abzuschalten, und das wäre schlechter.
    skip_verify  INTEGER NOT NULL DEFAULT 0,

    -- Für beide: bei S3 der Access Key, bei FTP der Benutzername. Ein Konto
    -- ist ein Konto, auch wenn der Anbieter es anders nennt.
    username     TEXT    NOT NULL DEFAULT '',
    -- Das Geheimnis, verschlüsselt mit demselben Schlüssel wie die
    -- Datenbank- und FTP-Passwörter (AES-256-GCM). Es muss im Klartext
    -- herausholbar sein — ein Hash liesse sich nicht signieren und nicht
    -- übermitteln.
    secret_enc   TEXT    NOT NULL DEFAULT '',

    -- Unterverzeichnis im Bucket bzw. auf dem FTP-Server.
    base_path    TEXT    NOT NULL DEFAULT '',

    enabled      INTEGER NOT NULL DEFAULT 1,

    -- Was beim letzten Mal geschah. Ohne diese drei Spalten ist ein Ziel, das
    -- seit Wochen still scheitert, von einem funktionierenden nicht zu
    -- unterscheiden — und genau das ist der Fehler, der bei Backups weh tut.
    last_error   TEXT    NOT NULL DEFAULT '',
    last_used_at INTEGER,
    last_ok_at   INTEGER,

    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

-- Zwei Ziele desselben Namens im selben Mandanten wären in jeder Auswahlliste
-- eine Verwechslung.
CREATE UNIQUE INDEX idx_backup_targets_name ON backup_targets(tenant_id, name);
CREATE INDEX idx_backup_targets_tenant ON backup_targets(tenant_id);

-- Wohin ein Backup gegangen ist, steht bisher nur als Wort in destination.
-- Welches Ziel gemeint war, lässt sich damit nicht sagen, sobald es zwei gibt.
ALTER TABLE backups ADD COLUMN target_id INTEGER REFERENCES backup_targets(id) ON DELETE SET NULL;

-- Der Schlüssel bzw. Pfad auf der Gegenseite. Ohne ihn ist ein hochgeladenes
-- Archiv im Panel nicht wiederzufinden.
ALTER TABLE backups ADD COLUMN remote_path TEXT NOT NULL DEFAULT '';
