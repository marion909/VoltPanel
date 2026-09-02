package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gueltigerContainer(srv *Server) ContainerParams {
	return ContainerParams{
		Name: "shop-example-at", SystemUser: "site_shop",
		RootPath: srv.roots[0] + "/shop.example.at",
		Image:    "nginx:1.27-alpine",
		HostPort: 21000, Port: 8080,
	}
}

// TestKeinSchalterAusDerAnfrage ist der Kern.
//
// Ein Container ist kein Sandkasten, sondern ein Prozess mit anderen
// Namensräumen — und eine Handvoll Schalter hebt jede Trennung auf, die dieses
// Panel aufbaut. Deshalb gibt es keine Schalterliste, die geprüft würde: der
// Aufrufer beschreibt, was er will, und die Kommandozeile baut der Agent.
//
// Der Test hält das an der Datenstruktur fest. Es gibt kein Feld, über das
// einer dieser Schalter in den Aufruf käme.
func TestKeinSchalterAusDerAnfrage(t *testing.T) {
	srv, _ := testServer(t)

	// Ein Versuch, über jedes vorhandene Feld einen Schalter einzuschleusen.
	angriffe := map[string]func(*ContainerParams){
		"privileged als Image": func(p *ContainerParams) { p.Image = "--privileged" },
		"Schalter nach Image":  func(p *ContainerParams) { p.Image = "nginx --privileged" },
		"Netzmodus als Image":  func(p *ContainerParams) { p.Image = "--net=host" },
		// Der Docker-Socket des Wirts lässt sich hier gar nicht benennen — die
		// Quelle ist immer relativ zur Site-Wurzel. Geprüft wird deshalb
		// beides: dass eine absolute Quelle abgelehnt wird (unten), und dass
		// sich /run im Container nicht überdecken lässt.
		"Socketpfad überdecken": func(p *ContainerParams) {
			p.Volumes = []ContainerVolume{{Source: "x", Target: "/var/run/docker.sock"}}
		},
		"Wurzel als Volume":     func(p *ContainerParams) { p.Volumes = []ContainerVolume{{Source: "..", Target: "/host"}} },
		"absolute Quelle":       func(p *ContainerParams) { p.Volumes = []ContainerVolume{{Source: "/etc", Target: "/host"}} },
		"proc überdecken":       func(p *ContainerParams) { p.Volumes = []ContainerVolume{{Source: "x", Target: "/proc"}} },
		"dev überdecken":        func(p *ContainerParams) { p.Volumes = []ContainerVolume{{Source: "x", Target: "/dev/sda"}} },
		"Doppelpunkt im Ziel":   func(p *ContainerParams) { p.Volumes = []ContainerVolume{{Source: "x", Target: "/a:/b:rw"}} },
		"Schalter in den CPUs":  func(p *ContainerParams) { p.CPUs = "1 --privileged" },
		"Pfad außerhalb":        func(p *ContainerParams) { p.RootPath = "/etc" },
		"Port außer der Reihe":  func(p *ContainerParams) { p.HostPort = 22 },
		"Port über dem Bereich": func(p *ContainerParams) { p.HostPort = 8080 },
		"Name mit Pfadwechsel":  func(p *ContainerParams) { p.Name = "../../etc" },
	}

	for name, kaputt := range angriffe {
		p := gueltigerContainer(srv)
		kaputt(&p)
		if args, err := srv.dockerRunArgs(p, 1001, 1001); err == nil {
			t.Errorf("%s wurde angenommen: docker %s", name, strings.Join(args, " "))
		}
	}
}

