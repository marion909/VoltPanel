package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/marion909/voltpanel/internal/dockerspec"
)

// Was ein Container verbraucht und was auf der Platte liegt.
//
// Beides fehlte bisher, und beides ist dieselbe Art Auskunft: das Panel zeigte
// den Zustand eines Containers ("läuft"), aber nicht seinen Preis. Wer wissen
// wollte, welche App den Speicher aufbraucht oder warum /var/lib/docker voll
// ist, musste auf die Shell — und damit als root auf einen Server, auf den er
// wegen des Panels gerade nicht mehr musste.

// ContainerStats ist der Verbrauch eines laufenden Containers.
//
// Neben den Zahlen stehen die Texte, die Docker selbst ausgibt. Das ist kein
// Ballast: eine Zahl, die beim Umrechnen verlorenging, fiele sonst nicht auf,
// und im Zweifel ist der Text von Docker die belastbarere Auskunft.
type ContainerStats struct {
	Name    string  `json:"name"`
	CPUPerc float64 `json:"cpu_perc"`
	MemUsed int64   `json:"mem_used"`
	MemMax  int64   `json:"mem_max"`
	MemPerc float64 `json:"mem_perc"`
	MemText string  `json:"mem_text"`
	NetText string  `json:"net_text"`
	IOText  string  `json:"io_text"`
	PIDs    int     `json:"pids"`
}

// ImageInfo ist ein Image auf diesem Server.
type ImageInfo struct {
	ID   string `json:"id"`
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
	// Ref ist, was man angeben muss, um es wieder loszuwerden: der Name mit
	// Tag, und bei einem namenlosen Image die Kennung.
	Ref      string `json:"ref"`
	Size     int64  `json:"size"`
	SizeText string `json:"size_text"`
	Created  string `json:"created"`
	// Dangling ist ein Image ohne Namen — übrig geblieben, weil ein neuer
	// Stand desselben Tags den alten abgehängt hat.
	Dangling bool `json:"dangling"`
}

// opDockerStats fragt den Verbrauch der laufenden Container ab.
//
// Zwei Aufrufe statt einem: `docker stats` kennt kein `--filter`, es nimmt nur
// Namen. Die Namen kommen deshalb aus `docker ps` — über dasselbe Label und
// dieselbe Präfixprüfung wie die Liste, damit hier nicht plötzlich Container
// auftauchen, die dem Panel nicht gehören.
func (s *Server) opDockerStats(ctx context.Context, _ json.RawMessage) (any, error) {
	namen, err := s.laufendeContainer(ctx)
	if err != nil {
		return nil, err
	}
	if len(namen) == 0 {
		// Ohne Namen zeigte `docker stats` alles, was auf dem Server läuft.
		// Das ist der Grund für die frühe Rückkehr, nicht die Ersparnis.
		return []ContainerStats{}, nil
	}

	args := []string{"stats", "--no-stream", "--format",
		"{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.NetIO}}\t{{.BlockIO}}\t{{.PIDs}}", "--"}
	args = append(args, namen...)

	out, err := run(ctx, shortTimeout, "docker", args...)
	if err != nil {
		return nil, opErr(OpDockerStats, "%s", truncate(out, 300))
	}

	return parseStatsLines(out), nil
}

// parseStatsLines liest die Ausgabe von `docker stats`.
//
// Getrennt vom Aufruf, damit sie sich prüfen lässt, ohne einen Docker-Daemon
// zu haben. Die Namensprüfung steht hier ein zweites Mal: die Liste kommt zwar
// aus dem eigenen `docker ps`, aber wer eine Zeile in dieser Funktion liest,
// soll nicht anderswo nachsehen müssen, ob sie geprüft war.
func parseStatsLines(out string) []ContainerStats {
	res := []ContainerStats{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) < 7 || !dockerspec.ContainerNameOwned(f[0]) {
			continue
		}
		used, max := parseMemUsage(f[2])
		res = append(res, ContainerStats{
			Name:    f[0],
			CPUPerc: parsePercent(f[1]),
			MemUsed: used,
			MemMax:  max,
			MemPerc: parsePercent(f[3]),
			MemText: strings.TrimSpace(f[2]),
			NetText: strings.TrimSpace(f[4]),
			IOText:  strings.TrimSpace(f[5]),
			PIDs:    atoiOr(f[6], 0),
		})
	}
	return res
}

