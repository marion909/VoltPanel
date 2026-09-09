-- "Not in Spam List" je Maildomäne (Vorbild: aaPanels Mail-Domain-Tabelle) —
-- eine Domain-Blacklist-Abfrage (dbl.spamhaus.org) ist eine DNS-Anfrage nach
-- außen und soll nicht bei jedem Laden der Liste erneut laufen. Das Ergebnis
-- steht deshalb hier, mit Zeitstempel, und wird nur auf Anfrage (Knopf je
-- Zeile) neu geholt — leer/0 heißt "noch nie geprüft", nicht "sauber".
ALTER TABLE mail_domains ADD COLUMN blacklist_status TEXT NOT NULL DEFAULT '';
ALTER TABLE mail_domains ADD COLUMN blacklist_checked_at INTEGER NOT NULL DEFAULT 0;
