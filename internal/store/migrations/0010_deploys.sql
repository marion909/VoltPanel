-- Phase 5: Git-Deploy je Site.
--
-- Der Webhook-Endpunkt liegt bewusst außerhalb des Zugriffspfads des Panels.
-- Er authentifiziert sich über sein eigenes Geheimnis und braucht die
-- Verborgenheit des Pfads nicht — und die Webhook-URL landet in den
-- Einstellungen eines fremden Dienstes, wo der Zugriffspfad des Betreibers
-- nichts verloren hat.
CREATE TABLE deploys (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id  INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Eine Site hat einen Deploy. Von ihr kommt der Systembenutzer, unter dem
    -- geklont und gebaut wird, und das Verzeichnis, in dem es geschieht.
    site_id    INTEGER NOT NULL UNIQUE REFERENCES sites(id) ON DELETE CASCADE,

    repo_url   TEXT NOT NULL,
    ref        TEXT NOT NULL DEFAULT 'main',
    -- steps sind Namen von Buildschritten als JSON-Liste, keine Kommandozeilen.
    steps      TEXT NOT NULL DEFAULT '[]',

    -- hook_id ist der öffentliche Teil der Webhook-Adresse und muss zufällig
    -- sein: er ist die einzige Angabe, mit der ein Aufrufer die richtige Site
    -- trifft. Ratbar wäre er eine Liste aller Sites des Servers.
    hook_id         TEXT NOT NULL UNIQUE,
    -- hook_secret_enc ist das Geheimnis für die Signatur, verschlüsselt.
    hook_secret_enc TEXT NOT NULL,
    auto_deploy     INTEGER NOT NULL DEFAULT 1,

    -- Der letzte Lauf, damit im Panel steht, was zuletzt geschah — auch wenn
    -- es schiefging. Ein Deploy, der nur "fehlgeschlagen" meldet, zwingt zur
    -- Shell, und die hat der Kunde nicht.
    last_release TEXT    NOT NULL DEFAULT '',
    last_commit  TEXT    NOT NULL DEFAULT '',
    last_status  TEXT    NOT NULL DEFAULT '',
    last_log     TEXT    NOT NULL DEFAULT '',
    last_run_at  INTEGER NOT NULL DEFAULT 0,

    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_deploys_tenant ON deploys(tenant_id);
