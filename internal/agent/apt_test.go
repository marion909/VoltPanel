package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// aptSeteuidFehler ist die Ausgabe von einem echten Server: Debian 13, der
// Agent als systemd-Dienst, `apt-get install pure-ftpd`. apt löst die
// Paketliste vollständig auf und bricht erst beim Download ab, weil ihm der
// Wechsel auf den Benutzer `_apt` verwehrt wird.
const aptSeteuidFehler = `Reading package lists...
Building dependency tree...
Reading state information...
The following additional packages will be installed:
  libevent-2.1-7t64 openbsd-inetd pure-ftpd-common tcpd update-inetd
The following NEW packages will be installed:
  libevent-2.1-7t64 openbsd-inetd pure-ftpd pure-ftpd-common tcpd update-inetd
0 upgraded, 6 newly installed, 0 to remove and 1 not upgraded.
Need to get 618 kB of archives.
After this operation, 2.155 kB of additional disk space will be used.
E: seteuid 42 failed - seteuid (1: Operation not permitted)`

// TestAptRechteabgabeWirdErkannt: nur dieser eine Abbruch darf den zweiten
// Versuch ohne Rechtetrennung auslösen.
//
// Eine Erkennung, die auch bei anderen Fehlern anspricht, wäre schlimmer als
// keine: jeder fehlgeschlagene Download liefe dann still als root nach.
func TestAptRechteabgabeWirdErkannt(t *testing.T) {
	if !aptSandboxBroken(aptSeteuidFehler) {
		t.Error("der seteuid-Abbruch wurde nicht erkannt")
	}

	andere := map[string]string{
		"platte voll":    "E: You don't have enough free space in /var/cache/apt/archives/.",
		"paket fehlt":    "E: Unable to locate package pure-ftpd",
		"lock":           "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 4711",
		"spiegel weg":    "E: Failed to fetch http://deb.debian.org/... Connection failed",
		"dpkg gebrochen": "E: Sub-process /usr/bin/dpkg returned an error code (1)",
		"leer":           "",
	}
	for name, out := range andere {
		if aptSandboxBroken(out) {
			t.Errorf("%s wurde als Rechteproblem gewertet — die Trennung fiele grundlos weg", name)
		}
	}
}

// TestAptFehlermeldungNenntDenGrund: die Meldung landet unverändert in der
// Oberfläche. Sie muss die Zeile enthalten, wegen der jemand sie liest.
func TestAptFehlermeldungNenntDenGrund(t *testing.T) {
	msg := aptMessage(aptSeteuidFehler)

	if !strings.Contains(msg, "seteuid 42 failed") {
		t.Errorf("der Grund fehlt in der Meldung: %q", msg)
	}
	// Das Vorgeplänkel hat in einer Fehlermeldung nichts verloren — genau das
	// stand in der ersten Fassung darin, und der Grund war abgeschnitten.
	if strings.Contains(msg, "Reading package lists") {
		t.Errorf("die Meldung beginnt mit Belanglosem: %q", msg)
	}
}

// TestAptAlterIndexWirdErkannt: ein unbekanntes Paket ist kein Tippfehler —
// die Namen stehen im Quelltext —, sondern ein Index, der zu alt ist.
func TestAptAlterIndexWirdErkannt(t *testing.T) {
	if !aptIndexStale("E: Unable to locate package php8.4-imagick") {
		t.Error("ein unbekanntes Paket löst kein apt-get update aus")
	}
	if !aptIndexStale("E: Package 'pure-ftpd' has no installation candidate") {
		t.Error("ein Paket ohne Kandidat löst kein apt-get update aus")
	}
	if aptIndexStale(aptSeteuidFehler) {
		t.Error("der seteuid-Abbruch löst ein sinnloses apt-get update aus")
	}
}

// TestAptFragtNieNach hält die Variable fest, ohne die ein Paket mit
// debconf-Dialog den Aufruf bis zum Timeout blockiert. pure-ftpd zieht mit
// openbsd-inetd genau so ein Paket mit.
//
// Der Wert stand schon einmal nur in runInto und fehlte in run — also in genau
// dem Weg, den jeder apt-Aufruf nimmt.
func TestAptFragtNieNach(t *testing.T) {
	var gefunden bool
	for _, e := range aptEnv {
		if e == "DEBIAN_FRONTEND=noninteractive" {
			gefunden = true
		}
	}
	if !gefunden {
		t.Errorf("DEBIAN_FRONTEND fehlt in aptEnv: %v", aptEnv)
	}

	// baseEnv bleibt frei davon: es gilt für jedes Kommando, nicht nur für apt.
	for _, e := range baseEnv() {
		if strings.HasPrefix(e, "DEBIAN_FRONTEND") {
			t.Error("DEBIAN_FRONTEND gehört zu apt, nicht in das Environment jedes Kommandos")
		}
	}
}

// TestTailBehaeltDasEnde prüft die Umkehrung von truncate — und dass sie nicht
// mitten in ein UTF-8-Zeichen schneidet.
func TestTailBehaeltDasEnde(t *testing.T) {
	if got := tail("kurz", 100); got != "kurz" {
		t.Errorf("kurzer Text wurde verändert: %q", got)
	}
	if got := tail("abcdefghij", 4); got != "…ghij" {
		t.Errorf("tail = %q, erwartet \"…ghij\"", got)
	}
	// "ä" belegt zwei Bytes. Bei n=3 landet der Schnitt zwischen ihnen, und ein
	// rohes s[len(s)-n:] lieferte ein halbes Zeichen — ungültiges UTF-8, das
	// weder in einer Fehlermeldung noch in JSON etwas zu suchen hat.
	got := tail("xxxäyz", 3)
	if !utf8.ValidString(got) {
		t.Errorf("tail hat ein Zeichen zerschnitten: %q", got)
	}
	if got != "…yz" {
		t.Errorf("tail = %q, erwartet \"…yz\"", got)
	}
}
