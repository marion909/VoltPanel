package templates

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Eine App ist eine systemd-Unit plus Reverse-Proxy.
//
// Der heikle Teil ist nicht das Starten, sondern das Schreiben der Unit-Datei.
// Eine Unit ist zeilenweise aufgebaut: was einen Zeilenumbruch in einen Wert
// bekommt, schreibt die nächste Direktive selbst. `User=root` in einer Zeile,
// die als Kommandozeile gedacht war, und die App läuft als root.
//
// Deshalb geht hier nichts ungeprüft hinein — dieselbe Regel wie bei den
// Nginx-Configs, nur mit einer anderen Grammatik.

var (
	// reAppName: der Name wird ein Unit-Name und ein Dateiname.
	reAppName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

	// reExecArg lässt kein Leerzeichen durch, und das ist Absicht.
	//
	// systemd zerlegt ExecStart selbst, mit eigenen Anführungszeichen und
	// C-Fluchtfolgen. Ein Argument mit Leerzeichen zwänge dazu, diese
	// Zerlegung nachzubauen — und eine nachgebaute Zerlegung ist genau die
	// Stelle, an der solche Sachen schiefgehen. Ohne Leerzeichen gibt es
	// nichts nachzubauen.
	//
	// Auch kein Prozentzeichen: das leitet in einer Unit einen Platzhalter ein
	// (%n, %i, %h). "%h/bin/node" zeigte auf das Heimatverzeichnis, nicht auf
	// den gemeinten Pfad.
	reExecArg = regexp.MustCompile(`^[A-Za-z0-9_./:@=,+-]+$`)

	// reEnvKey ist die übliche Form eines Umgebungsnamens.
	reEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// EnvVar ist eine Umgebungsvariable der App.
type EnvVar struct {
	Key   string
	Value string
}

// AppData ist das Eingabemodell der Unit-Vorlage.
type AppData struct {
	// Name ohne Präfix und ohne .service — beides setzt UnitName().
	Name        string
	GeneratedAt string
	User        string
	Group       string
	WorkingDir  string
	EnvPath     string

	// Command ist das Programm mit seinen Argumenten, jedes für sich. Als
	// Liste, nicht als Zeichenkette: eine Zeichenkette müsste jemand zerlegen,
	// und wer zerlegt, kann sich vertun.
	Command []string

	Env []EnvVar
}

// appUnitPrefix hält die Units dieses Panels von allen anderen getrennt.
const appUnitPrefix = "volt-app-"

// UnitName ist der volle systemd-Name.
func UnitName(app string) string { return appUnitPrefix + app }

// AppNameFromUnit ist die Umkehrung, leer bei einem fremden Namen.
func AppNameFromUnit(unit string) string {
	unit = strings.TrimSuffix(unit, ".service")
	if !strings.HasPrefix(unit, appUnitPrefix) {
		return ""
	}
	return strings.TrimPrefix(unit, appUnitPrefix)
}

// ValidAppName prüft, was ein Unit- und Dateiname werden soll.
//
// Ein Name, der schon mit dem Präfix beginnt, wird abgelehnt. Gefährlich wäre
// er nicht — aus "volt-app-shop" würde "volt-app-volt-app-shop", also immer
// noch eine eigene Unit. Aber er legt nahe, dass der Aufrufer das Präfix selbst
// setzt, und das tut er nie. Ein Name, der eine falsche Erwartung weckt,
// gehört an genau der Stelle abgewiesen, an der die Erwartung entsteht.
func ValidAppName(name string) bool {
	return reAppName.MatchString(name) && !strings.HasPrefix(name, appUnitPrefix)
}

// RenderApp erzeugt die Unit-Datei.
func RenderApp(d AppData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}

	view := struct {
		AppData
		ExecStart string
	}{AppData: d, ExecStart: strings.Join(d.Command, " ")}
	view.Name = UnitName(d.Name)
	if view.Group == "" {
		view.Group = d.User
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "app.service.tmpl", view); err != nil {
		return "", fmt.Errorf("unit für %s: %w", d.Name, err)
	}
	return buf.String(), nil
}

