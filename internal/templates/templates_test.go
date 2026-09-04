package templates

import (
	"strings"
	"testing"

	"github.com/marion909/voltpanel/internal/store"
)

func phpSite() *store.Site {
	return &store.Site{
		ID: 1, TenantID: 1, Domain: "example.at", Aliases: []string{"www.example.at"},
		Type: store.SitePHP, SystemUser: "site_example", RootPath: "/var/www/example.at",
		DocumentRoot: "public", PHPVersion: "8.3", SSLEnabled: true, ForceHTTPS: true, HSTS: true,
	}
}

func phpData() SiteData {
	return SiteData{
		Site:        phpSite(),
		LogDir:      "/var/log/volt/sites",
		ACMEWebroot: "/var/lib/volt/acme",
		CertPath:    "/var/lib/volt/certs/example.at/fullchain.pem",
		KeyPath:     "/var/lib/volt/certs/example.at/privkey.pem",
		SocketPath:  "/run/php/volt-example.sock",
	}
}

func TestRenderSitePHP(t *testing.T) {
	out, err := RenderSite(phpData())
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}

	must := []string{
		"server_name example.at www.example.at;",
		"root /var/www/example.at/public;",
		"fastcgi_pass unix:/run/php/volt-example.sock;",
		"return 301 https://$host$request_uri;",
		"Strict-Transport-Security",
		"ssl_certificate     /var/lib/volt/certs/example.at/fullchain.pem;",
		"listen 443 ssl;",
		// Ohne try_files vor fastcgi_pass wäre ein hochgeladenes Bild als PHP ausführbar.
		"try_files $uri =404;",
		"location ^~ /.well-known/acme-challenge/",
	}
	for _, s := range must {
		if !strings.Contains(out, s) {
			t.Errorf("Vhost enthält %q nicht.\n---\n%s", s, out)
		}
	}
}

func TestRenderSiteStaticHasNoPHP(t *testing.T) {
	d := phpData()
	d.Site.Type = store.SiteStatic
	d.Site.SSLEnabled = false
	d.CertPath, d.KeyPath, d.SocketPath = "", "", ""

	out, err := RenderSite(d)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	if strings.Contains(out, "fastcgi_pass") {
		t.Errorf("statische Site enthält fastcgi_pass:\n%s", out)
	}
	if !strings.Contains(out, "listen 80;") {
		t.Errorf("Site ohne SSL hört nicht auf Port 80:\n%s", out)
	}
}

func TestRenderSiteProxy(t *testing.T) {
	d := phpData()
	d.Site.Type = store.SiteProxy
	d.Site.ProxyTarget = "http://127.0.0.1:3000"
	d.SocketPath = ""

	out, err := RenderSite(d)
	if err != nil {
		t.Fatalf("RenderSite: %v", err)
	}
	for _, s := range []string{"proxy_pass http://127.0.0.1:3000;", "$connection_upgrade"} {
		if !strings.Contains(out, s) {
			t.Errorf("Proxy-Vhost enthält %q nicht:\n%s", s, out)
		}
	}
}

