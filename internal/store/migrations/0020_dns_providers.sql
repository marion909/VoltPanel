-- Zweiter DNS-Provider neben Cloudflare, genauso pro Mandant verschlüsselt
-- abgelegt wie cloudflare_token — für die generische Domain-/DNS-Verwaltung.
ALTER TABLE tenants ADD COLUMN hetzner_token TEXT NOT NULL DEFAULT '';
