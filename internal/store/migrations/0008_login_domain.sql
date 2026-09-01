-- Phase 4: eigene Anmeldeseite und Domain für den Kundenbereich.
--
-- Die reduzierte Oberfläche für Kunden steht seit dem Rollenmodell. Was fehlte,
-- ist der Weg dorthin: ein Kunde bekommt bisher dieselbe Adresse genannt wie
-- der Betreiber, samt dessen zufälligem Zugriffspfad.
--
-- Mit einer eigenen Domain je Mandant führt der Weg an die Anmeldung, die zu
-- ihm gehört — und nur dorthin: wer sich unter dieser Domain anmeldet, muss zu
-- diesem Mandanten gehören. Sonst wäre die Domain des Kunden ein zweiter
-- Eingang zum Konto des Betreibers, nur mit anderem Namen darüber.
ALTER TABLE tenants ADD COLUMN login_domain TEXT NOT NULL DEFAULT '';

-- Eindeutig, aber nur für gesetzte Werte: der Leerstring ist der Normalfall und
-- steht bei jedem Mandanten ohne eigene Domain. Ein gewöhnlicher UNIQUE-Index
-- ließe ihn nur einmal zu.
--
-- Kleingeschrieben verglichen: eine Domain ist nicht case-sensitiv, und
-- "Kunde.de" neben "kunde.de" wären zwei Einträge für dieselbe Adresse — der
-- zweite überschriebe die Anmeldung des ersten.
CREATE UNIQUE INDEX idx_tenants_login_domain
    ON tenants (lower(login_domain)) WHERE login_domain <> '';
