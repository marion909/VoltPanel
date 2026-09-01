package agent

import (
	"context"
	"os"
	"strings"
	"unicode/utf8"
)

// Paketinstallation über apt.
//
// Derselbe Aufruf, der in einer Root-Shell durchläuft, scheitert aus einem
// systemd-Dienst heraus an Dingen, die mit dem Paket nichts zu tun haben. Diese
// Datei sammelt sie an einer Stelle, damit nicht jede Operation, die etwas
// nachinstalliert, dieselben Fallen einzeln wieder aufstellt.

// aptEnv ergänzt das feste Minimum aus baseEnv.
//
// Ohne DEBIAN_FRONTEND öffnet debconf für Pakete wie openbsd-inetd — das kommt
// mit pure-ftpd mit — einen Dialog. In einem Dienst ohne Terminal wartet der,
// bis der Timeout zuschlägt: zehn Minuten Stillstand und danach eine halb
// entpackte Installation.
var aptEnv = []string{"DEBIAN_FRONTEND=noninteractive"}

// aptInstall holt Pakete nach: einmal versuchen, und wenn der Paketindex zu alt
// ist, nach einem `apt-get update` ein zweites Mal.
//
// Das Update steht bewusst nicht davor. Es kostet auf einem trägen Spiegel
// leicht eine halbe Minute, und in den allermeisten Fällen ist der Index frisch
// genug — der Installer hat ihn beim Einrichten des Servers geholt.
func (s *Server) aptInstall(ctx context.Context, packages ...string) (string, error) {
	defer s.blockServiceStarts()()

	args := append([]string{
		"install", "-y", "--no-install-recommends",
		// Fragt dpkg zu einer bestehenden Konfigurationsdatei, bleibt die
		// vorhandene liegen. Ohne diese Angabe hinge der Aufruf an der Frage,
		// und die Antwort wäre ohnehin immer dieselbe.
		"-o", "Dpkg::Options::=--force-confold",
	}, packages...)

	out, err := s.aptRun(ctx, args...)
	if err == nil || !aptIndexStale(out) {
		return out, err
	}

	s.log.Info("paket nicht im index — apt-get update", "pakete", strings.Join(packages, " "))
	if _, updateErr := s.aptRun(ctx, "update"); updateErr != nil {
		// Der erste Fehler nennt das fehlende Paket und ist damit der
		// brauchbarere von beiden.
		return out, err
	}
	return s.aptRun(ctx, args...)
}

// policyPath ist der Weg, auf dem Debian einem Paket sagt, dass es seinen
// Dienst beim Installieren nicht starten soll: invoke-rc.d ruft dieses Skript
// und bricht bei Rückgabewert 101 ab.
//
// Als Variable, damit der Test das Verhalten an einer echten Datei prüfen kann
// statt an einer Nachbildung. Verändert wird sie ausschliesslich dort.
var policyPath = "/usr/sbin/policy-rc.d"

// policyMarker steht in der Datei, damit eine liegengebliebene eigene von einer
// fremden zu unterscheiden ist. Eine fremde wird nie angefasst.
const policyMarker = "# von volt-agent, nur waehrend einer paketinstallation"

const policyBody = "#!/bin/sh\n" + policyMarker + "\nexit 101\n"

// blockServiceStarts verhindert, dass Paket-Postinstalls Dienste hochfahren,
// und gibt die Funktion zurück, die das wieder zurücknimmt.
//
// Der Grund ist ein konkreter Fehlschlag: pure-ftpd hängt zwingend an
// openbsd-inetd, und dessen Postinst startet inetd. Gelingt das nicht — in
// einem Container etwa, oder weil Port 21 schon belegt ist —, gibt dpkg einen
// Fehler zurück, und apt bricht die ganze Installation ab. Dabei braucht
// VoltPanel inetd überhaupt nicht: Pure-FTPd läuft hier als eigener Dienst.
//
// Der Agent startet jeden Dienst, den er wirklich braucht, danach selbst und
// ausdrücklich. Was ein Paket beim Auspacken hochfahren möchte, ist für ihn
// ohnehin nie die Entscheidung gewesen.
//
// Eine fremde policy-rc.d bleibt unangetastet — sie kann eine bewusste
// Einstellung des Serverbetreibers sein.
func (s *Server) blockServiceStarts() func() {
	if data, err := os.ReadFile(policyPath); err == nil {
		if !strings.Contains(string(data), policyMarker) {
			s.log.Info("policy-rc.d liegt bereits vor und bleibt unverändert")
			return func() {}
		}
		// Eine eigene von einem abgebrochenen Lauf. Sie darf weg.
		s.log.Warn("liegengebliebene policy-rc.d vom letzten mal wird ersetzt")
	}

	if err := os.WriteFile(policyPath, []byte(policyBody), 0o755); err != nil {
		// Kein Grund abzubrechen: ohne die Datei läuft die Installation wie
		// vorher, nur eben mit dem Risiko oben.
		s.log.Warn("policy-rc.d nicht schreibbar", "err", err)
		return func() {}
	}
	return func() {
		if err := os.Remove(policyPath); err != nil && !os.IsNotExist(err) {
			s.log.Error("policy-rc.d nicht entfernt — dienste starten sonst bei "+
				"jeder paketinstallation nicht mehr", "pfad", policyPath, "err", err)
		}
	}
}

