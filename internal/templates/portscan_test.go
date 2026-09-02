package templates

import (
	"strings"
	"testing"
)

// Die Stufe entscheidet, wie schnell gesperrt wird. Sie kommt aus einer festen
// Liste — ein Zahlenfeld ließe sich so einstellen, dass der Schutz zur
// Selbstsperre wird.
func TestRenderPortScanNimmtNurBekannteStufen(t *testing.T) {
	for _, l := range PortScanLevels() {
		if _, _, err := RenderPortScan(l, "/var/log/ufw.log", nil); err != nil {
			t.Errorf("%s: %v", l, err)
		}
	}
	for _, l := range []PortScanLevel{"", "aggressiv", "STRENG", "normal ", "0"} {
		if _, _, err := RenderPortScan(l, "/var/log/ufw.log", nil); err == nil {
			t.Errorf("%q wurde als stufe angenommen", l)
		}
	}
}

// Der Pfad landet in einer Datei, die fail2ban einliest. Er kommt zwar aus dem
// Agent und nicht von außen — geprüft wird er trotzdem.
func TestRenderPortScanPrueftDenPfad(t *testing.T) {
	schlecht := []string{
		"",
		"var/log/ufw.log",
		"/var/log/ufw.log\nbantime = 1s",
		"/var/log/../../etc/shadow ; rm -rf /",
		"/var/log/$(id).log",
	}
	for _, p := range schlecht {
		if _, _, err := RenderPortScan(PortScanNormal, p, nil); err == nil {
			t.Errorf("%q wurde als pfad angenommen", p)
		}
	}
}

// Die Whitelist steht in einer Datei, die fail2ban einliest — und eine Zeile
// dort kann mehr als eine Adresse sein.
//
// "1.2.3.4\nbantime = 1s" wäre in der ignoreip-Zeile nicht bloß wirkungslos:
// der Umbruch beendet die Direktive, und was danach steht, ist die nächste.
// Genau derselbe Mechanismus wie bei einer systemd-Unit.
func TestRenderPortScanLaesstNurAdressenInDieWhitelist(t *testing.T) {
	_, jail, err := RenderPortScan(PortScanNormal, "/var/log/ufw.log", []string{
		"203.0.113.7",
		"10.0.0.0/8",
		"2001:db8::/32",
		"203.0.113.7", // doppelt
		"1.2.3.4\nbantime = 1s",
		"1.2.3.4 bantime = 1s",
		"nicht-eine-adresse",
		"",
		"999.999.999.999",
	})
	if err != nil {
		t.Fatal(err)
	}

	var zeile string
	for _, z := range strings.Split(jail, "\n") {
		if strings.HasPrefix(z, "ignoreip") {
			zeile = z
		}
	}
	if zeile == "" {
		t.Fatal("in der jail-datei steht keine ignoreip-zeile")
	}

	for _, will := range []string{"127.0.0.1/8", "::1", "203.0.113.7", "10.0.0.0/8", "2001:db8::/32"} {
		if !strings.Contains(zeile, will) {
			t.Errorf("%s fehlt in %q", will, zeile)
		}
	}
	if strings.Count(zeile, "203.0.113.7") != 1 {
		t.Errorf("die doppelte adresse steht zweimal drin: %q", zeile)
	}
	for _, darfNicht := range []string{"bantime = 1s", "nicht-eine-adresse", "999.999"} {
		if strings.Contains(jail, darfNicht) {
			t.Errorf("%q steht in der jail-datei:\n%s", darfNicht, jail)
		}
	}
}

// Die Stufe bestimmt die Zahlen, und die Zahlen stehen in der Datei.
func TestRenderPortScanSchreibtDieZahlenDerStufe(t *testing.T) {
	_, streng, err := RenderPortScan(PortScanStreng, "/var/log/kern.log", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, will := range []string{
		"maxretry = 3", "findtime = 5m", "bantime  = 24h",
		"logpath  = /var/log/kern.log",
		"# Empfindlichkeit: streng",
	} {
		if !strings.Contains(streng, will) {
			t.Errorf("%q fehlt:\n%s", will, streng)
		}
	}

	_, vorsichtig, err := RenderPortScan(PortScanVorsichtig, "/var/log/ufw.log", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(vorsichtig, "maxretry = 3") {
		t.Error("vorsichtig und streng schreiben dieselbe zahl")
	}
}