// RenderAppEnv erzeugt die Umgebungsdatei.
//
// Eigene Datei statt Environment= in der Unit: Unit-Dateien sind für alle
// lesbar, und in einer App-Umgebung stehen regelmäßig Datenbankpasswörter.
//
// Sortiert, damit zweimal dasselbe Schreiben dieselbe Datei ergibt — sonst
// meldete jeder Durchlauf eine Änderung und startete die App neu.
func RenderAppEnv(d AppData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}

	env := append([]EnvVar(nil), d.Env...)
	sort.Slice(env, func(i, j int) bool { return env[i].Key < env[j].Key })

	var b strings.Builder
	b.WriteString("# Von VoltPanel erzeugt — nicht von Hand bearbeiten.\n")
	for _, e := range env {
		fmt.Fprintf(&b, "%s=%s\n", e.Key, quoteEnvValue(e.Value))
	}
	return b.String(), nil
}

// quoteEnvValue macht aus einem beliebigen Wert eine Zeile, die systemd wieder
// als genau diesen Wert liest.
//
// In Anführungszeichen, weil ein Wert Leerzeichen und Doppelkreuze enthalten
// darf — ein unquotiertes `#` beginnt sonst einen Kommentar. Backslash und
// Anführungszeichen werden verdoppelt bzw. geschützt: systemd löst innerhalb
// der Anführungszeichen C-Fluchtfolgen auf.
func quoteEnvValue(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(v) + `"`
}

func (d *AppData) validate() error {
	if !ValidAppName(d.Name) {
		return fmt.Errorf("app-name %q: 3 bis 32 zeichen, kleinbuchstaben, ziffern, bindestrich", d.Name)
	}
	if !reSystemUser.MatchString(d.User) {
		return fmt.Errorf("systembenutzer %q ist ungültig", d.User)
	}
	if d.Group != "" && !reSystemUser.MatchString(d.Group) {
		return fmt.Errorf("gruppe %q ist ungültig", d.Group)
	}
	for name, p := range map[string]string{
		"working_dir": d.WorkingDir,
		"env_path":    d.EnvPath,
	} {
		if err := checkPath(name, p); err != nil {
			return err
		}
	}

	if len(d.Command) == 0 {
		return fmt.Errorf("app %s hat kein startkommando", d.Name)
	}
	if len(d.Command) > 32 {
		return fmt.Errorf("app %s: höchstens 32 argumente", d.Name)
	}
	for i, arg := range d.Command {
		if !reExecArg.MatchString(arg) {
			return fmt.Errorf("argument %d von %s enthält zeichen, die in einer unit-datei "+
				"nicht sicher stehen können: %q", i+1, d.Name, arg)
		}
	}
	// Das Programm selbst absolut: ohne Pfad suchte systemd es nicht im PATH,
	// sondern lehnte die Unit ab — und mit einem relativen Pfad hinge es am
	// Arbeitsverzeichnis, das jemand anderes bestimmt.
	if !strings.HasPrefix(d.Command[0], "/") {
		return fmt.Errorf("das startkommando von %s muss ein absoluter pfad sein, nicht %q",
			d.Name, d.Command[0])
	}

	if len(d.Env) > 200 {
		return fmt.Errorf("app %s: höchstens 200 umgebungsvariablen", d.Name)
	}
	gesehen := make(map[string]bool, len(d.Env))
	for _, e := range d.Env {
		if !reEnvKey.MatchString(e.Key) {
			return fmt.Errorf("umgebungsname %q ist ungültig", e.Key)
		}
		if gesehen[e.Key] {
			return fmt.Errorf("umgebungsname %q steht zweimal", e.Key)
		}
		gesehen[e.Key] = true
		// Ein Zeilenumbruch im Wert schriebe die nächste Zeile der
		// Umgebungsdatei selbst — und damit eine Variable, die niemand
		// gesetzt hat.
		if strings.ContainsAny(e.Value, "\n\r\x00") {
			return fmt.Errorf("der wert von %s enthält einen zeilenumbruch", e.Key)
		}
		if len(e.Value) > 4096 {
			return fmt.Errorf("der wert von %s ist länger als 4096 zeichen", e.Key)
		}
	}
	return nil
}
