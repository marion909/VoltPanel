-- Phase 5: eine App läuft entweder als systemd-Unit oder als Container.
--
-- Zwei Tabellen dafür wären zwei Wege zu demselben Ziel: beide belegen einen
-- Port, beide bekommen den Vhost der Site auf sich gerichtet, beide haben eine
-- Umgebung. Der Unterschied ist, was den Prozess startet — und das ist eine
-- Spalte, keine zweite Hälfte des Panels.
ALTER TABLE apps ADD COLUMN kind TEXT NOT NULL DEFAULT 'native';

-- Nur für kind='docker'.
ALTER TABLE apps ADD COLUMN image TEXT NOT NULL DEFAULT '';

-- volumes ist eine JSON-Liste. Die Quelle ist immer relativ zur Wurzel der
-- Site: absolut wäre sie der kürzeste Weg, das Dateisystem des Servers in
-- einen Container zu hängen.
ALTER TABLE apps ADD COLUMN volumes TEXT NOT NULL DEFAULT '[]';

-- Grenzen für den Container. 0 heißt auch hier "unbegrenzt".
ALTER TABLE apps ADD COLUMN memory_mb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN cpus TEXT NOT NULL DEFAULT '';

-- container_port ist der Port, auf dem das Image horcht. apps.port bleibt der
-- Port auf 127.0.0.1, auf den der Vhost zeigt — bei einer nativen App sind
-- beide dasselbe, bei einem Container fast nie.
ALTER TABLE apps ADD COLUMN container_port INTEGER NOT NULL DEFAULT 0;
