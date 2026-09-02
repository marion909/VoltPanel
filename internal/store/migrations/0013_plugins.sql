-- Phase 7: Server-Plugins.
--
-- Ein Plugin hier ist keine Fremddatei, die jemand hochlädt — es ist ein
-- Eintrag aus einem festen, im Quelltext geprüften Katalog
-- (internal/core/plugins.go). Diese Zeile hält nur fest, was auf *diesem*
-- Server installiert und eingeschaltet ist; die eigentliche Installation läuft
-- über dieselben Bausteine wie überall sonst — apt-Pakete aus einer
-- Namensliste, Dienste aus der Whitelist, nie Freitext.
--
-- Ein offenes Repository mit signierten Fremd-Plugins ist bewusst nicht
-- gebaut: das hieße, beliebigen Code als root laufen zu lassen und sich allein
-- auf eine Signatur zu verlassen. Das ist die Art Entscheidung, die sich nicht
-- rückgängig machen lässt, wenn sie einmal falsch war.
CREATE TABLE plugins (
    -- id ist der Schlüssel aus dem Katalog ("redis", "phpmyadmin", ...), kein
    -- Anzeigename. Er landet in Dateinamen und Dienstverweisen.
    id           TEXT PRIMARY KEY,
    enabled      INTEGER NOT NULL DEFAULT 0,
    -- config ist eine kleine JSON-Zeile für das, was ein einzelnes Plugin an
    -- eigenem Zustand braucht (bei phpMyAdmin etwa der zufällige Pfad). Kein
    -- Freitext, der irgendwo ausgeführt wird — nur Werte, die das jeweilige
    -- Plugin selbst wieder ausliest.
    config       TEXT NOT NULL DEFAULT '{}',
    installed_at INTEGER,
    updated_at   INTEGER NOT NULL
);
