package agent

import (
	"strings"
	"testing"
)

// Docker mischt zwei Einheitensysteme, und beide sehen fast gleich aus.
// "1.45GB" sind 10^9, "1.45GiB" sind 2^30 — wer sie gleich behandelt, liegt um
// sieben Prozent daneben, und zwar immer in dieselbe Richtung.
func TestParseSizeUnterscheidetGBundGiB(t *testing.T) {
	faelle := []struct {
		in   string
		will int64
	}{
		{"0B", 0},
		{"512B", 512},
		{"1kB", 1000},
		{"1KiB", 1024},
		{"1.45GB", 1_450_000_000},
		{"1.45GiB", 1_556_925_644},
		{"1.945GiB", 2_088_378_368},
		{"12.3MiB", 12_897_484},
		{"", 0},
		{"N/A", 0},
		{"kaputt", 0},
		{"7Parsec", 0},
	}
	for _, f := range faelle {
		got := parseSize(f.in)
		// Ein Prozent Spielraum: die Nachkommastellen von Docker sind selbst
		// gerundet, und auf die letzte Stelle kommt es hier nicht an.
		if abweichung(got, f.will) > 0.01 {
			t.Errorf("parseSize(%q) = %d, erwartet %d", f.in, got, f.will)
		}
	}

	// Der eigentliche Punkt, noch einmal als Aussage: dieselbe Zahl mit den
	// beiden Suffixen darf nicht dasselbe Ergebnis haben.
	if parseSize("1GB") == parseSize("1GiB") {
		t.Error("GB und GiB werden gleich gerechnet")
	}
}

func abweichung(got, will int64) float64 {
	if will == 0 {
		if got == 0 {
			return 0
		}
		return 1
	}
	d := float64(got-will) / float64(will)
	if d < 0 {
		return -d
	}
	return d
}

func TestParseMemUsage(t *testing.T) {
	used, max := parseMemUsage("12.3MiB / 1.945GiB")
	if abweichung(used, 12_897_484) > 0.01 {
		t.Errorf("belegt = %d", used)
	}
	if abweichung(max, 2_088_378_368) > 0.01 {
		t.Errorf("grenze = %d", max)
	}

	// Ohne Grenze — ein Container ohne Speicherlimit.
	used, max = parseMemUsage("40MiB")
	if abweichung(used, 41_943_040) > 0.01 || max != 0 {
		t.Errorf("ohne grenze: belegt = %d, grenze = %d", used, max)
	}
}

func TestParsePercent(t *testing.T) {
	if v := parsePercent("12.34%"); v != 12.34 {
		t.Errorf("= %v", v)
	}
	if v := parsePercent("--"); v != 0 {
		t.Errorf("unlesbar sollte 0 sein, ist %v", v)
	}
}

// Die Statistik kommt zwar aus dem eigenen `docker ps`, aber die Zeilen werden
// trotzdem noch einmal am Präfix geprüft. Sonst stünde in der Übersicht des
// Panels irgendwann ein Container, den jemand anders gestartet hat.
func TestParseStatsLinesNimmtNurEigene(t *testing.T) {
	out := strings.Join([]string{
		"volt-shop\t1.5%\t12.3MiB / 512MiB\t2.40%\t1.2kB / 800B\t0B / 4.1kB\t7",
		"fremder-container\t99.0%\t900MiB / 1GiB\t90.00%\t0B / 0B\t0B / 0B\t3",
		"volt-blog\t0.00%\t8MiB / 256MiB\t3.12%\t0B / 0B\t0B / 0B\t2",
		"unvollständig\t1.0%",
		"",
	}, "\n")

	got := parseStatsLines(out)
	if len(got) != 2 {
		t.Fatalf("%d zeilen übernommen, erwartet 2: %+v", len(got), got)
	}
	for _, st := range got {
		if !strings.HasPrefix(st.Name, "volt-") {
			t.Errorf("fremder container in der liste: %s", st.Name)
		}
	}
	if got[0].Name != "volt-shop" || got[0].CPUPerc != 1.5 || got[0].PIDs != 7 {
		t.Errorf("erste zeile falsch gelesen: %+v", got[0])
	}
	if abweichung(got[0].MemUsed, 12_897_484) > 0.01 {
		t.Errorf("speicher falsch gelesen: %d", got[0].MemUsed)
	}
	if got[0].MemText != "12.3MiB / 512MiB" {
		t.Errorf("der text von docker fehlt: %q", got[0].MemText)
	}
}

// Ein Image ohne Namen lässt sich nur über seine Kennung entfernen —
// "<none>:<none>" kennt Docker nicht.
func TestParseImageLinesRefBeiNamenlosen(t *testing.T) {
	out := strings.Join([]string{
		"a1b2c3d4e5f6\tnginx\t1.27\t187MB\t2026-08-01 10:00:00 +0200 CEST",
		"ff00ff00ff00\t<none>\t<none>\t92.4MB\t2026-07-02 09:00:00 +0200 CEST",
		"kaputt\tnur\tdrei",
	}, "\n")

	got := parseImageLines(out)
	if len(got) != 2 {
		t.Fatalf("%d images, erwartet 2", len(got))
	}
	if got[0].Ref != "nginx:1.27" || got[0].Dangling {
		t.Errorf("benanntes image falsch: %+v", got[0])
	}
	if got[1].Ref != "ff00ff00ff00" || !got[1].Dangling {
		t.Errorf("namenloses image falsch: %+v", got[1])
	}
	if abweichung(got[0].Size, 187_000_000) > 0.01 {
		t.Errorf("größe = %d", got[0].Size)
	}
}
