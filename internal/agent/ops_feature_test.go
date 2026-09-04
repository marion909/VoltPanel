package agent

import "testing"

// TestJedeFeatureStartetIhrenDienst hält die Lücke fest, die
// fail2ban/postfix/dovecot/opendkim/rspamd hatte: aptInstallOptions sperrt
// während der Installation jeden Dienststart über eine temporäre policy-rc.d
// (blockServiceStarts) — danach muss featureDiensteStarten ihn explizit
// nachholen. Eine Fähigkeit ohne Eintrag in featureDienste meldet "installiert",
// der Dienst läuft danach aber nie, bis jemand ihn manuell startet oder der
// Server neu startet.
func TestJedeFeatureStartetIhrenDienst(t *testing.T) {
	for feature := range featurePakete {
		if len(featureDienste[feature]) == 0 {
			t.Errorf("featurePakete[%q] hat keinen Eintrag in featureDienste — der Dienst startet nach der Installation nie", feature)
		}
	}
}

// TestFeatureDiensteStehenAufDerWhitelist stellt sicher, dass jeder in
// featureDienste genannte Dienst auch tatsächlich von checkService akzeptiert
// wird — sonst scheiterte featureDiensteStarten beim ersten Aufruf, nicht beim
// Bauen.
func TestFeatureDiensteStehenAufDerWhitelist(t *testing.T) {
	for feature, dienste := range featureDienste {
		for _, dienst := range dienste {
			if err := checkService(dienst); err != nil {
				t.Errorf("featureDienste[%q] enthält %q, das checkService ablehnt: %v", feature, dienst, err)
			}
		}
	}
}
