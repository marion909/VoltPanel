-- Phase 6: Mail. Domänen, Postfächer, Aliase.
--
-- Die Entscheidung, die der Fahrplan vorweg verlangt hat, ist gefallen:
-- eigener Stack — Postfix, Dovecot, Rspamd, OpenDKIM — mit virtuellen Domänen
-- aus dieser Datenbank. Nicht Mailcow. Ein Docker-Stack, den das Panel nur
-- verwaltet, wäre ein zweites Panel mit eigener Datenhaltung, eigenen
-- Benutzern und eigener Vorstellung davon, wem was gehört; die Mandantengrenze
-- müsste dann an zwei Stellen stimmen.
--
-- Was hier steht, ist die Datenhaltung. Postfix und Dovecot lesen sie nicht
-- selbst: der Agent schreibt aus diesen Zeilen die Map-Dateien, so wie er aus
-- den Sites die Vhosts schreibt. Ein Mailserver, der in die Panel-Datenbank
-- greift, hätte eine zweite Kennung darauf und wäre der Weg, über eine
-- Mail-Lücke an die Passwörter aller Kunden zu kommen.

CREATE TABLE mail_domains (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id  INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Eine Domäne gehört genau einem Mandanten, serverweit eindeutig. Zwei
    -- Mandanten mit derselben Maildomäne wären zwei Postfächer für dieselbe
    -- Adresse — und die Zustellung entschiede, wer die Mail bekommt.
    domain     TEXT    NOT NULL UNIQUE,

    -- active trennt "eingerichtet" von "nimmt Post an". Eine Domäne
    -- abzuschalten, ohne die Postfächer zu löschen, ist der Normalfall beim
    -- Umzug: erst hier aus, dann drüben an.
    active     INTEGER NOT NULL DEFAULT 1,

    -- catch_all ist die Adresse, an die alles geht, was kein Postfach trifft.
    -- Leer heißt: unbekannte Empfänger werden abgewiesen, und das ist die
    -- richtige Voreinstellung — ein Catch-All sammelt vor allem Spam.
    catch_all  TEXT    NOT NULL DEFAULT '',

    -- DKIM. Der öffentliche Teil geht in den DNS-Eintrag, der private bleibt
    -- hier — verschlüsselt, wie jedes andere Geheimnis in dieser Datenbank.
    -- Wer ihn hat, unterschreibt Mail im Namen dieser Domäne.
    dkim_selector TEXT NOT NULL DEFAULT '',
    dkim_private  TEXT NOT NULL DEFAULT '',
    dkim_public   TEXT NOT NULL DEFAULT '',

    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_mail_domains_tenant ON mail_domains(tenant_id);

CREATE TABLE mailboxes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id  INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    domain_id  INTEGER NOT NULL REFERENCES mail_domains(id) ON DELETE CASCADE,

    local_part TEXT    NOT NULL,

    -- address ist local_part@domain, noch einmal ausgeschrieben.
    --
    -- Redundant, und trotzdem richtig: die Eindeutigkeit einer Mailadresse ist
    -- die Zusage, um die es geht, und sie soll die Datenbank geben und nicht
    -- eine Prüfung im Code, die jemand später umgeht. Zusammengesetzt wird sie
    -- an genau einer Stelle im Store.
    address    TEXT    NOT NULL UNIQUE,

    -- Das Passwort liegt verschlüsselt, nicht als Hash.
    --
    -- Anders als bei einem Panel-Konto: ein Mailkonto wird in einem
    -- Mailprogramm eingetragen, und die Frage "wie war noch mein Passwort"
    -- kommt hier wirklich vor. Denselben Weg gehen FTP- und Datenbankzugänge
    -- schon. Was Dovecot bekommt, ist trotzdem ein Hash — den erzeugt der
    -- Agent beim Schreiben der Map.
    password_enc TEXT  NOT NULL DEFAULT '',

    -- 0 heißt auch hier "unbegrenzt", wie überall im Panel.
    quota_mb   INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 1,

    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_mailboxes_tenant ON mailboxes(tenant_id);
CREATE INDEX idx_mailboxes_domain ON mailboxes(domain_id);

CREATE TABLE mail_aliases (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id  INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    domain_id  INTEGER NOT NULL REFERENCES mail_domains(id) ON DELETE CASCADE,

    -- source ist die Adresse, die jemand anschreibt.
    source     TEXT    NOT NULL,
    -- destination ist genau ein Ziel. Mehrere Ziele sind mehrere Zeilen:
    -- so lässt sich eines entfernen, ohne die Liste neu zu schreiben, und die
    -- Eindeutigkeit unten hat etwas, woran sie sich halten kann.
    destination TEXT   NOT NULL,

    active     INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_mail_aliases_tenant ON mail_aliases(tenant_id);
CREATE UNIQUE INDEX idx_mail_aliases_paar ON mail_aliases(source, destination);