// TestRenderSiteRejectsInjection ist der Grund, warum jede Vorlage validiert
// wird: text/template escaped nichts, ein durchgelassener Wert würde direkt
// zusätzliche Nginx-Direktiven erzeugen.
func TestRenderSiteRejectsInjection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SiteData)
	}{
		{"domain mit direktive", func(d *SiteData) {
			d.Site.Domain = "evil.at;\n}\nserver{listen 8080;root /;"
		}},
		{"alias mit umbruch", func(d *SiteData) {
			d.Site.Aliases = []string{"a.at\n    root /etc;"}
		}},
		{"root_path mit umbruch", func(d *SiteData) {
			d.Site.RootPath = "/var/www/x;\n}\nserver{"
		}},
		{"proxy-ziel als datei-url", func(d *SiteData) {
			d.Site.Type, d.Site.ProxyTarget = store.SiteProxy, "file:///etc/passwd"
		}},
		{"proxy-ziel mit semikolon", func(d *SiteData) {
			d.Site.Type = store.SiteProxy
			d.Site.ProxyTarget = "http://127.0.0.1:3000; proxy_set_header X-Evil 1"
		}},
		{"zertifikatspfad mit umbruch", func(d *SiteData) {
			d.CertPath = "/x.pem;\n    ssl_verify_client off;"
		}},
		{"ip-sperre mit direktive", func(d *SiteData) {
			d.DenyIPs = []string{"1.2.3.4;\n    root /;"}
		}},
		{"zusatzzeile bricht block auf", func(d *SiteData) {
			d.ExtraLines = []string{"} server { listen 9999;"}
		}},
		{"zusatzzeile mit location-block", func(d *SiteData) {
			d.ExtraLines = []string{"location /x { deny all; }"}
		}},
		{"zusatzzeile ohne semikolon", func(d *SiteData) {
			d.ExtraLines = []string{"add_header X 1"}
		}},
		{"zusatzzeile mit include", func(d *SiteData) {
			d.ExtraLines = []string{"include /etc/passwd;"}
		}},
		{"zusatzzeile mit umbruch", func(d *SiteData) {
			d.ExtraLines = []string{"add_header X 1;\n    root /etc;"}
		}},
		{"weiterleitung auf javascript-url", func(d *SiteData) {
			d.Redirects = []Redirect{{From: "/x", To: "javascript:alert(1)", Code: 301}}
		}},
		{"unerlaubter weiterleitungscode", func(d *SiteData) {
			d.Redirects = []Redirect{{From: "/x", To: "/y", Code: 200}}
		}},
		{"relativer webroot", func(d *SiteData) { d.Site.RootPath = "var/www/x" }},
		{"webroot mit ..", func(d *SiteData) { d.Site.RootPath = "/var/www/../etc" }},
		{"ssl ohne zertifikat", func(d *SiteData) { d.CertPath, d.KeyPath = "", "" }},
		{"php ohne socket", func(d *SiteData) { d.SocketPath = "" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := phpData()
			tc.mutate(&d)
			out, err := RenderSite(d)
			if err == nil {
				t.Fatalf("RenderSite akzeptierte die Eingabe:\n%s", out)
			}
		})
	}
}

func TestRenderPool(t *testing.T) {
	site := phpSite()
	pool := &store.PHPPool{
		SiteID: 1, TenantID: 1, PHPVersion: "8.3", PoolName: "volt-example",
		SocketPath: "/run/php/volt-example.sock", PM: "ondemand", MaxChildren: 12,
		MemoryLimit: "512M", MaxExecutionTime: 60, UploadMaxFilesize: "128M",
	}

	out, err := RenderPool(PoolData{Site: site, Pool: pool, LogDir: "/var/log/volt/sites"})
	if err != nil {
		t.Fatalf("RenderPool: %v", err)
	}

	must := []string{
		"[volt-example]",
		"user  = site_example",
		"listen       = /run/php/volt-example.sock",
		"pm.max_children      = 12",
		"php_value[memory_limit]        = 512M",
		// Ohne diese beiden Zeilen ist die Site nicht isoliert.
		"php_admin_value[open_basedir]      = /var/www/example.at:/var/www/example.at/tmp:/usr/share/php",
		"php_admin_value[disable_functions] = exec,passthru,shell_exec,system",
	}
	for _, s := range must {
		if !strings.Contains(out, s) {
			t.Errorf("Pool-Config enthält %q nicht:\n%s", s, out)
		}
	}
}

