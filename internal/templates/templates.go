// Package templates erzeugt alle Systemkonfigurationen aus Vorlagen.
//
// Prinzip 1 der Roadmap: nichts wird manuell gepatcht. Jede Nginx- und
// PHP-FPM-Config entsteht hier, aus dem Zustand in der Datenbank — damit ist
// sie jederzeit reproduzierbar und nach einem Restore identisch.
//
// text/template escaped nichts (eine Nginx-Config ist kein HTML), deshalb wird
// jeder eingesetzte Wert vorher in Validate() geprüft. Nur validierte Daten
// erreichen eine Vorlage.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/marion909/voltpanel/internal/store"
)

//go:embed nginx/*.tmpl php-fpm/*.tmpl systemd/*.tmpl fail2ban/*.tmpl dovecot.conf.tmpl dovecot-modern.conf.tmpl wordpress-config.php.tmpl mail-autoconfig.xml.tmpl mail-autodiscover.xml.tmpl roundcube-config.php.tmpl
var files embed.FS

var tmpl = template.Must(template.New("volt").
	Funcs(template.FuncMap{"join": strings.Join, "phpstr": phpstr, "xmlesc": xmlesc}).
	ParseFS(files, "nginx/*.tmpl", "php-fpm/*.tmpl", "systemd/*.tmpl", "fail2ban/*.tmpl",
		"dovecot.conf.tmpl", "dovecot-modern.conf.tmpl", "wordpress-config.php.tmpl",
		"mail-autoconfig.xml.tmpl", "mail-autodiscover.xml.tmpl", "roundcube-config.php.tmpl"))

// Redirect ist eine einzelne Weiterleitungsregel einer Site.
type Redirect struct {
	From string
	To   string
	Code int
}

// SiteData ist das vollständige Eingabemodell des Vhost-Templates.
type SiteData struct {
	Site        *store.Site
	GeneratedAt string
	ServerNames string
	WebRoot     string
	LogDir      string
	ACMEWebroot string
	CertPath    string
	KeyPath     string
	SocketPath  string

	MaxBodySize    string
	FastCGITimeout int

	DenyIPs       []string
	AllowIPs      []string
	BasicAuthFile string
	Redirects     []Redirect
	ExtraLines    []string
}

// PoolData ist das Eingabemodell des PHP-FPM-Pool-Templates.
type PoolData struct {
	Site             *store.Site
	Pool             *store.PHPPool
	GeneratedAt      string
	LogDir           string
	OpenBasedir      string
	DisableFunctions string
	Timezone         string
	StartServers     int
	MinSpare         int
	MaxSpare         int
}

// defaultDisableFunctions sperrt die Funktionen, über die PHP-Code sonst direkt
// Shell-Kommandos absetzen könnte. Eine kompromittierte Site bleibt damit in
// ihrem eigenen Verzeichnis.
const defaultDisableFunctions = "exec,passthru,shell_exec,system,proc_open,popen," +
	"curl_multi_exec,parse_ini_file,show_source,pcntl_exec,dl,symlink,link"

// RenderSite erzeugt die Nginx-Config einer Site.
func RenderSite(d SiteData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	d.applyDefaults()

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "site.conf.tmpl", d); err != nil {
		return "", fmt.Errorf("vhost für %s: %w", d.Site.Domain, err)
	}
	return buf.String(), nil
}

// RenderPool erzeugt die PHP-FPM-Pool-Config einer Site.
func RenderPool(d PoolData) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	d.applyDefaults()

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "pool.conf.tmpl", d); err != nil {
		return "", fmt.Errorf("pool für %s: %w", d.Site.Domain, err)
	}
	return buf.String(), nil
}

// RenderShared erzeugt die vhost-übergreifende Config (Upgrade-Map, Default-Server).
func RenderShared(acmeWebroot string) (string, error) {
	if !filepath.IsAbs(acmeWebroot) {
		return "", fmt.Errorf("acme-webroot %q muss absolut sein", acmeWebroot)
	}
	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "volt-shared.conf.tmpl",
		map[string]string{"ACMEWebroot": acmeWebroot})
	return buf.String(), err
}

