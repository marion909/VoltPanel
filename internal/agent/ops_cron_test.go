package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestOpCronWriteLehntRunAsOhneSitePraefixAb deckt die Lücke ab, die
// opCronWrite vorher hatte: RunAs wurde nur über checkUsername geprüft
// (verbietet nur die reservierten Systemkonten), nicht über siteUserIDs
// (Präfix- + UID-Prüfung), wie es alle Nachbardateien für vergleichbare
// Felder tun. Ein Cronjob ließ sich damit dauerhaft unter der Identität
// eines beliebigen anderen existierenden Benutzers einrichten.
func TestOpCronWriteLehntRunAsOhneSitePraefixAb(t *testing.T) {
	srv := &Server{}

	for _, runAs := range []string{"bob", "mysql", "www-data", "root"} {
		raw, err := json.Marshal(CronParams{
			Name: "test_job", Schedule: "* * * * *", Command: "true", RunAs: runAs,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := srv.opCronWrite(context.Background(), raw); err == nil {
			t.Errorf("opCronWrite akzeptierte RunAs=%q (kein Site-Systembenutzer)", runAs)
		}
	}
}

// TestEscapeCronPercentMaskiertNurProzent hält die zweite Lücke fest: ein
// unmaskiertes '%' im Kommandofeld wird von cron(8) als Zeilenumbruch
// gelesen, der Rest als Stdin an das Kommando weitergereicht.
func TestEscapeCronPercentMaskiertNurProzent(t *testing.T) {
	cases := map[string]string{
		"echo hallo":                  "echo hallo",
		"curl http://x.at/hook?ts=%s": `curl http://x.at/hook?ts=\%s`,
		"100% fertig":                 `100\% fertig`,
		"%%":                          `\%\%`,
	}
	for in, want := range cases {
		if got := escapeCronPercent(in); got != want {
			t.Errorf("escapeCronPercent(%q) = %q, erwartet %q", in, got, want)
		}
	}

	// Nach dem Maskieren darf kein unmaskiertes '%' mehr vorkommen — das ist
	// die eigentliche Eigenschaft, nicht nur der Einzelfall oben.
	roh := "wget http://x.at/a%20b?x=%s&y=%d"
	maskiert := escapeCronPercent(roh)
	ohneEscapes := strings.ReplaceAll(maskiert, `\%`, "")
	if strings.Contains(ohneEscapes, "%") {
		t.Errorf("escapeCronPercent(%q) = %q enthält noch ein unmaskiertes %%", roh, maskiert)
	}
}
