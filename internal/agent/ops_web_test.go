package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestCheckNginxOnStartupWarntBeiUngueltigerConfig deckt den Fund ab, dass
// opNginxWriteVhost/opNginxWriteShared eine neue Config direkt an die
// Stelle schreiben, die nginx bei einem Reload einliest, und erst danach
// testen — stirbt der Agent genau dazwischen, bleibt eine ungeprüfte Datei
// live liegen, unbemerkt bis zum nächsten externen Ereignis. Der nächste
// Agent-Start soll das wenigstens sichtbar machen.
//
// Auf dieser Testmaschine gibt es kein nginx unter dem festen Pfad in
// allowedBinaries — run() scheitert deshalb deterministisch, genau wie es
// bei einer kaputten Config auf einem echten Server täte, und das reicht,
// um den Warnpfad zu prüfen.
func TestCheckNginxOnStartupWarntBeiUngueltigerConfig(t *testing.T) {
	var buf bytes.Buffer
	srv := &Server{log: slog.New(slog.NewTextHandler(&buf, nil))}

	srv.checkNginxOnStartup(context.Background())

	out := buf.String()
	if !strings.Contains(out, "nginx-config beim agent-start ungültig") {
		t.Fatalf("keine Warnung geloggt: %q", out)
	}
}
