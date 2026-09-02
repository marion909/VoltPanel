// Package dockerspec prüft, was von einem Kunden kommt und später Teil einer
// docker-Kommandozeile wird.
//
// Eigenes Paket aus demselben Grund wie gitspec: Store und Agent brauchen
// dieselbe Prüfung — der eine beim Speichern, der andere unmittelbar vor dem
// Aufruf —, und importieren können sie einander nicht, ohne einen Zyklus zu
// bilden.
package dockerspec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ErrInvalid trägt jede Ablehnung wegen der Form.
	ErrInvalid = errors.New("ungültige eingabe")
	// ErrNotAllowed trägt die Ablehnungen, die nicht die Form betreffen,
	// sondern das Ziel: ein Pfad, den ein Container nicht überdecken soll.
	ErrNotAllowed = errors.New("nicht erlaubt")
)

// Docker.
//
// Ein Container ist kein Sandkasten, sondern ein Prozess mit anderen Namensräumen
// — und eine Handvoll Schalter hebt jede Trennung auf, die dieses Panel aufbaut:
//
//	--privileged            alle Capabilities, alle Geräte. Root auf dem Wirt.
//	-v /:/host              das Dateisystem des Servers im Container.
//	-v /var/run/docker.sock der Weg, einen zweiten Container mit --privileged
//	                        zu starten — also dasselbe eine Ebene tiefer.
//	--pid=host              die Prozesse des Wirts, samt /proc/1/root.
//	--net=host              alles, was auf 127.0.0.1 horcht, ist erreichbar.
//	--cap-add SYS_ADMIN     reicht allein für einen Ausbruch.
//	--device /dev/sda       die Platte, roh.
//	--user 0                root im Container, und bei einem Bind-Mount auch
//	                        root auf den Dateien des Wirts.
//
// Deshalb gibt es hier keine Schalterliste, die geprüft würde. Der Aufrufer
// beschreibt, was er will; die Kommandozeile baut der Agent, und sie enthält
// genau die Schalter, die hier im Quelltext stehen. Was nicht vorgesehen ist,
// lässt sich nicht anfordern — auch nicht mit einem findigen Wert.

var (
	// reImageRepo ist der Teil vor Tag und Digest: optional eine Registry mit
	// Port, dann der Pfad.
	//
	// Der führende Buchstabe oder die Ziffer ist die eine Zeile, die verhindert,
	// dass ein "Image" namens "--privileged" als Schalter gelesen wird.
	reImageRepo = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,199}$`)

	// reImageRegistry ist ein Registry-Host mit Port: registry.example.at:5000.
	reImageRegistry = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,251}:[0-9]{1,5}$`)

	// reImageTag ist ein Tag.
	reImageTag = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

	// reImageDigest ist die Prüfsumme, die stabilste Art, ein Image zu benennen.
	reImageDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

	// reContainerPath ist ein Pfad *im* Container. Absolut, ohne Doppelpunkt —
	// der trennt in -v die drei Felder, und ein Doppelpunkt im Pfad verschöbe
	// die Trennung.
	reContainerPath = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9._/-]{0,200}$`)

	// reRelPath ist ein Pfad *unterhalb* der Site-Wurzel. Der absolute bildet
	// sich daraus im Agent; er kommt nie aus der Anfrage.
	reRelPath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,200}$`)

	// reCPUs ist eine Zahl wie "0.5" oder "2".
	reCPUs = regexp.MustCompile(`^[0-9]{1,2}(\.[0-9]{1,2})?$`)
)

// containerPrefix hält die Container dieses Panels von allen anderen getrennt.
//
// Dasselbe wie bei den systemd-Units: ohne das Präfix wäre "meinen Container
// anhalten" ein Weg, jeden Container des Servers anzuhalten — auch den, in dem
// jemand anderes seine Datenbank betreibt.
const containerPrefix = "volt-"

// ContainerName bildet den Docker-Namen aus dem geprüften Site-Namen.
func ContainerName(name string) string { return containerPrefix + name }

// ContainerNameOwned sagt, ob ein Name zu diesem Panel gehört.
func ContainerNameOwned(name string) bool {
	rest := strings.TrimPrefix(name, containerPrefix)
	return rest != name && reAppNameOnly.MatchString(rest)
}

// reAppNameOnly ist dasselbe Muster wie für App- und Unit-Namen.
var reAppNameOnly = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

// ValidImage prüft einen Image-Namen.
func ValidImage(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("%w: kein image angegeben", ErrInvalid)
	case len(s) > 300:
		return fmt.Errorf("%w: der image-name ist zu lang", ErrInvalid)
	case strings.ContainsAny(s, " \t\n\r\x00'\";|&$`<>()"):
		return fmt.Errorf("%w: der image-name enthält zeichen, die dort nicht vorkommen",
			ErrInvalid)
	case strings.HasPrefix(s, "-"):
		// docker läse das als Schalter, nicht als Image.
		//
		// Wie in gitspec ist das die zweite Schranke, nicht die erste: ein
		// solcher Name scheitert ohnehin am Muster weiter unten. Sie steht
		// hier, weil die Meldung dann sagt, woran es wirklich liegt — und
		// weil eine spätere Lockerung des Musters sie nicht mitnehmen soll.
		return fmt.Errorf("%w: ein image-name beginnt nicht mit einem bindestrich", ErrInvalid)
	}
	return checkImageParts(s)
}

