package agent

import (
	"strings"
	"testing"
)

// procMountsBeispiel ist eine gekürzte, aber echte /proc/mounts: eine Wurzel
// ohne Quota, ein eigenes /var/www mit, ein Pfad mit Leerzeichen und ein
// Dateisystem, das gar keine Project Quota kennt.
const procMountsBeispiel = `sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
/dev/sda2 / ext4 rw,relatime,errors=remount-ro 0 0
/dev/sda3 /var/www ext4 rw,relatime,prjquota 0 0
/dev/sdb1 /vartmp ext4 rw,relatime 0 0
/dev/sdc1 /mnt/mein\040platz xfs rw,relatime,prjquota 0 0
/dev/sdd1 /srv btrfs rw,relatime 0 0
tmpfs /run tmpfs rw,nosuid,nodev 0 0`

func mountsAusBeispiel(t *testing.T) []mountEntry {
	t.Helper()
	m := parseMounts(strings.NewReader(procMountsBeispiel))
	if len(m) != 7 {
		t.Fatalf("%d Einträge gelesen, erwartet 7", len(m))
	}
	return m
}

// TestEinhaengepunktMitLeerzeichen: /proc/mounts schreibt ein Leerzeichen als
// \040. Bleibt die Fluchtfolge stehen, findet der Pfadvergleich den Punkt nie —
// und die Quota landete auf dem darüberliegenden Dateisystem.
func TestEinhaengepunktMitLeerzeichen(t *testing.T) {
	mounts := mountsAusBeispiel(t)

	m, ok := mountFor("/mnt/mein platz/kunde.de", mounts)
	if !ok {
		t.Fatal("der Einhängepunkt wurde nicht gefunden")
	}
	if m.Point != "/mnt/mein platz" {
		t.Errorf("Punkt ist %q", m.Point)
	}
}

// TestLaengsterEinhaengepunktGewinnt ist der Kern von mountFor. Auf einem
// Server mit "/" und "/var/www" liegt /var/www/kunde.de auf dem zweiten. Wer
// den ersten Treffer nimmt, setzt die Quota auf dem falschen Dateisystem und
// wundert sich, dass nichts geschieht.
func TestLaengsterEinhaengepunktGewinnt(t *testing.T) {
	mounts := mountsAusBeispiel(t)

	cases := map[string]string{
		"/var/www/kunde.de":   "/var/www",
		"/var/www":            "/var/www",
		"/var/log/nginx":      "/",
		"/vartmp/x":           "/vartmp",
		"/srv/etwas":          "/srv",
		"/etc/nginx/nginx.co": "/",
	}
	for path, want := range cases {
		m, ok := mountFor(path, mounts)
		if !ok {
			t.Errorf("%s: kein Einhängepunkt gefunden", path)
			continue
		}
		if m.Point != want {
			t.Errorf("%s liegt laut mountFor auf %q, richtig ist %q", path, m.Point, want)
		}
	}
}

// TestPfadgrenzeWirdEingehalten: "/var" ist kein Einhängepunkt von "/vartmp".
// Ein reiner Präfixvergleich ohne Blick auf den Trenner sagt etwas anderes.
func TestPfadgrenzeWirdEingehalten(t *testing.T) {
	mounts := []mountEntry{
		{Point: "/", FSType: "ext4"},
		{Point: "/var", FSType: "ext4"},
	}
	m, ok := mountFor("/vartmp/x", mounts)
	if !ok {
		t.Fatal("kein Einhängepunkt gefunden")
	}
	if m.Point != "/" {
		t.Errorf("/vartmp/x liegt laut mountFor auf %q, richtig ist \"/\"", m.Point)
	}
}

func alleDa(string) bool  { return true }
func keineDa(string) bool { return false }

// TestQuotaBereitschaft prüft die Auskunft, die im Panel steht.
func TestQuotaBereitschaft(t *testing.T) {
	mounts := mountsAusBeispiel(t)

	bereit := quotaSupport("/var/www/kunde.de", mounts, alleDa)
	if !bereit.Ready {
		t.Errorf("/var/www ist mit prjquota eingehängt, gilt aber als nicht bereit: %+v", bereit)
	}
	if bereit.Hinweis != "" {
		t.Errorf("wo nichts zu tun ist, darf kein Hinweis stehen: %q", bereit.Hinweis)
	}

	ohneWerkzeug := quotaSupport("/var/www/kunde.de", mounts, keineDa)
	if ohneWerkzeug.Ready {
		t.Error("ohne setquota und chattr gilt der Server als bereit")
	}
	if !strings.Contains(ohneWerkzeug.Hinweis, "apt install") {
		t.Errorf("der Hinweis sagt nicht, was zu installieren ist: %q", ohneWerkzeug.Hinweis)
	}
}

