-- Phase 4: Traffic-Zähler aus den Nginx-Access-Logs.
--
-- sites.traffic_bytes und traffic_period stehen seit 0002, und AddSiteTraffic
-- schreibt sie fort. Aufgerufen hat die Funktion nie jemand — es gab nichts,
-- was die Logs gelesen hätte.
--
-- Was fehlte, ist der Lesestand. Ohne ihn gäbe es nur zwei Möglichkeiten: bei
-- jedem Lauf die ganze Datei zu lesen (und alles doppelt zu zählen) oder gar
-- nicht zu zählen.
ALTER TABLE sites ADD COLUMN traffic_offset INTEGER NOT NULL DEFAULT 0;

-- Die Inode-Nummer erkennt die Logrotation. Ohne sie sähe der Zähler nach der
-- Rotation eine Datei, die kleiner ist als sein Lesestand, und müsste raten,
-- ob rotiert oder gekürzt wurde.
ALTER TABLE sites ADD COLUMN traffic_inode INTEGER NOT NULL DEFAULT 0;