// laufendeContainer sind die laufenden Container dieses Panels.
func (s *Server) laufendeContainer(ctx context.Context) ([]string, error) {
	out, err := run(ctx, shortTimeout, "docker", "ps",
		"--filter", "label=volt.site", "--filter", "status=running",
		"--format", "{{.Names}}")
	if err != nil {
		return nil, opErr(OpDockerStats, "%s", truncate(out, 300))
	}
	var namen []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if dockerspec.ContainerNameOwned(name) {
			namen = append(namen, name)
		}
	}
	return namen, nil
}

// opDockerImages listet, was auf der Platte liegt.
//
// Anders als bei Containern ist das die ganze Liste des Servers, nicht nur die
// des Panels. Ein Image trägt kein Label von uns — wir holen es ja fremd —,
// und die Frage, die dahintersteht, ist ohnehin die des Administrators: was
// belegt /var/lib/docker. Der Zugriff ist deshalb auf Administratoren
// beschränkt, eine Ebene höher.
func (s *Server) opDockerImages(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := run(ctx, shortTimeout, "docker", "images", "--format",
		"{{.ID}}\t{{.Repository}}\t{{.Tag}}\t{{.Size}}\t{{.CreatedAt}}")
	if err != nil {
		return nil, opErr(OpDockerImages, "%s", truncate(out, 300))
	}

	return parseImageLines(out), nil
}

// parseImageLines liest die Ausgabe von `docker images`.
func parseImageLines(out string) []ImageInfo {
	res := []ImageInfo{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) < 5 {
			continue
		}
		img := ImageInfo{
			ID:       f[0],
			Repo:     f[1],
			Tag:      f[2],
			Size:     parseSize(f[3]),
			SizeText: strings.TrimSpace(f[3]),
			Created:  strings.TrimSpace(f[4]),
			Dangling: f[1] == "<none>" || f[2] == "<none>",
		}
		// Ein namenloses Image lässt sich nur über seine Kennung entfernen.
		// Deshalb steht hier, was der Aufrufer wirklich zurückschicken muss,
		// und nicht ein zusammengesetzter Name, den Docker nicht kennt.
		if img.Dangling {
			img.Ref = f[0]
		} else {
			img.Ref = f[1] + ":" + f[2]
		}
		res = append(res, img)
	}
	return res
}

// opDockerImageRemove entfernt ein Image.
//
// Ohne `--force`, und das ist der ganze Punkt. Docker weigert sich von selbst,
// ein Image zu entfernen, an dem noch ein Container hängt; mit `--force` täte
// es das trotzdem und der Container startete beim nächsten Mal nicht mehr.
// Diese Weigerung ist die letzte Schranke hinter der Prüfung im Panel, das
// vorher in seinen eigenen Zeilen nachsieht, welche App welches Image benutzt.
func (s *Server) opDockerImageRemove(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Ref string `json:"ref"`
	}](raw, OpDockerImageRemove)
	if err != nil {
		return nil, err
	}
	if err := dockerspec.ValidImageRef(p.Ref); err != nil {
		return nil, opInputErr(OpDockerImageRemove, "%v", err)
	}

	out, err := run(ctx, dockerTimeout, "docker", "rmi", "--", p.Ref)
	if err != nil {
		return nil, opErr(OpDockerImageRemove, "image entfernen: %s", truncate(out, 400))
	}
	return TextResult{Text: strings.TrimSpace(truncate(out, 2000))}, nil
}

// parsePercent liest "12.34%".
func parsePercent(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil {
		return 0
	}
	return v
}

func atoiOr(s string, vorgabe int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return vorgabe
	}
	return v
}

// parseMemUsage liest "12.3MiB / 1.945GiB".
func parseMemUsage(s string) (used, max int64) {
	links, rechts, ok := strings.Cut(s, "/")
	if !ok {
		return parseSize(s), 0
	}
	return parseSize(links), parseSize(rechts)
}

// parseSize liest die Größenangaben von Docker.
//
// Docker mischt zwei Einheitensysteme: `docker images` schreibt "1.45GB" und
// meint 10^9, `docker stats` schreibt "1.945GiB" und meint 2^30. Wer beides
// gleich behandelt, liegt bei einem Gigabyte um sieben Prozent daneben — nicht
// dramatisch, aber falsch, und zwar systematisch in dieselbe Richtung.
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "N/A" {
		return 0
	}

	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	zahl, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}

	faktor := map[string]float64{
		"b": 1, "": 1,
		"kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
	}
	f, ok := faktor[strings.ToLower(strings.TrimSpace(s[i:]))]
	if !ok {
		return 0
	}
	return int64(zahl * f)
}
