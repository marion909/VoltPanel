package templates

import (
	"strings"
	"testing"
)

func gueltigeWPConfig(t *testing.T) WordPressConfigData {
	t.Helper()
	geheim, err := NewWordPressSecrets()
	if err != nil {
		t.Fatal(err)
	}
	geheim.DBName = "wp_shop"
	geheim.DBUser = "wp_shop_user"
	geheim.DBPassword = "ein-generiertes-passwort"
	return geheim
}

// phpstr ist die einzige Schranke zwischen einem eingesetzten Wert und
// ausführbarem PHP. Ein Wert mit einem einfachen Anführungszeichen darin
// bricht ohne sie aus der Zeichenkette aus — genau das, wonach hier geprüft
// wird, statt nur "irgendwas kommt raus".
func TestPHPStrEscaptAnführungszeichenUndBackslash(t *testing.T) {
	faelle := map[string]string{
		`normal`:            `normal`,
		`mit ' anführung`:   `mit \' anführung`,
		`mit \ backslash`:   `mit \\ backslash`,
		`'); system('id');`: `\'); system(\'id\');`,
	}
	for in, want := range faelle {
		if got := phpstr(in); got != want {
			t.Errorf("phpstr(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

// Ein Wert mit einem einfachen Anführungszeichen darf in der erzeugten Datei
// nicht als PHP-Syntax ankommen — er muss maskiert dastehen, sonst bricht er
// aus der Zeichenkette aus und alles danach ist PHP, das der Angreifer
// bestimmt statt VoltPanel.
func TestRenderWordPressConfigEscaptDasPasswort(t *testing.T) {
	d := gueltigeWPConfig(t)
	d.DBPassword = `ein'passwort\mit"sonderzeichen`

	out, err := RenderWordPressConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `ein\'passwort\\mit"sonderzeichen`) {
		t.Errorf("das passwort steht nicht maskiert in der datei:\n%s", out)
	}
	// Und unmaskiert darf es nicht vorkommen — sonst stünde daneben noch eine
	// zweite, kaputte Fassung.
	if strings.Contains(out, `'ein'passwort`) {
		t.Error("das passwort steht auch unmaskiert in der datei")
	}
}

// Alle acht Schlüssel/Salze müssen gesetzt sein — WordPress verlangt sie beim
// Start, ein fehlender ist kein "geht auch ohne".
func TestRenderWordPressConfigVerlangtAlleSchluessel(t *testing.T) {
	d := gueltigeWPConfig(t)
	d.NonceSalt = ""
	if _, err := RenderWordPressConfig(d); err == nil {
		t.Error("ein fehlendes salz wurde angenommen")
	}
}

// Ein Datenbankname oder -benutzer, der store.ValidDBName/-User nicht
// besteht, darf nicht in einer ausführbaren PHP-Datei landen — auch wenn die
// Werte in diesem Programm eigentlich immer schon vom Store geprüft wurden,
// bevor sie hier ankommen.
func TestRenderWordPressConfigPrueftDenNamenNocheinmal(t *testing.T) {
	d := gueltigeWPConfig(t)
	d.DBName = "'; DROP TABLE wp_users; --"
	if _, err := RenderWordPressConfig(d); err == nil {
		t.Error("ein ungültiger datenbankname wurde angenommen")
	}
}

// Zwei Aufrufe von NewWordPressSecrets müssen unterschiedliche Werte liefern
// — sonst würden alle WordPress-Installationen dieses Panels dieselben
// Cookie-Schlüssel teilen, und ein gestohlenes Cookie einer Installation
// gälte auch bei jeder anderen.
func TestNewWordPressSecretsIstJedesMalAnders(t *testing.T) {
	a, err := NewWordPressSecrets()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewWordPressSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if a.AuthKey == b.AuthKey {
		t.Error("zwei aufrufe ergeben denselben auth_key")
	}
}
