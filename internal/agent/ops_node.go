package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Node-Versionen.
//
// Mehrere Fassungen nebeneinander, damit eine alte Anwendung weiterläuft, während
// eine neue schon auf der nächsten baut. Die Roadmap nennt fnm; fnm installiert
// aber je Benutzer, und in diesem Panel hat jede Site einen eigenen — dieselbe
// Fassung läge dann zwanzigmal auf der Platte.
//
// Deshalb systemweit unter /opt/volt/node/<major>, entpackt aus dem offiziellen
// Archiv. Die Apps wählen "node22" statt "node"; welcher Pfad das ist, weiß der
// Agent.

const (
	nodeBaseURL     = "https://nodejs.org/dist"
	nodeDownloadMax = 200 << 20
	nodeTimeout     = 15 * time.Minute
)

// reNodeVersion ist eine vollständige Versionsangabe wie "22.12.0".
//
// Sie wird Teil einer URL und Teil eines Pfades. Ein "../" darin wäre beides
// zugleich: ein anderer Ort zum Herunterladen und ein anderer zum Auspacken.
var reNodeVersion = regexp.MustCompile(`^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)

// nodeDir ist das Verzeichnis einer installierten Fassung.
func (s *Server) nodeDir(major int) string {
	return filepath.Join(s.nodeRoot, "node"+strconv.Itoa(major))
}

type NodeVersion struct {
	Major   int    `json:"major"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Binary  string `json:"binary"`
}

type NodeInstallParams struct {
	Version string `json:"version"`
}

// opNodeList sagt, welche Fassungen dastehen.
func (s *Server) opNodeList(_ context.Context, _ json.RawMessage) (any, error) {
	out := []NodeVersion{}
	entries, err := os.ReadDir(s.nodeRoot)
	if err != nil {
		// Kein Verzeichnis heißt: noch keine Fassung installiert. Kein Fehler.
		return out, nil
	}

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "node") {
			continue
		}
		major, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "node"))
		if err != nil || major < 1 {
			continue
		}
		dir := filepath.Join(s.nodeRoot, e.Name())
		bin := filepath.Join(dir, "bin", "node")
		if !fileExists(bin) {
			// Ein halb entpacktes Verzeichnis ist keine Fassung.
			continue
		}
		v := NodeVersion{Major: major, Path: dir, Binary: bin}
		if raw, err := os.ReadFile(filepath.Join(dir, "VOLT_VERSION")); err == nil {
			v.Version = strings.TrimSpace(string(raw))
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Major > out[j].Major })
	return out, nil
}

// opNodeInstall holt eine Fassung und packt sie aus.
//
// Der Ablauf hat drei Stellen, an denen etwas schiefgehen kann, und alle drei
// enden hier mit einem Abbruch statt mit einem halb entpackten Verzeichnis:
// der Download, die Prüfsumme und das Auspacken.
func (s *Server) opNodeInstall(ctx context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[NodeInstallParams](raw, OpNodeInstall)
	if err != nil {
		return nil, err
	}
	version := strings.TrimPrefix(strings.TrimSpace(p.Version), "v")
	if !reNodeVersion.MatchString(version) {
		return nil, opInputErr(OpNodeInstall, "%q ist keine version — erwartet wird etwas "+
			"wie 22.12.0", p.Version)
	}
	arch, err := nodeArch()
	if err != nil {
		return nil, opErr(OpNodeInstall, "%v", err)
	}

	major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	name := fmt.Sprintf("node-v%s-linux-%s.tar.gz", version, arch)

	// Die Prüfsumme zuerst: sie kommt aus derselben Datei, die Node zu jedem
	// Release veröffentlicht. Was das leistet und was nicht, steht bei
	// nodeChecksum.
	want, err := s.nodeChecksum(ctx, version, name)
	if err != nil {
		return nil, opErr(OpNodeInstall, "%v", err)
	}

	tmp, err := os.MkdirTemp(s.nodeRoot, ".volt-node-*")
	if err != nil {
		if err := os.MkdirAll(s.nodeRoot, 0o755); err != nil {
			return nil, opErr(OpNodeInstall, "verzeichnis anlegen: %v", err)
		}
		if tmp, err = os.MkdirTemp(s.nodeRoot, ".volt-node-*"); err != nil {
			return nil, opErr(OpNodeInstall, "arbeitsverzeichnis: %v", err)
		}
	}
	defer os.RemoveAll(tmp)

	url := fmt.Sprintf("%s/v%s/%s", nodeBaseURL, version, name)
	sum, err := s.nodeExtract(ctx, url, tmp)
	if err != nil {
		return nil, opErr(OpNodeInstall, "%v", err)
	}
	if sum != want {
		// Erst nach dem Auspacken vergleichbar, weil die Summe über den Strom
		// mitläuft. Das ausgepackte Verzeichnis wird verworfen, nicht benutzt.
		return nil, opErr(OpNodeInstall,
			"die prüfsumme stimmt nicht: erwartet %s, bekommen %s", want, sum)
	}

	ziel := s.nodeDir(major)
	if err := os.WriteFile(filepath.Join(tmp, "VOLT_VERSION"), []byte(version+"\n"), 0o644); err != nil {
		return nil, opErr(OpNodeInstall, "version vermerken: %v", err)
	}
	// Erst am Ende an den endgültigen Platz. Ein Abbruch vorher hinterlässt
	// nichts, was wie eine gültige Fassung aussieht.
	_ = os.RemoveAll(ziel)
	if err := os.Rename(tmp, ziel); err != nil {
		return nil, opErr(OpNodeInstall, "einsetzen: %v", err)
	}

	return NodeVersion{
		Major: major, Version: version, Path: ziel,
		Binary: filepath.Join(ziel, "bin", "node"),
	}, nil
}