// --- Validierung -----------------------------------------------------------

func (d *SiteData) validate() error {
	if d.Site == nil {
		return fmt.Errorf("keine site übergeben")
	}
	if !store.ValidDomain(d.Site.Domain) {
		return fmt.Errorf("domain %q ist ungültig", d.Site.Domain)
	}
	for _, a := range d.Site.Aliases {
		if !store.ValidDomain(a) {
			return fmt.Errorf("alias %q ist ungültig", a)
		}
	}
	if !d.Site.Type.Valid() {
		return fmt.Errorf("site-typ %q ist unbekannt", d.Site.Type)
	}

	// Alles, was als Pfad in die Config wandert, muss absolut und frei von
	// Zeilenumbrüchen sein — sonst ließen sich zusätzliche Direktiven einschleusen.
	for name, p := range map[string]string{
		"root_path":    d.Site.RootPath,
		"log_dir":      d.LogDir,
		"acme_webroot": d.ACMEWebroot,
	} {
		if err := checkPath(name, p); err != nil {
			return err
		}
	}
	for name, p := range map[string]string{
		"cert_path":       d.CertPath,
		"key_path":        d.KeyPath,
		"socket_path":     d.SocketPath,
		"basic_auth_file": d.BasicAuthFile,
	} {
		if p == "" {
			continue
		}
		if err := checkPath(name, p); err != nil {
			return err
		}
	}
	if d.Site.SSLEnabled && (d.CertPath == "" || d.KeyPath == "") {
		return fmt.Errorf("site %s hat ssl aktiviert, aber kein zertifikat", d.Site.Domain)
	}
	if d.Site.Type == store.SitePHP && d.SocketPath == "" {
		return fmt.Errorf("php-site %s ohne fpm-socket", d.Site.Domain)
	}

	if d.Site.Type == store.SiteProxy {
		u, err := url.Parse(d.Site.ProxyTarget)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("proxy-ziel %q muss eine http(s)-url mit host sein", d.Site.ProxyTarget)
		}
		if strings.ContainsAny(d.Site.ProxyTarget, " \t\n\r;{}") {
			return fmt.Errorf("proxy-ziel %q enthält unerlaubte zeichen", d.Site.ProxyTarget)
		}
	}

	for _, ip := range append(append([]string{}, d.DenyIPs...), d.AllowIPs...) {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	for _, r := range d.Redirects {
		if err := checkRedirect(r); err != nil {
			return err
		}
	}
	// ExtraLines kommen aus dem Rewrite-Editor und sind auf einzelne Direktiven
	// beschränkt: eine Zeile, keine geschweiften Klammern, abgeschlossen mit ";".
	//
	// Klammern zu zählen genügt hier nicht — "} server { listen 8080;" ist
	// balanciert und bricht trotzdem aus dem Server-Block in den http-Kontext
	// aus. Wer eigene location-Blöcke braucht, bekommt sie über die
	// strukturierten Felder (Redirects, IP-Regeln), nicht über freien Text.
	for _, line := range d.ExtraLines {
		if err := checkDirective(line); err != nil {
			return err
		}
	}
	if d.MaxBodySize != "" && !reSize.MatchString(d.MaxBodySize) {
		return fmt.Errorf("client_max_body_size %q ist ungültig", d.MaxBodySize)
	}
	return nil
}

func (d *SiteData) applyDefaults() {
	d.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	d.ServerNames = strings.Join(d.Site.ServerNames(), " ")
	d.WebRoot = d.Site.WebRoot()
	if d.MaxBodySize == "" {
		d.MaxBodySize = "64M"
	}
	if d.FastCGITimeout <= 0 {
		d.FastCGITimeout = 60
	}
}