// checkImageParts zerlegt einen Image-Namen so, wie Docker ihn liest.
//
// Der Doppelpunkt ist mehrdeutig: in "registry.example.at:5000/x/y" trennt er
// den Port der Registry, in "nginx:1.27" den Tag. Docker unterscheidet an der
// Stelle — was nach dem letzten Schrägstrich kommt, ist der Tag. Ein Ausdruck,
// der beides in einem Muster versucht, wird entweder zu großzügig oder lehnt
// gültige Namen ab; das zweite war hier zuerst der Fall.
func checkImageParts(s string) error {
	// Digest zuerst: er hängt hinten und enthält selbst einen Doppelpunkt.
	if repo, digest, ok := strings.Cut(s, "@"); ok {
		if !reImageDigest.MatchString(digest) {
			return fmt.Errorf("%w: %q ist keine sha256-prüfsumme", ErrInvalid, digest)
		}
		s = repo
	}

	// Ein Doppelpunkt nach dem letzten Schrägstrich trennt den Tag ab.
	rest := s
	if i := strings.LastIndex(s, "/"); i >= 0 {
		rest = s[i+1:]
	}
	if j := strings.LastIndex(rest, ":"); j >= 0 {
		tag := rest[j+1:]
		if !reImageTag.MatchString(tag) {
			return fmt.Errorf("%w: %q ist kein tag", ErrInvalid, tag)
		}
		s = s[:len(s)-len(tag)-1]
	}

	// Was übrig ist, ist der Pfad — mit einer Registry davor, die einen Port
	// tragen darf.
	if i := strings.Index(s, "/"); i > 0 && reImageRegistry.MatchString(s[:i]) {
		s = s[i+1:]
		if s == "" {
			return fmt.Errorf("%w: nach der registry fehlt der image-name", ErrInvalid)
		}
	}
	if !reImageRepo.MatchString(s) {
		return fmt.Errorf("%w: %q ist kein image-name", ErrInvalid, s)
	}
	return nil
}

// checkContainerPath prüft einen Pfad im Container.
func CheckContainerPath(p string) error {
	switch {
	case !reContainerPath.MatchString(p):
		return fmt.Errorf("%w: %q ist kein pfad im container", ErrInvalid, p)
	case strings.Contains(p, ".."):
		return fmt.Errorf("%w: der pfad darf kein .. enthalten", ErrInvalid)
	}
	// Ziele, die nichts überdecken sollen. /proc und /sys sind die Sicht auf
	// den Kern, /dev ist Hardware, und unter /run liegen die Sockets der
	// Dienste — ein Verzeichnis darüber zu legen macht den Container nicht
	// unsicherer für den Wirt, aber unerklärlich kaputt.
	//
	// Der klassische Ausbruch — den Docker-Socket des Wirts hineinreichen —
	// ist auf diesem Weg ohnehin nicht möglich: die Quelle eines Volumes ist
	// immer relativ zur Wurzel der Site und kann den Wirt gar nicht benennen.
	for _, tabu := range []string{"/proc", "/sys", "/dev", "/run", "/var/run",
		"/etc/shadow", "/etc/passwd"} {
		if p == tabu || strings.HasPrefix(p, tabu+"/") {
			return fmt.Errorf("%w: %q darf im container nicht überdeckt werden", ErrNotAllowed, p)
		}
	}
	return nil
}

// checkRelPath prüft einen Pfad unterhalb der Site-Wurzel.
//
// Nur relativ: der absolute entsteht im Agent aus der Wurzel der Site. Käme er
// aus der Anfrage, wäre ein Volume der kürzeste Weg, das Dateisystem des
// Servers in einen Container zu hängen — und damit an alles heranzukommen, was
// dieses Panel trennt.
func CheckRelPath(p string) error {
	switch {
	case !reRelPath.MatchString(p):
		return fmt.Errorf("%w: %q ist kein zulässiger pfad in der site", ErrInvalid, p)
	case strings.Contains(p, ".."):
		return fmt.Errorf("%w: der pfad darf kein .. enthalten", ErrInvalid)
	}
	return nil
}

// ValidCPUs prüft eine CPU-Angabe wie "0.5" oder "2".
func ValidCPUs(s string) bool { return reCPUs.MatchString(s) }

// CheckVolume prüft ein Volume in einem Zug: Quelle relativ zur Site-Wurzel,
// Ziel absolut im Container.
func CheckVolume(source, target string) error {
	if err := CheckRelPath(source); err != nil {
		return err
	}
	return CheckContainerPath(target)
}