// TestRenderPoolSpareServersConsistent: FPM startet nicht, wenn die
// Spare-Werte nicht zueinander passen.
func TestRenderPoolSpareServersConsistent(t *testing.T) {
	for _, children := range []int{1, 2, 3, 5, 10, 50, 500} {
		pool := &store.PHPPool{
			PHPVersion: "8.3", PoolName: "pool1", SocketPath: "/run/php/p.sock",
			PM: "dynamic", MaxChildren: children,
		}
		d := PoolData{Site: phpSite(), Pool: pool, LogDir: "/var/log/volt"}
		if err := d.validate(); err != nil {
			t.Fatalf("max_children=%d: %v", children, err)
		}
		d.applyDefaults()

		if d.MinSpare < 1 || d.MaxSpare < d.MinSpare || d.StartServers < d.MinSpare || d.StartServers > d.MaxSpare {
			t.Errorf("max_children=%d ergibt start=%d min_spare=%d max_spare=%d — FPM würde das ablehnen",
				children, d.StartServers, d.MinSpare, d.MaxSpare)
		}
	}
}

func TestRenderPoolRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		pool store.PHPPool
	}{
		{"poolname mit slash", store.PHPPool{PoolName: "a/b", PHPVersion: "8.3", SocketPath: "/run/p.sock", MaxChildren: 5}},
		{"unbekannte php-version", store.PHPPool{PoolName: "pool1", PHPVersion: "9.9", SocketPath: "/run/p.sock", MaxChildren: 5}},
		{"relativer socket", store.PHPPool{PoolName: "pool1", PHPVersion: "8.3", SocketPath: "run/p.sock", MaxChildren: 5}},
		{"max_children null", store.PHPPool{PoolName: "pool1", PHPVersion: "8.3", SocketPath: "/run/p.sock", MaxChildren: 0}},
		{"max_children zu groß", store.PHPPool{PoolName: "pool1", PHPVersion: "8.3", SocketPath: "/run/p.sock", MaxChildren: 5000}},
		{"unbekannter pm", store.PHPPool{PoolName: "pool1", PHPVersion: "8.3", SocketPath: "/run/p.sock", MaxChildren: 5, PM: "magic"}},
		{"memory_limit unsinnig", store.PHPPool{PoolName: "pool1", PHPVersion: "8.3", SocketPath: "/run/p.sock", MaxChildren: 5, MemoryLimit: "viel"}},
		{"extra_ini eröffnet neuen pool", store.PHPPool{
			PoolName: "pool1", PHPVersion: "8.3", SocketPath: "/run/p.sock", MaxChildren: 5,
			ExtraINI: "foo = bar\n[boese]\nuser = root\nlisten = /run/boese.sock",
		}},
		{"extra_ini mit eckigen klammern ohne umbruch", store.PHPPool{
			PoolName: "pool1", PHPVersion: "8.3", SocketPath: "/run/p.sock", MaxChildren: 5,
			ExtraINI: "foo[] = bar",
		}},
		{"disable_functions eröffnet neuen pool", store.PHPPool{
			PoolName: "pool1", PHPVersion: "8.3", SocketPath: "/run/p.sock", MaxChildren: 5,
			DisableFunctions: "exec\n[boese]\nuser = root",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := tc.pool
			d := PoolData{Site: phpSite(), Pool: &pool, LogDir: "/var/log/volt"}
			if _, err := RenderPool(d); err == nil {
				t.Fatalf("RenderPool akzeptierte %+v", tc.pool)
			}
		})
	}
}

func TestRenderShared(t *testing.T) {
	out, err := RenderShared("/var/lib/volt/acme")
	if err != nil {
		t.Fatalf("RenderShared: %v", err)
	}
	for _, s := range []string{"map $http_upgrade $connection_upgrade", "listen 80 default_server;", "return 444;"} {
		if !strings.Contains(out, s) {
			t.Errorf("Shared-Config enthält %q nicht:\n%s", s, out)
		}
	}
	if _, err := RenderShared("relativ/pfad"); err == nil {
		t.Error("RenderShared akzeptierte einen relativen Pfad")
	}
}
