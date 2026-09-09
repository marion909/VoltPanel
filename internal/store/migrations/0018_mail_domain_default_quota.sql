-- Ein Vorgabewert für die Postfach-Quota je Domäne, um das Anlegefeld
-- vorzubelegen — 0 bedeutet "kein Vorgabewert gesetzt", dieselbe Bedeutung
-- wie 0 bei einem Postfach selbst (unbegrenzt), nicht "keine Mailbox darf
-- etwas speichern".
ALTER TABLE mail_domains ADD COLUMN default_quota_mb INTEGER NOT NULL DEFAULT 0;
