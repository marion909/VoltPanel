package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marion909/voltpanel/internal/config"
)

// panelTLS baut die TLS-Konfiguration für das Panel.
//
// Das Panel terminiert selbst, statt sich hinter nginx zu stellen. Der Grund
// ist der Notfall: wer eine kaputte nginx-Konfiguration reparieren will,
// braucht das Panel gerade dann, wenn nginx nicht mehr ausliefert.
func panelTLS(cfg *config.Config, log *slog.Logger) (*tls.Config, error) {
	if err := ensureSelfSigned(cfg, log); err != nil {
		return nil, err
	}

	r := &certReloader{cfg: cfg, log: log}
	if _, err := r.load(); err != nil {
		return nil, err
	}

	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return r.load() },
	}, nil
}

// certReloader liest das Zertifikat bei jedem Handshake neu, solange sich die
// Datei geändert hat. Zwei stat-Aufrufe pro Verbindung sind billiger als der
// Neustart, den man sonst nach jeder Erneuerung bräuchte.
type certReloader struct {
	cfg *config.Config
	log *slog.Logger

	mu     sync.Mutex
	cached *tls.Certificate
	from   string
	stamp  time.Time
	size   int64
}

func (r *certReloader) load() (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var firstErr error
	for _, pair := range r.cfg.PanelTLSChain() {
		info, err := os.Stat(pair.Cert)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if _, err := os.Stat(pair.Key); err != nil {
			// Häufigster Fall: das echte Zertifikat liegt schon da, der
			// Schlüssel gehört aber noch root. Dann lieber weiter zum
			// selbstsignierten, als den Handshake scheitern zu lassen.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		if r.cached != nil && r.from == pair.Cert &&
			info.ModTime().Equal(r.stamp) && info.Size() == r.size {
			return r.cached, nil
		}

		cert, err := tls.LoadX509KeyPair(pair.Cert, pair.Key)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", pair.Cert, err)
			}
			continue
		}

		if r.from != pair.Cert {
			r.log.Info("panel-zertifikat übernommen", "datei", pair.Cert)
		}
		r.cached, r.from, r.stamp, r.size = &cert, pair.Cert, info.ModTime(), info.Size()
		return r.cached, nil
	}

	if r.cached != nil {
		// Lieber das alte Zertifikat weiterbenutzen als das Panel unerreichbar
		// machen — sonst sperrt ein misslungenes Erneuern genau den aus, der
		// es reparieren müsste.
		r.log.Warn("kein lesbares panel-zertifikat, behalte das bisherige", "err", firstErr)
		return r.cached, nil
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("kein zertifikat konfiguriert")
	}
	return nil, firstErr
}

// ensureSelfSigned legt das Notzertifikat an, falls noch keines existiert.
//
// Es ist nicht vertrauenswürdig und soll es auch nicht sein — es sorgt nur
// dafür, dass zwischen Installation und erstem `volt cert issue` niemand ein
// Passwort im Klartext über die Leitung schickt.
func ensureSelfSigned(cfg *config.Config, log *slog.Logger) error {
	pair := cfg.SelfSignedPanelCert()
	if _, err := os.Stat(pair.Cert); err == nil {
		if _, err := os.Stat(pair.Key); err == nil {
			return nil
		}
	}
	// Ein ausdrücklich gesetztes Zertifikat macht das Notzertifikat überflüssig.
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		if _, err := os.Stat(cfg.TLSCert); err == nil {
			return nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("schlüssel erzeugen: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("seriennummer: %w", err)
	}

	host := cfg.PanelDomain
	if host == "" {
		host, _ = os.Hostname()
	}
	if host == "" {
		host = "volt-panel"
	}

	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{"VoltPanel"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              dnsNames(host),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("zertifikat erzeugen: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("schlüssel kodieren: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(pair.Cert), 0o750); err != nil {
		return fmt.Errorf("zertifikatsverzeichnis: %w", err)
	}
	if err := writeFile(pair.Cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	if err := writeFile(pair.Key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}

	log.Info("selbstsigniertes panel-zertifikat erzeugt", "host", host, "datei", pair.Cert,
		"hinweis", "mit `volt cert issue "+host+"` durch ein gültiges ersetzen")
	return nil
}

func dnsNames(host string) []string {
	names := []string{host}
	if host != "localhost" {
		names = append(names, "localhost")
	}
	if h, err := os.Hostname(); err == nil && h != host && h != "" {
		names = append(names, h)
	}
	return names
}

// writeFile schreibt erst daneben und tauscht dann: ein abgebrochener
// Schreibvorgang darf kein halbes Zertifikat hinterlassen.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("%s schreiben: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s ablegen: %w", path, err)
	}
	return nil
}

// x509Leaf entpackt das Blattzertifikat eines geladenen Paares.
func x509Leaf(cert tls.Certificate) (*x509.Certificate, error) {
	if cert.Leaf != nil {
		return cert.Leaf, nil
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("zertifikat ohne inhalt")
	}
	return x509.ParseCertificate(cert.Certificate[0])
}
