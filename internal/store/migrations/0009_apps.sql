-- Phase 5: eine App ist eine systemd-Unit plus Reverse-Proxy.
--
-- Der Vhost steht seit Phase 2: der Site-Typ `proxy` schreibt einen fertigen
-- proxy_pass. Was fehlte, ist die andere Hälfte — der Prozess, auf den er
-- zeigt.
CREATE TABLE apps (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id  INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Eine App gehört zu genau einer Site. Von dort kommt alles, was der Agent
    -- ohnehin nicht aus der Anfrage nimmt: der Systembenutzer, das
    -- Arbeitsverzeichnis, der Vhost.
    site_id    INTEGER NOT NULL UNIQUE REFERENCES sites(id) ON DELETE CASCADE,

    -- name wird ein Unit- und ein Dateiname auf der Maschine, muss also über
    -- alle Mandanten hinweg eindeutig sein. Deshalb wird er nicht eingegeben,
    -- sondern aus der Domain gebildet: Domains sind es schon.
    --
    -- Eingegeben wäre er zweierlei Ärger. Erstens müsste die Fehlermeldung
    -- "schon vergeben" lauten — und verriete damit einem Mandanten, welche
    -- Namen ein anderer benutzt. Zweitens wäre er eine zweite Wahrheit neben
    -- der Domain, die auseinanderlaufen kann.
    name       TEXT    NOT NULL UNIQUE,

    -- runtime ist der Name einer Laufzeitumgebung, nie ein Pfad. Den sucht der
    -- Agent in seiner eigenen Liste.
    runtime    TEXT    NOT NULL DEFAULT 'node',
    -- args sind die Argumente danach, als JSON-Liste. Jedes einzeln, weil eine
    -- Zeichenkette jemand zerlegen müsste.
    args       TEXT    NOT NULL DEFAULT '[]',

    -- port ist der Port auf 127.0.0.1, auf den der Vhost zeigt. Vergeben wird
    -- er vom Panel, nicht vom Kunden: zwei Apps auf demselben Port wären zwei
    -- Sites, von denen eine nicht startet.
    port       INTEGER NOT NULL,

    -- env liegt verschlüsselt. In einer App-Umgebung stehen regelmäßig
    -- Datenbankpasswörter, und die Datenbank des Panels ist eine Datei.
    env        TEXT    NOT NULL DEFAULT '',

    -- enabled sagt, ob die App laufen soll. Ob sie läuft, sagt der Agent.
    enabled    INTEGER NOT NULL DEFAULT 1,

    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_apps_tenant ON apps(tenant_id);

-- Ein Port gehört genau einer App. Ohne diese Zusage entscheidet die
-- Reihenfolge des Startens, welche der beiden Sites erreichbar ist — und die
-- andere fällt mit "Address already in use" aus, ohne dass jemand den
-- Zusammenhang sieht.
CREATE UNIQUE INDEX idx_apps_port ON apps(port);