// TestOhneMountOptionKeineQuota: die Option lässt sich nicht im Betrieb setzen,
// deshalb muss der Hinweis sagen, was wirklich zu tun ist — und für ext4 ist
// das etwas anderes als für XFS.
func TestOhneMountOptionKeineQuota(t *testing.T) {
	mounts := mountsAusBeispiel(t)

	ext4 := quotaSupport("/var/log/nginx", mounts, alleDa)
	if ext4.Ready {
		t.Error("die Wurzel ist ohne prjquota eingehängt, gilt aber als bereit")
	}
	if !strings.Contains(ext4.Hinweis, "tune2fs") || !strings.Contains(ext4.Hinweis, "prjquota") {
		t.Errorf("ext4-Hinweis nennt die nötigen Schritte nicht: %q", ext4.Hinweis)
	}

	// XFS auf der Wurzel: dort nimmt der Kernel die Option nur beim Booten.
	xfsRoot := quotaSupport("/etwas", []mountEntry{
		{Device: "/dev/sda1", Point: "/", FSType: "xfs", Opts: []string{"rw"}},
	}, alleDa)
	if !strings.Contains(xfsRoot.Hinweis, "rootflags") {
		t.Errorf("XFS-Wurzel ohne Hinweis auf die Kernel-Kommandozeile: %q", xfsRoot.Hinweis)
	}
}

// TestDateisystemOhneProjectQuota: btrfs kennt keine Project Quota. Das ist
// kein Fehler, aber es muss dastehen — sonst wartet jemand auf eine Grenze,
// die nie greift.
func TestDateisystemOhneProjectQuota(t *testing.T) {
	sup := quotaSupport("/srv/etwas", mountsAusBeispiel(t), alleDa)

	if sup.Ready {
		t.Error("btrfs gilt als bereit")
	}
	if !strings.Contains(sup.Hinweis, "btrfs") {
		t.Errorf("der Hinweis nennt das Dateisystem nicht: %q", sup.Hinweis)
	}
}

// TestZweiDateisystemeKeineGemeinsameGrenze: der Kernel kennt keine Grenze über
// zwei Dateisysteme hinweg. Sie auf beiden zu setzen gäbe dem Mandanten das
// Doppelte — und sähe im Panel aus, als täte sie es nicht.
func TestZweiDateisystemeKeineGemeinsameGrenze(t *testing.T) {
	mounts := mountsAusBeispiel(t)

	if _, err := singleMount([]string{"/var/www/a.de", "/var/www/b.de"}, mounts); err != nil {
		t.Errorf("zwei Verzeichnisse auf demselben Dateisystem wurden abgelehnt: %v", err)
	}

	_, err := singleMount([]string{"/var/www/a.de", "/srv/b.de"}, mounts)
	if err == nil {
		t.Fatal("zwei Dateisysteme wurden angenommen")
	}
	if !strings.Contains(err.Error(), "zwei dateisystemen") {
		t.Errorf("abgelehnt, aber aus dem falschen Grund: %v", err)
	}
}

// TestProjektnummerHaeltAbstandZuNull: Projekt 0 trägt jede Datei, die zu
// keinem Projekt gehört. Eine Grenze darauf träfe das halbe Dateisystem.
func TestProjektnummerHaeltAbstandZuNull(t *testing.T) {
	for _, tenant := range []int64{0, -1, -99999, quotaProjectMax} {
		if id, err := quotaProjectID(tenant); err == nil {
			t.Errorf("Mandant %d bekam die Projektnummer %d", tenant, id)
		}
	}
	id, err := quotaProjectID(1)
	if err != nil {
		t.Fatalf("Mandant 1: %v", err)
	}
	if id <= 0 {
		t.Errorf("Mandant 1 bekam die Projektnummer %d", id)
	}
	// Verschiedene Mandanten dürfen nie dieselbe Nummer bekommen — sonst
	// zählte der Verbrauch des einen auf die Grenze des anderen.
	zwei, _ := quotaProjectID(2)
	if zwei == id {
		t.Errorf("Mandant 1 und 2 teilen sich die Projektnummer %d", id)
	}
}

// TestQuotaPfadOhneTrennzeichen: xfs_quota nimmt seine eigenen Befehle als eine
// Zeichenkette hinter -c und zerlegt sie selbst an Leerzeichen. Ein Pfad, der
// diese Zerlegung verschiebt, darf dort nicht hineingeraten.
func TestQuotaPfadOhneTrennzeichen(t *testing.T) {
	gut := []string{"/var/www/kunde.de", "/var/www/a_b-c.de", "/srv"}
	for _, p := range gut {
		if !reQuotaArg.MatchString(p) {
			t.Errorf("%q wurde abgelehnt", p)
		}
	}
	schlecht := []string{
		"/var/www/mit platz",   // verschiebt die Zerlegung
		"/var/www/a\tb",        // ebenso
		"/var/www/a\nlimit -p", // hängt einen zweiten Befehl an
		"/var/www/x;y",         // sähe in einer Shell böse aus, hier auch
		"var/www/x",            // nicht absolut
		"/var/www/$(id)",       // keine Shell, aber nichts, was ein Pfad braucht
	}
	for _, p := range schlecht {
		if reQuotaArg.MatchString(p) {
			t.Errorf("%q ging durch", p)
		}
	}
}