func (d *PoolData) validate() error {
	if d.Site == nil || d.Pool == nil {
		return fmt.Errorf("site oder pool fehlt")
	}
	if !store.ValidDomain(d.Site.Domain) {
		return fmt.Errorf("domain %q ist ungültig", d.Site.Domain)
	}
	if !rePoolName.MatchString(d.Pool.PoolName) {
		return fmt.Errorf("poolname %q ist ungültig", d.Pool.PoolName)
	}
	if !rePHPVersion.MatchString(d.Pool.PHPVersion) {
		return fmt.Errorf("php-version %q ist ungültig", d.Pool.PHPVersion)
	}
	if !reSystemUser.MatchString(d.Site.SystemUser) {
		return fmt.Errorf("systembenutzer %q ist ungültig", d.Site.SystemUser)
	}
	switch d.Pool.PM {
	case "", "static", "dynamic", "ondemand":
	default:
		return fmt.Errorf("prozessmanager %q ist unbekannt", d.Pool.PM)
	}
	for name, p := range map[string]string{
		"root_path":   d.Site.RootPath,
		"socket_path": d.Pool.SocketPath,
		"log_dir":     d.LogDir,
	} {
		if err := checkPath(name, p); err != nil {
			return err
		}
	}
	for name, v := range map[string]string{
		"memory_limit":        d.Pool.MemoryLimit,
		"upload_max_filesize": d.Pool.UploadMaxFilesize,
	} {
		if v != "" && !reSize.MatchString(v) {
			return fmt.Errorf("%s %q ist ungültig", name, v)
		}
	}
	if d.Pool.MaxChildren < 1 || d.Pool.MaxChildren > 500 {
		return fmt.Errorf("max_children %d liegt außerhalb 1–500", d.Pool.MaxChildren)
	}
	// Beide Werte landen roh in der Pool-Datei — ein Zeilenumbruch mit
	// anschließender neuer "[poolname]"-Kopfzeile eröffnete sonst einen
	// vollständig neuen, unabhängigen PHP-FPM-Pool in derselben Datei.
	if err := checkINIValue("zusätzliche ini-einstellungen", d.Pool.ExtraINI); err != nil {
		return err
	}
	if err := checkINIValue("disable_functions", d.Pool.DisableFunctions); err != nil {
		return err
	}
	return nil
}

func (d *PoolData) applyDefaults() {
	d.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	if d.Pool.PM == "" {
		d.Pool.PM = "ondemand"
	}
	if d.Pool.MemoryLimit == "" {
		d.Pool.MemoryLimit = "256M"
	}
	if d.Pool.UploadMaxFilesize == "" {
		d.Pool.UploadMaxFilesize = "64M"
	}
	if d.Pool.MaxExecutionTime <= 0 {
		d.Pool.MaxExecutionTime = 30
	}
	if d.Timezone == "" {
		d.Timezone = "UTC"
	}
	if d.DisableFunctions == "" {
		d.DisableFunctions = defaultDisableFunctions
	}
	if d.Pool.DisableFunctions != "" {
		d.DisableFunctions = d.Pool.DisableFunctions
	}
	if d.OpenBasedir == "" {
		// Der Pool sieht sein eigenes Verzeichnis und sonst nichts.
		d.OpenBasedir = strings.Join([]string{
			d.Site.RootPath,
			d.Site.RootPath + "/tmp",
			"/usr/share/php",
		}, ":")
	}
	if d.Pool.OpenBasedir != "" {
		d.OpenBasedir = d.Pool.OpenBasedir
	}

	// Die Spare-Server-Werte müssen zueinander passen, sonst startet FPM nicht.
	d.MaxSpare = max(d.Pool.MaxChildren/2, 1)
	d.MinSpare = max(d.Pool.MaxChildren/4, 1)
	d.StartServers = max(min(d.MinSpare+1, d.MaxSpare), 1)
}
