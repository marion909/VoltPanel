package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// cronDir ist das Verzeichnis für einzelne Job-Dateien. Eine Datei je Job statt
// einer gemeinsamen crontab: so kann ein fehlerhafter Job die anderen nicht
// mitreißen, und Anlegen wie Entfernen bleibt ein Dateivorgang.
const cronDir = "/etc/cron.d"

// Der Dateiname muss den Regeln von cron.d folgen: Punkte und Bindestriche
// werden dort ignoriert, solche Dateien liefe nie.
var reCronFile = regexp.MustCompile(`^[a-z0-9_]{3,64}$`)

func (s *Server) opCronWrite(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[CronParams](raw, OpCronWrite)
	if err != nil {
		return nil, err
	}
	if !reCronFile.MatchString(p.Name) {
		return nil, opErr(OpCronWrite, "jobname %q: erlaubt sind 3–64 zeichen a-z, 0-9 und unterstrich", p.Name)
	}
	// siteUserIDs statt checkUsername: wie ops_app.go, ops_ftp.go, ops_deploy.go
	// und ops_docker.go es für vergleichbare Felder tun. checkUsername allein
	// verbietet nur die reservierten Systemkonten (root, www-data, …), lässt
	// aber jeden anderen existierenden Benutzer durch — ein Cronjob ließe sich
	// damit dauerhaft unter der Identität eines beliebigen anderen Kontos
	// einrichten, auch dem Systembenutzer einer fremden Site.
	if _, _, err := siteUserIDs(OpCronWrite, p.RunAs); err != nil {
		return nil, err
	}
	// Zeitplan und Kommando kommen bereits geprüft aus dem Store. Hier wird
	// noch einmal auf Zeilenumbrüche geschaut: eine zweite Zeile in dieser
	// Datei wäre ein zweiter, ungeprüfter Job.
	for name, value := range map[string]string{"zeitplan": p.Schedule, "kommando": p.Command} {
		if strings.ContainsAny(value, "\n\r\x00") {
			return nil, opErr(OpCronWrite, "%s darf nur eine zeile sein", name)
		}
	}
	if strings.TrimSpace(p.Schedule) == "" || strings.TrimSpace(p.Command) == "" {
		return nil, opErr(OpCronWrite, "zeitplan und kommando dürfen nicht leer sein")
	}

	path := filepath.Join(cronDir, p.Name)
	if _, err := jail(path, []string{cronDir}); err != nil {
		return nil, err
	}

	logPath := filepath.Join(s.logDir, "cron", p.Name+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, opErr(OpCronWrite, "log-verzeichnis: %v", err)
	}

	command := escapeCronPercent(p.Command)

	// Der Job schreibt seine Ausgabe in eine eigene Datei; ohne das ginge sie
	// an die lokale Mail des Benutzers, die auf einem Hosting-Server niemand liest.
	content := fmt.Sprintf(`# Von VoltPanel generiert — nicht von Hand bearbeiten.
SHELL=/bin/sh
PATH=/usr/local/sbin:/usr/local/bin:/sbin:/bin:/usr/sbin:/usr/bin
MAILTO=""

%s %s %s >> %s 2>&1
`, p.Schedule, p.RunAs, command, logPath)

	if err := writeFileAtomic(path, []byte(content), 0o644); err != nil {
		return nil, opErr(OpCronWrite, "%v", err)
	}
	// cron.d verlangt root als Eigentümer; sonst wird die Datei ignoriert.
	if err := os.Chown(path, 0, 0); err != nil {
		return nil, opErr(OpCronWrite, "eigentümer setzen: %v", err)
	}
	return TextResult{Text: "cronjob " + p.Name + " geschrieben"}, nil
}

// escapeCronPercent maskiert '%' im Cron-Kommandofeld.
//
// cron(8) wandelt jedes unmaskierte '%' im Kommandofeld in einen
// Zeilenumbruch um und leitet den Rest als Stdin an das Kommando weiter — ein
// '%' in z. B. einer URL veränderte damit unbemerkt das Verhalten, ohne dass
// die Zeilenumbruch-Prüfung in opCronWrite (nur \n, \r, \x00) das bemerkt
// hätte. Maskiert statt abgelehnt: das Kommando läuft dann wie eingegeben,
// statt dass ein legitimes '%' im Text den Job unbrauchbar macht.
func escapeCronPercent(command string) string {
	return strings.ReplaceAll(command, "%", `\%`)
}

func (s *Server) opCronRemove(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[CronParams](raw, OpCronRemove)
	if err != nil {
		return nil, err
	}
	if !reCronFile.MatchString(p.Name) {
		return nil, opErr(OpCronRemove, "jobname %q ist ungültig", p.Name)
	}

	path := filepath.Join(cronDir, p.Name)
	if _, err := jail(path, []string{cronDir}); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, opErr(OpCronRemove, "%v", err)
	}
	return TextResult{Text: "cronjob " + p.Name + " entfernt"}, nil
}

// opCronLog liefert die Ausgabe der letzten Läufe.
func (s *Server) opCronLog(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[CronParams](raw, OpCronLog)
	if err != nil {
		return nil, err
	}
	if !reCronFile.MatchString(p.Name) {
		return nil, opErr(OpCronLog, "jobname %q ist ungültig", p.Name)
	}

	// Der Pfad wird aus dem Namen gebildet, nicht übernommen — über diesen
	// Endpunkt lässt sich damit keine beliebige Datei lesen.
	path := filepath.Join(s.logDir, "cron", p.Name+".log")
	tail, err := json.Marshal(TailParams{Path: path, Lines: p.Lines})
	if err != nil {
		return nil, err
	}
	return s.opFileTailLog(ctx, tail)
}