// TestJederAufrufTraegtDieSchranken: die Schalter, die den Container einsperren,
// stehen fest im Aufruf — nicht als Voreinstellung, die jemand überschreiben
// könnte.
func TestJederAufrufTraegtDieSchranken(t *testing.T) {
	srv, _ := testServer(t)
	p := gueltigerContainer(srv)
	p.MemoryMB = 512
	p.CPUs = "0.5"

	args, err := srv.dockerRunArgs(p, 1001, 1001)
	if err != nil {
		t.Fatal(err)
	}
	zeile := strings.Join(args, " ")

	muss := []string{
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--pids-limit 512",
		"--network bridge",
		"--publish 127.0.0.1:21000:8080",
		"--restart unless-stopped",
		"--memory 512m",
		"--cpus 0.5",
		"--user ",
	}
	// Und niemals unter einem Systemkonto.
	if _, err := srv.dockerRunArgs(p, 0, 0); err == nil {
		t.Error("ein Container unter UID 0 wurde angenommen")
	}

	for _, m := range muss {
		if !strings.Contains(zeile, m) {
			t.Errorf("%q fehlt im Aufruf: docker %s", m, zeile)
		}
	}

	// Und was nie dastehen darf. Argumentweise verglichen, nicht als
	// Teilzeichenkette: "--pid" steckt in "--pids-limit", und ein Test, der
	// darüber stolpert, sagt nichts über den Schalter, um den es geht.
	verboten := map[string]bool{
		"--privileged": true, "--pid": true, "--ipc": true, "--uts": true,
		"--cap-add": true, "--device": true, "--userns": true,
		"--net": true, "--entrypoint": true, "--group-add": true,
		"--privileged=true": true, "--net=host": true, "--pid=host": true,
		"--userns=host": true,
	}
	for _, a := range args {
		if verboten[a] {
			t.Errorf("%q steht im Aufruf: docker %s", a, zeile)
		}
	}
	// Netz und Benutzer sind gesetzt, aber nie auf den Wirt bzw. auf root.
	for i, a := range args {
		if (a == "--network" || a == "--user" || a == "--security-opt") && i+1 < len(args) {
			switch args[i+1] {
			case "host", "0:0", "root", "seccomp=unconfined", "apparmor=unconfined":
				t.Errorf("%s %s steht im Aufruf", a, args[i+1])
			}
		}
	}
	if strings.Contains(zeile, "0.0.0.0:") {
		t.Errorf("veröffentlicht auf allen Adressen: docker %s", zeile)
	}

	// Das Image steht am Ende und ohne Kommando dahinter: ein Kommando wäre
	// die Stelle, an der ein Container doch wieder Code ausführt, den nicht
	// der Image-Autor bestimmt hat.
	if args[len(args)-1] != p.Image {
		t.Errorf("nach dem Image steht noch etwas: %v", args[len(args)-2:])
	}
}

// TestVeroeffentlichtNurAufLocalhost: der Weg von außen führt über den Vhost,
// wo TLS, Zugriffsregeln und Protokollierung schon stehen. Ein Container, der
// selbst auf 0.0.0.0 horcht, geht daran vorbei.
func TestVeroeffentlichtNurAufLocalhost(t *testing.T) {
	srv, _ := testServer(t)
	p := gueltigerContainer(srv)

	args, err := srv.dockerRunArgs(p, 1001, 1001)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range args {
		if a != "--publish" {
			continue
		}
		if !strings.HasPrefix(args[i+1], "127.0.0.1:") {
			t.Errorf("veröffentlicht auf %q", args[i+1])
		}
	}
}

func TestDockerStatusUnterscheidetInstalliertVonVerfuegbar(t *testing.T) {
	srv, _ := testServer(t)
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\necho daemon fehlt\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	alt := allowedBinaries["docker"]
	allowedBinaries["docker"] = docker
	t.Cleanup(func() { allowedBinaries["docker"] = alt })

	out, err := srv.opDockerStatus(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	st := out.(DockerStatus)
	if !st.Installed {
		t.Fatal("Docker-CLI ist da, Status meldet aber nicht installiert")
	}
	if st.Available {
		t.Fatal("ein nicht antwortender Daemon darf nicht als verfuegbar gelten")
	}
	if len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "Daemon antwortet aber nicht") {
		t.Fatalf("Daemon-Warnung fehlt: %+v", st)
	}
}

func TestDockerStatusMeldetInstalliertesPaketOhneCLI(t *testing.T) {
	srv, _ := testServer(t)
	dir := t.TempDir()
	dpkg := filepath.Join(dir, "dpkg-query")
	skript := "#!/bin/sh\nif [ \"$4\" = docker.io ]; then printf 'install ok installed'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(dpkg, []byte(skript), 0o755); err != nil {
		t.Fatal(err)
	}

	altDocker := allowedBinaries["docker"]
	altDpkg := allowedBinaries["dpkg-query"]
	allowedBinaries["docker"] = filepath.Join(dir, "docker-fehlt")
	allowedBinaries["dpkg-query"] = dpkg
	t.Cleanup(func() {
		allowedBinaries["docker"] = altDocker
		allowedBinaries["dpkg-query"] = altDpkg
	})

	out, err := srv.opDockerStatus(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	st := out.(DockerStatus)
	if st.Installed {
		t.Fatal("fehlende Docker-CLI darf nicht als installiert gelten")
	}
	if len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "laut dpkg installiert") {
		t.Fatalf("kaputtes docker.io-Paket wird nicht erklärt: %+v", st)
	}
	if !strings.Contains(st.Warnings[0], "--reinstall") {
		t.Fatalf("Reparaturhinweis fehlt: %+v", st)
	}
}

func TestDockerFehlerNenntAuchLeerenProzessfehler(t *testing.T) {
	got := commandMessage("", os.ErrNotExist, 500)
	if !strings.Contains(got, "file does not exist") {
		t.Fatalf("commandMessage = %q", got)
	}
}