// nodeChecksum holt die Prüfsumme aus SHASUMS256.txt.
//
// Was das leistet: es fängt einen abgebrochenen oder verstümmelten Download,
// und es fängt den Fall, dass zwischen Prüfsummenliste und Archiv etwas
// anderes ausgeliefert wird.
//
// Was es nicht leistet: Schutz vor einem übernommenen nodejs.org. Beide Dateien
// kommen von dort, und wer die eine austauschen kann, kann auch die andere.
// Der Anker ist das TLS-Zertifikat von nodejs.org, nicht diese Summe. Node
// signiert die Liste zusätzlich mit GPG; das hier zu prüfen hieße, die
// Release-Schlüssel im Binary zu führen und mitzupflegen — was in dem Moment
// unbemerkt bricht, in dem Node einen Schlüssel wechselt.
func (s *Server) nodeChecksum(ctx context.Context, version, name string) (string, error) {
	url := fmt.Sprintf("%s/v%s/SHASUMS256.txt", nodeBaseURL, version)
	body, err := s.nodeGet(ctx, url)
	if err != nil {
		return "", err
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("prüfsummen lesen: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		sum, datei, ok := strings.Cut(strings.TrimSpace(line), "  ")
		if ok && datei == name {
			if len(sum) != 64 {
				return "", fmt.Errorf("die prüfsumme zu %s ist unbrauchbar", name)
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("zu %s gibt es keine prüfsumme — gibt es die version?", name)
}

func (s *Server) nodeGet(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "VoltPanel")

	client := &http.Client{Timeout: nodeTimeout}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("%s: status %d", url, res.StatusCode)
	}
	return res.Body, nil
}

// nodeExtract lädt das Archiv, packt es aus und gibt die Prüfsumme des Stroms
// zurück.
//
// Ausgepackt wird mit archive/tar, nicht mit dem Programm tar. Der Grund ist
// derselbe wie überall in diesem Projekt: ein Archiv ist Eingabe, und wer sie
// an ein Werkzeug weitergibt, das sie anders auslegt, hat die Prüfung
// verschenkt. Hier wird jeder Eintrag selbst angesehen — und ein Pfad mit ".."
// oder einem führenden Schrägstrich fliegt raus, statt neben dem Zielordner zu
// landen.
func (s *Server) nodeExtract(ctx context.Context, url, dest string) (string, error) {
	body, err := s.nodeGet(ctx, url)
	if err != nil {
		return "", err
	}
	defer body.Close()

	hash := sha256.New()
	limited := io.LimitReader(body, nodeDownloadMax)
	gz, err := gzip.NewReader(io.TeeReader(limited, hash))
	if err != nil {
		return "", fmt.Errorf("archiv lesen: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("archiv lesen: %w", err)
		}
		if err := extractOne(tr, h, dest); err != nil {
			return "", err
		}
	}

	// Den Rest des Stroms noch durch die Prüfsumme ziehen: gzip hört auf,
	// sobald der Inhalt vollständig ist, und ohne das fehlten die letzten Bytes
	// in der Summe.
	if _, err := io.Copy(hash, limited); err != nil {
		return "", fmt.Errorf("archiv lesen: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// extractOne schreibt einen einzelnen Eintrag.
//
// Das Archiv von Node hat ein Wurzelverzeichnis (node-v22.12.0-linux-x64/); das
// wird abgeschnitten, damit bin/node direkt unter dem Ziel liegt.
func extractOne(tr *tar.Reader, h *tar.Header, dest string) error {
	name := strings.TrimPrefix(filepath.ToSlash(h.Name), "./")
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[i+1:]
	} else {
		// Das Wurzelverzeichnis selbst.
		return nil
	}
	if name == "" {
		return nil
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("das archiv enthält den pfad %q", h.Name)
	}
	ziel := filepath.Join(dest, filepath.FromSlash(name))

	switch h.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(ziel, 0o755)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(ziel), 0o755); err != nil {
			return err
		}
		// Nur die Ausführungsbits aus dem Archiv übernehmen, sonst feste
		// Rechte. Ein Archiv aus dem Netz bestimmt nicht, welche Rechte eine
		// Datei auf diesem Server bekommt: 0666 im Archiv hieße sonst, dass
		// jeder Benutzer sie überschreiben kann.
		//
		// Das setuid-Bit kommt auf diesem Weg ohnehin nicht durch — Go bildet
		// es nicht aus den unteren Bits einer Zahl, sondern nur aus
		// os.ModeSetuid. Die Zeile hier hält also die Schreibrechte im Zaum,
		// nicht das setuid-Bit; das steht hier, weil ich es beim ersten
		// Schreiben andersherum kommentiert hatte.
		mode := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(ziel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(f, tr); err != nil {
			return err
		}
		// Ausdrücklich nachziehen: bei os.OpenFile wirkt die umask des
		// Prozesses, bei chmod nicht. Ohne das hinge das Ergebnis daran,
		// welche umask der Agent gerade hat.
		return os.Chmod(ziel, mode)
	case tar.TypeSymlink:
		// Symlinks dürfen nur innerhalb des ausgepackten Verzeichnisses zeigen.
		// Ein Link auf /etc/shadow wäre nach dem Auspacken eine Datei im
		// Node-Verzeichnis, die dorthin zeigt — und alles, was danach unter
		// diesem Namen gelesen wird, läse /etc/shadow.
		//
		// Geprüft wird, wohin der Link *zeigt*, nicht ob ".." darin vorkommt:
		// Node verlinkt sein eigenes npm als
		// "../lib/node_modules/npm/bin/npm-cli.js", und eine Prüfung auf ".."
		// lehnte genau das ab. Das ist beim ersten Versuch passiert.
		if strings.HasPrefix(h.Linkname, "/") {
			return fmt.Errorf("das archiv enthält den symlink %q → %q", h.Name, h.Linkname)
		}
		if !unterhalb(dest, filepath.Join(filepath.Dir(ziel), h.Linkname)) {
			return fmt.Errorf("das archiv enthält den symlink %q → %q", h.Name, h.Linkname)
		}
		if err := os.MkdirAll(filepath.Dir(ziel), 0o755); err != nil {
			return err
		}
		_ = os.Remove(ziel)
		return os.Symlink(h.Linkname, ziel)
	}
	// Alles Übrige — Geräte, Sockets, Hardlinks — hat in einem Node-Archiv
	// nichts zu suchen und wird übergangen.
	return nil
}

// unterhalb sagt, ob ein Pfad nach dem Auflösen noch unter der Wurzel liegt.
func unterhalb(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// nodeArch übersetzt die Architektur in Nodes Schreibweise.
func nodeArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	}
	return "", fmt.Errorf("für die architektur %s gibt es kein node-archiv", runtime.GOARCH)
}

// opNodeRemove entfernt eine installierte Fassung.
func (s *Server) opNodeRemove(_ context.Context, raw json.RawMessage) (any, error) {
	p, err := decode[struct {
		Major int `json:"major"`
	}](raw, OpNodeRemove)
	if err != nil {
		return nil, err
	}
	// Der Pfad entsteht aus einer Zahl. Käme er aus der Anfrage, wäre "eine
	// Node-Fassung entfernen" ein Weg, jedes Verzeichnis des Servers zu löschen.
	if p.Major < 1 || p.Major > 999 {
		return nil, opInputErr(OpNodeRemove, "%d ist keine hauptversion", p.Major)
	}
	dir := s.nodeDir(p.Major)
	if !fileExists(dir) {
		return TextResult{Text: "node" + strconv.Itoa(p.Major) + " war nicht installiert"}, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, opErr(OpNodeRemove, "entfernen: %v", err)
	}
	return TextResult{Text: "node" + strconv.Itoa(p.Major) + " entfernt"}, nil
}
