-- Der Agent zählt beim Parsen der Access-Logs schon die Anfragen je Site
-- (agent.TrafficCount.Requests), nicht nur die Bytes — die Zahl wurde bisher
-- verworfen. Derselbe Rhythmus wie traffic_bytes: ein laufender Zähler je
-- Abrechnungszeitraum, kein Verlauf.
ALTER TABLE sites ADD COLUMN traffic_requests INTEGER NOT NULL DEFAULT 0;
