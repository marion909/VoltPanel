-- Phase 2 zu Ende: Die Vhost-Vorlage kann seit Anfang an Weiterleitungen,
-- HSTS, Basic-Auth, IP-Regeln und eigene Direktiven — es gab nur keinen Weg,
-- sie zu setzen.
--
-- Die Einstellungen liegen als JSON in einer Spalte statt in einem Dutzend
-- eigener Spalten und drei Nebentabellen: es sind Listen variabler Länge, sie
-- werden immer als Ganzes gelesen und geschrieben, und es wird nie danach
-- gefiltert. Geprüft werden sie beim Schreiben und noch einmal beim Rendern.

ALTER TABLE sites ADD COLUMN settings TEXT NOT NULL DEFAULT '{}';

-- Der Cloudflare-Token ermöglicht DNS-01 und damit Wildcard-Zertifikate.
-- Er liegt verschlüsselt (AES-256-GCM), der Schlüssel in einer Datei mit 0600 —
-- ein Datenbank-Backup allein gibt ihn also nicht her.
ALTER TABLE tenants ADD COLUMN cloudflare_token TEXT NOT NULL DEFAULT '';
