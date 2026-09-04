-- VerifyTOTP prüfte einen Code bisher nur gegen Zeitfenster, ohne sich zu
-- merken, welches Fenster bereits einmal akzeptiert wurde. Wegen Skew=1 bleibt
-- derselbe Code bis zu ~90 Sekunden lang (vorheriges/aktuelles/nächstes
-- 30s-Fenster) beliebig oft gültig — ein abgefangener/mitgeloggter Code war in
-- dieser Zeit für Login bzw. 2FA-Ein-/Ausschalten mehrfach nutzbar.
--
-- NULL heißt "noch nie ein Code angenommen" — nicht 0, das ein gültiger
-- Zeitschritt (die Unix-Epoche selbst) wäre.
ALTER TABLE users ADD COLUMN totp_last_step INTEGER;
