package agent

import (
	"os"
	"path/filepath"
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

// aptDpkgFehler ist die Form, in der eine Installation in dpkg scheitert.
// Die einzige E:-Zeile ist die Zusammenfassung; der Grund steht darüber.
const aptDpkgFehler = `Reading package lists...
Building dependency tree...
Setting up libevent-2.1-7t64:amd64 (2.1.12-stable-10) ...
Setting up openbsd-inetd (0.20221205-1+b1) ...
Job for openbsd-inetd.service failed because the control process exited with error code.
invoke-rc.d: initscript openbsd-inetd, action "start" failed.
dpkg: error processing package openbsd-inetd (--configure):
 installed openbsd-inetd package post-installation script subprocess returned error exit status 1
Errors were encountered while processing:
 openbsd-inetd
E: Sub-process /usr/bin/dpkg returned an error code (1)`

// TestAptMeldungNenntDpkgsGrund: dpkg schreibt seine Fehler ohne E: davor.
//
// Die erste Fassung filterte nur auf E:-Zeilen. Bei einem dpkg-Fehlschlag ist
// das genau eine Zeile — die nichtssagende Zusammenfassung —, und die
// Meldung im Panel lautete vollständig "E: Sub-process /usr/bin/dpkg returned
// an error code (1)". Damit lässt sich nichts anfangen.
func TestAptMeldungNenntDpkgsGrund(t *testing.T) {
	msg := aptMessage(aptDpkgFehler)

	müssen := []string{
		"dpkg: error processing package openbsd-inetd",
		"post-installation script subprocess returned error exit status 1",
		"invoke-rc.d",
	}
	for _, teil := range müssen {
		if !strings.Contains(msg, teil) {
			t.Errorf("%q fehlt in der Meldung: %q", teil, msg)
		}
	}
	if strings.Contains(msg, "Setting up libevent") {
		t.Errorf("die Meldung führt gelungene Schritte mit auf: %q", msg)
	}
}

// TestFremdePolicyBleibtUnangetastet ist die wichtigste Zusage rund um
// policy-rc.d.
//
// Die Datei entscheidet serverweit, ob Pakete beim Installieren ihre Dienste
// starten dürfen. Eine vorhandene kann eine bewusste Einstellung des
// Serverbetreibers sein — sie zu überschreiben oder am Ende wegzuräumen wäre
// ein stiller Eingriff in eine fremde Entscheidung.
func TestFremdePolicyBleibtUnangetastet(t *testing.T) {
	srv, _ := testServer(t)
	pfad := filepath.Join(t.TempDir(), "policy-rc.d")

	alt := policyPath
	policyPath = pfad
	t.Cleanup(func() { policyPath = alt })

	fremd := "#!/bin/sh\n# vom serverbetreiber\nexit 0\n"
	if err := os.WriteFile(pfad, []byte(fremd), 0o755); err != nil {
		t.Fatal(err)
	}

	zurück := srv.blockServiceStarts()
	inhalt, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatalf("die fremde Datei ist während der Installation verschwunden: %v", err)
	}
	if string(inhalt) != fremd {
		t.Errorf("die fremde Datei wurde überschrieben:\n%s", inhalt)
	}

	zurück()
	if inhalt, err := os.ReadFile(pfad); err != nil || string(inhalt) != fremd {
		t.Errorf("die fremde Datei wurde am Ende weggeräumt (err=%v)", err)
	}
}

// TestEigenePolicyKommtUndGeht: ohne vorhandene Datei legt der Agent seine an
// und nimmt sie wieder weg. Bliebe sie liegen, würde auf diesem Server kein
// Paket mehr seinen Dienst starten — und niemand wüsste, warum.
func TestEigenePolicyKommtUndGeht(t *testing.T) {
	srv, _ := testServer(t)
	pfad := filepath.Join(t.TempDir(), "policy-rc.d")

	alt := policyPath
	policyPath = pfad
	t.Cleanup(func() { policyPath = alt })

	zurück := srv.blockServiceStarts()

	inhalt, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatalf("policy-rc.d wurde nicht angelegt: %v", err)
	}
	// 101 ist der Wert, auf den invoke-rc.d hört. Ein anderer bewirkt nichts.
	if !strings.Contains(string(inhalt), "exit 101") {
		t.Errorf("policy-rc.d verhindert keinen Dienststart:\n%s", inhalt)
	}
	info, err := os.Stat(pfad)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("policy-rc.d ist nicht ausführbar (%v) — invoke-rc.d übergeht sie dann", err)
	}

	zurück()
	if _, err := os.Stat(pfad); !os.IsNotExist(err) {
		t.Errorf("policy-rc.d blieb liegen (err=%v)", err)
	}
}

