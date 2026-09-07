-- CreatePHPPool prüfte vor dem Insert nicht, ob für die Site schon ein Pool
-- existiert. Zwei nahezu gleichzeitige Aufrufe für dieselbe Site (mit
-- unterschiedlichem pool_name) konnten deshalb zwei Zeilen mit derselben
-- site_id anlegen — PHPPoolBySite liefert ohne ORDER BY/LIMIT die erste
-- Treffer-Zeile, welche das war, war unbestimmt; die andere blieb im Panel
-- unsichtbar, existierte aber als laufender PHP-FPM-Pool weiter.
--
-- Vor dem UNIQUE-Index eine eventuell schon vorhandene doppelte Zeile je
-- Site aufräumen (die mit der kleineren id bleibt, als die zuerst
-- angelegte) — sonst schlüge diese Migration auf einem Server fehl, auf
-- dem die Race schon einmal zugeschlagen hat.
DELETE FROM php_pools WHERE id NOT IN (
    SELECT MIN(id) FROM php_pools GROUP BY site_id
);
CREATE UNIQUE INDEX idx_php_pools_site ON php_pools(site_id);
