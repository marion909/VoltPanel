-- Phase 3: FTP-Zugänge. Die Tabelle steht seit 0001, benutzt hat sie nie
-- jemand — es gab weder Repository noch Dienst noch Oberfläche.
--
-- Umbenannt wird eine einzige Spalte. Sie hieß password_hash, hält aber das
-- verschlüsselte Passwort: der Kunde muss es in seinen FTP-Client eintragen
-- können, ein Hash wäre dafür nutzlos. Verschlüsselt mit demselben Schlüssel
-- wie die Datenbankpasswörter und der Cloudflare-Token (AES-256-GCM, Schlüssel
-- in einer Datei mit 0600). Der Name soll sagen, was drin ist.
ALTER TABLE ftp_accounts RENAME COLUMN password_hash TO password_enc;

-- Der eigentliche Zugang lebt in der PureDB von Pure-FTPd. Diese Spalte hält
-- fest, ob unsere Zeile dort schon angekommen ist — sonst wäre nach einem
-- abgebrochenen Anlegen nicht mehr feststellbar, was gilt.
ALTER TABLE ftp_accounts ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
