package agent

import "testing"

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
