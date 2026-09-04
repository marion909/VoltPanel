package agent

import (
	"fmt"
	"testing"
	"time"
)

// TestCheckMySQLDBNameLehntSystemschemataAb hält die Lücke fest, die
// opMySQLDropDB/opMySQLGrant/opMySQLCreateUser vorher hatten: reMyDBName
// prüfte nur die Zeichenform eines Datenbanknamens, nicht ob er ein
// MySQL-Systemschema ist. "DROP DATABASE mysql" bzw. "GRANT ALL ... ON
// mysql.*" waren damit über die normale Datenbank-Verwaltung erreichbar.
func TestCheckMySQLDBNameLehntSystemschemataAb(t *testing.T) {
	for _, name := range []string{"mysql", "information_schema", "performance_schema", "sys"} {
		if err := checkMySQLDBName(name); err == nil {
			t.Errorf("checkMySQLDBName(%q) = nil, erwartet war eine Ablehnung", name)
		}
	}
}

func TestCheckMySQLDBNameErlaubtGewoehnlicheNamen(t *testing.T) {
	for _, name := range []string{"kunde_shop", "wordpress1", "app"} {
		if err := checkMySQLDBName(name); err != nil {
			t.Errorf("checkMySQLDBName(%q) = %v, erwartet war keine Ablehnung", name, err)
		}
	}
}

// TestAccountCreatedBeforeUnterscheidetFrischVonVerwaist hält die Lücke fest,
// die dropStaleAccounts vorher hatte: es löschte pauschal jedes Konto mit
// passendem Präfix, unabhängig vom Alter — ein Konto, das eine parallele
// Anfrage gerade erst angelegt hatte, traf das genauso wie einen echten Rest
// eines abgestürzten Laufs.
func TestAccountCreatedBeforeUnterscheidetFrischVonVerwaist(t *testing.T) {
	now := time.Now()
	cutoff := now.Add(-staleAccountAge)

	frisch := fmt.Sprintf("%s%08x_%s", importUserPrefix, uint32(now.Unix()), "abcdef0123")
	if accountCreatedBefore(frisch, importUserPrefix, cutoff) {
		t.Errorf("frisches Konto %q gilt als verwaist", frisch)
	}

	alt := fmt.Sprintf("%s%08x_%s", importUserPrefix, uint32(now.Add(-2*staleAccountAge).Unix()), "abcdef0123")
	if !accountCreatedBefore(alt, importUserPrefix, cutoff) {
		t.Errorf("altes Konto %q gilt nicht als verwaist", alt)
	}

	// Ein Name, der nicht ins erwartete Format passt (z. B. ein Rest aus
	// einer Fassung vor diesem Zeitstempel), gilt bewusst als nicht verwaist —
	// besser übersehen als ein Missverständnis löschen.
	for _, unpassend := range []string{
		importUserPrefix + "ab12cd34",            // kein Unterstrich an Position 8
		importUserPrefix + "xxxxxxxx_abcdef0123", // keine Hexziffern
		importUserPrefix + "abc",                 // zu kurz
	} {
		if accountCreatedBefore(unpassend, importUserPrefix, cutoff) {
			t.Errorf("unerwartetes Format %q gilt fälschlich als verwaist", unpassend)
		}
	}
}

// TestThrowawayAccountNameBleibtInnerhalbDerRegex stellt sicher, dass das
// längste erzeugbare Konto (importUserPrefix, der längere der beiden Präfixe)
// weiterhin die eigene Formprüfung reMyUser besteht — sonst würde
// dropStaleAccounts das gerade erst angelegte Konto beim nächsten Aufruf
// stillschweigend ignorieren.
func TestThrowawayAccountNameBleibtInnerhalbDerRegex(t *testing.T) {
	name := fmt.Sprintf("%s%08x_%s", importUserPrefix, uint32(time.Now().Unix()), "abcdef0123")
	if !reMyUser.MatchString(name) {
		t.Fatalf("erzeugter Kontoname %q (Länge %d) besteht reMyUser nicht", name, len(name))
	}
}
