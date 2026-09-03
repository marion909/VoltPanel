-- Webmail (Roundcube): eine einzige, server-weite Installation.
--
-- Anders als ein Plugin (Tabelle "plugins", Zeile 0013) ist das hier kein
-- apt-Paket mit Dienst-Whitelist — es ist ein aus dem Internet geholtes
-- PHP-Programm mit eigener Datenbank, wie ein App-Store-Eintrag. Anders als
-- ein App-Store-Eintrag gehört es aber keinem Mandanten: jedes Postfach
-- jedes Mandanten soll sich hier anmelden können, genau wie bei Postfix und
-- Dovecot selbst. Deshalb weder "plugins" (falscher Installationsweg) noch
-- "sites"/"databases" (falscher Besitzer, harte tenant_id-Fremdschlüssel) —
-- eine eigene, kleine Tabelle mit genau einer möglichen Zeile.
CREATE TABLE webmail (
    -- Immer 1. Es gibt nie eine zweite Installation.
    id              INTEGER PRIMARY KEY,
    hostname        TEXT NOT NULL,
    php_version     TEXT NOT NULL,
    db_name         TEXT NOT NULL,
    db_user         TEXT NOT NULL,
    -- Verschlüsselt wie jedes andere Datenbankpasswort in diesem Panel
    -- (authn.SecretBox) — gebraucht nur, falls config.inc.php je neu
    -- geschrieben werden muss, ohne die Datenbank selbst anzufassen.
    db_password_enc TEXT NOT NULL,
    installed_at    INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