// packageInstalled fragt dpkg, ob genau dieses Paket ausgepackt und
// konfiguriert ist.
//
// Nötig, weil apt auch dann einen Fehler zurückgibt, wenn nur das Postinstall
// eines abhängigen Pakets gescheitert ist. Ob das gewünschte Paket dabei
// heil geblieben ist, sagt allein dpkg.
func packageInstalled(ctx context.Context, name string) bool {
	out, err := run(ctx, shortTimeout, "dpkg-query", "-W", "-f", "${Status}", name)
	return err == nil && strings.TrimSpace(out) == "install ok installed"
}

// aptRun führt apt aus und wiederholt den Aufruf, wenn apt seine eigenen Rechte
// nicht abgeben konnte.
//
// Zum Herunterladen wechselt apt auf den unprivilegierten Benutzer `_apt`.
// Dieser Wechsel braucht CAP_SETUID. Ein systemd-Dienst kann diese Fähigkeit
// verloren haben, ohne dass es irgendwo aufgefallen wäre — der Agent selbst
// braucht sie sonst nirgends. apt bricht dann mit
//
//	E: seteuid 42 failed - seteuid (1: Operation not permitted)
//
// ab, nachdem es die Paketliste schon aufgelöst hat, weshalb die Meldung
// aussieht, als hätte es fast geklappt.
//
// APT::Sandbox::User=root ist der von apt selbst vorgesehene Ausweg für
// Umgebungen, in denen der Wechsel nicht möglich ist. Er kostet die Trennung
// beim Download: der HTTP-Teil von apt liest die Antworten des Spiegels dann
// als root statt als `_apt`. Deshalb steht er nicht pauschal in jedem Aufruf,
// sondern nur im zweiten Versuch — solange der Wechsel funktioniert, bleibt er
// unangetastet.
func (s *Server) aptRun(ctx context.Context, args ...string) (string, error) {
	out, err := runEnv(ctx, aptTimeout, aptEnv, "apt-get", args...)
	if err == nil || !aptSandboxBroken(out) {
		return out, err
	}

	s.log.Warn("apt kann seine rechte nicht abgeben, download läuft als root",
		"grund", "dem dienst fehlt CAP_SETUID",
		"apt", aptMessage(out))

	return runEnv(ctx, aptTimeout, aptEnv, "apt-get",
		append([]string{"-o", "APT::Sandbox::User=root"}, args...)...)
}

// aptSandboxBroken erkennt genau den Abbruch oben — nicht „irgendein Fehler“.
// Ein zweiter Versuch mit abgeschalteter Trennung soll nur dort laufen, wo
// gerade sie das Problem war.
func aptSandboxBroken(out string) bool {
	low := strings.ToLower(out)
	if !strings.Contains(low, "failed") {
		return false
	}
	for _, call := range []string{"seteuid", "setegid", "setgroups"} {
		if strings.Contains(low, call) {
			return true
		}
	}
	return false
}

// aptIndexStale erkennt ein Paket, das apt nicht kennt. Das ist fast immer ein
// alter Index und keine Falscheingabe: die Paketnamen stehen im Quelltext.
func aptIndexStale(out string) bool {
	low := strings.ToLower(out)
	for _, phrase := range []string{
		"unable to locate package",
		"has no installation candidate",
		"couldn't find any package",
	} {
		if strings.Contains(low, phrase) {
			return true
		}
	}
	return false
}

// aptMessage zieht aus der Ausgabe das heraus, was den Fehler erklärt.
//
// apt schreibt erst seitenweise Belangloses („Reading package lists…“) und
// nennt den Grund am Ende. Ein Abschneiden von vorne trifft deshalb genau die
// Zeile, wegen der jemand die Meldung überhaupt liest — in der ersten Fassung
// endete die Fehlermeldung mitten in „After this opera…“.
//
// Nur auf E:-Zeilen zu filtern war aber zu eng, und zwar an der Stelle, an der
// es am meisten weh tut: scheitert die Installation in dpkg, ist die einzige
// E:-Zeile die nichtssagende Zusammenfassung
//
//	E: Sub-process /usr/bin/dpkg returned an error code (1)
//
// Der Grund steht darüber, in Zeilen ohne Präfix — „dpkg: error processing
// package …“ und die eingerückte Zeile darunter. Deshalb sammelt diese
// Funktion beides ein.
func aptMessage(out string) string {
	var problems []string
	var folgt bool

	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			folgt = false
			continue
		}

		if aptProblemLine(line) {
			problems = append(problems, line)
			// Auf „dpkg: error processing“ und „Errors were encountered“
			// folgt eingerückt die eigentliche Begründung.
			folgt = strings.Contains(line, "dpkg: error") ||
				strings.HasPrefix(line, "Errors were encountered")
			continue
		}
		// Eingerückte Fortsetzung einer Problemzeile.
		if folgt && strings.HasPrefix(raw, " ") {
			problems = append(problems, line)
			continue
		}
		folgt = false
	}

	if len(problems) > 0 {
		return truncate(strings.Join(problems, " · "), 700)
	}
	return tail(out, 500)
}

// aptProblemLine erkennt die Zeilen, die einen Fehler benennen.
//
// apt, dpkg und invoke-rc.d schreiben jeweils in ihrer eigenen Form; keine
// davon ist die der anderen.
func aptProblemLine(line string) bool {
	if strings.HasPrefix(line, "E:") || strings.HasPrefix(line, "W:") {
		return true
	}
	for _, marker := range []string{
		"dpkg: error",
		"dpkg: warning",
		"invoke-rc.d:",
		"Job for ",
		"Errors were encountered",
	} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// tail ist truncate von hinten: es behält das Ende und schneidet den Anfang ab.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	// Nicht mitten in ein UTF-8-Zeichen hinein schneiden.
	for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
		cut = cut[1:]
	}
	return "…" + cut
}
