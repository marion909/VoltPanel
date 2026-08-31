-- Phase 4: Quotas brauchen gemessene Werte, nicht nur Grenzwerte.
--
-- Der Verbrauch wird periodisch gemessen und hier festgehalten, statt bei
-- jeder Anfrage neu berechnet zu werden: ein `du` über eine große Site dauert
-- Sekunden und darf nicht im Anfragepfad hängen.

ALTER TABLE sites ADD COLUMN disk_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN disk_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN disk_measured_at INTEGER;

-- Traffic wird aus den Nginx-Access-Logs aufsummiert (Phase 4, Traffic-Zähler).
ALTER TABLE sites ADD COLUMN traffic_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN traffic_period TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_sites_measured ON sites(disk_measured_at);

-- Pakete brauchen eine Beschreibung für die Anzeige und ein Kennzeichen,
-- welches Paket neuen Tenants standardmäßig zugeordnet wird.
ALTER TABLE plans ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;