// aptReadOnlyFehler ist die Ausgabe von einem echten Server: der Agent läuft
// mit ProtectSystem=true, /usr ist für ihn schreibgeschützt, und dpkg kann die
// Dateien des Pakets nicht auspacken.
const aptReadOnlyFehler = `Reading package lists...
Building dependency tree...
dpkg: error processing archive /tmp/apt-dpkg-install-5mPC5K/0-libevent-2.1-7t64_2.1.12-stable-10+b1_amd64.deb (--unpack):
 unable to create '/usr/lib/x86_64-linux-gnu/libevent-2.1.so.7.0.1.dpkg-new' (while processing './usr/lib/x86_64-linux-gnu/libevent-2.1.so.7.0.1'): Read-only file system
dpkg: error while cleaning up:
 unable to remove newly-extracted version of '/usr/lib/x86_64-linux-gnu/libevent-2.1.so.7.0.1': Read-only file system
Errors were encountered while processing:
 /tmp/apt-dpkg-install-5mPC5K/0-libevent-2.1-7t64_2.1.12-stable-10+b1_amd64.deb
E: Sub-process /usr/bin/dpkg returned an error code (1)`

// TestAptMeldungNenntDasSchreibverbot: dieser Fehler kam auf einem echten
// Server an, und ohne die erweiterte Filterung stand im Panel nur die
// nichtssagende dpkg-Zusammenfassung. Der Grund — "Read-only file system" —
// zeigt direkt auf ProtectSystem=true in der Unit des Agents.
func TestAptMeldungNenntDasSchreibverbot(t *testing.T) {
	msg := aptMessage(aptReadOnlyFehler)

	if !strings.Contains(msg, "Read-only file system") {
		t.Errorf("der Grund fehlt in der Meldung: %q", msg)
	}
	if !strings.Contains(msg, "/usr/lib") {
		t.Errorf("die Meldung nennt nicht, wohin nicht geschrieben werden konnte: %q", msg)
	}
}

// TestPaketinstallationLaeuftAusserhalbDerSandbox hält die Argumente fest, mit
// denen apt gestartet wird.
//
// --wait ist das entscheidende: ohne es kehrt systemd-run sofort zurück, der
// Agent hielte die Installation für erledigt, und die Schritte danach —
// Konfiguration schreiben, Dienst starten — liefen gegen ein Paket, das noch
// gar nicht ausgepackt ist.
func TestPaketinstallationLaeuftAusserhalbDerSandbox(t *testing.T) {
	if _, ok := allowedBinaries["systemd-run"]; !ok {
		t.Fatal("systemd-run steht nicht auf der Whitelist — apt liefe in der Sandbox")
	}

	// Der Pfad zu apt kommt aus der Whitelist, nicht aus einer Anfrage.
	if allowedBinaries["apt-get"] != "/usr/bin/apt-get" {
		t.Errorf("apt-get zeigt auf %q", allowedBinaries["apt-get"])
	}
}

func TestAptReinstallSetztReinstallVorDasPaket(t *testing.T) {
	srv, _ := testServer(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "apt.log")
	apt := filepath.Join(dir, "apt-get")
	skript := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + logPath + "\n"
	if err := os.WriteFile(apt, []byte(skript), 0o755); err != nil {
		t.Fatal(err)
	}

	altApt := allowedBinaries["apt-get"]
	altSystemdRun := allowedBinaries["systemd-run"]
	altPolicy := policyPath
	allowedBinaries["apt-get"] = apt
	allowedBinaries["systemd-run"] = filepath.Join(dir, "systemd-run-fehlt")
	policyPath = filepath.Join(dir, "policy-rc.d")
	t.Cleanup(func() {
		allowedBinaries["apt-get"] = altApt
		allowedBinaries["systemd-run"] = altSystemdRun
		policyPath = altPolicy
	})

	if _, err := srv.aptReinstall(t.Context(), "docker.io"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(got)
	if !strings.Contains(args, "--reinstall docker.io") {
		t.Fatalf("aptReinstall-Aufruf = %q", args)
	}
}

// TestSystemdRunAusfallWirdErkannt: nur ein nicht ansprechbares systemd darf
// den Rückfall auf den direkten Aufruf auslösen. Ein fehlgeschlagenes apt
// dagegen ist ein Ergebnis und kein Grund, es noch einmal anders zu versuchen.
func TestSystemdRunAusfallWirdErkannt(t *testing.T) {
	ausfall := []string{
		"Failed to connect to bus: No such file or directory",
		"System has not been booted with systemd as init system (PID 1). Can't operate.",
	}
	for _, out := range ausfall {
		if !systemdRunUnavailable(out) {
			t.Errorf("kein Rückfall bei %q", out)
		}
	}

	kein := []string{
		"E: Unable to locate package pure-ftpd",
		"E: Sub-process /usr/bin/dpkg returned an error code (1)",
		aptReadOnlyFehler,
		// Der wichtigste Fall: apt lief, konnte nur seine Rechte nicht
		// abgeben. Ein Rückfall auf den direkten Aufruf wäre hier falsch —
		// dafür gibt es aptSandboxBroken.
		aptSeteuidFehler,
		"",
	}
	for _, out := range kein {
		if systemdRunUnavailable(out) {
			t.Errorf("apt-Fehler wurde als systemd-Ausfall gewertet: %q", truncate(out, 60))
		}
	}
}
