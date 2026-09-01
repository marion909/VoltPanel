package transfer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// S3-kompatibler Speicher: AWS S3, Backblaze B2, MinIO, Hetzner, Wasabi.
//
// Signiert wird nach AWS Signature Version 4, von Hand. Ein SDK dafür
// einzubinden hiesse, für einen einzigen PUT ein paar hundert Pakete in den
// Bau zu holen — und die Signatur ist ein HMAC über eine festgelegte
// Zeichenkette, keine Wissenschaft.
//
// Was hier nicht steht, ist Absicht: kein Multipart, keine Wiederaufnahme, kein
// Auflisten mit Blättern. Ein Backup ist eine Datei, die einmal hochgeht.

const (
	s3Algorithm = "AWS4-HMAC-SHA256"
	s3Service   = "s3"

	// Ein PUT auf einmal. Fünf Gigabyte ist die Grenze von S3 selbst; darüber
	// verlangt es Multipart. Dann ist das Backup ohnehin zu gross für einen
	// Weg, der keine Wiederaufnahme kennt.
	s3MaxObject = 5 << 30
)

// S3Config beschreibt einen Speicherplatz. Nichts davon ist erraten: Endpunkt
// und Region stehen beim Anbieter, Schlüssel und Geheimnis legt der Kunde an.
type S3Config struct {
	// Endpoint ist der Host ohne Schema, z. B. "s3.eu-central-1.amazonaws.com"
	// oder "s3.eu-central-003.backblazeb2.com".
	Endpoint string
	Region   string
	Bucket   string
	// Prefix ist ein Verzeichnis im Bucket, ohne führenden Schrägstrich.
	Prefix    string
	AccessKey string
	Secret    string
	// PathStyle setzt den Bucket in den Pfad statt in den Hostnamen. MinIO und
	// die meisten Selbstbauten brauchen das; AWS und B2 nicht.
	PathStyle bool
}

// s3Client bündelt Konfiguration und einen HTTP-Client mit sicherem Wähler.
type s3Client struct {
	cfg  S3Config
	http *http.Client
}

func newS3Client(cfg S3Config, timeout time.Duration) *s3Client {
	return &s3Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext:           DialContext,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
				// Kein Proxy aus der Umgebung: der Agent und das Panel laufen
				// mit festem, minimalem Environment, und ein Proxy aus einer
				// Variablen wäre ein Weg an CheckAddr vorbei.
				Proxy: nil,
			},
		},
	}
}

// PutFile lädt eine Datei hoch und gibt den Schlüssel zurück, unter dem sie
// liegt.
//
// Der SHA-256 der Datei wird mitgeschickt (x-amz-content-sha256) — nicht
// UNSIGNED-PAYLOAD. Das kostet einen zweiten Durchlauf über die Datei und
// bringt dafür, dass der Speicher selbst merkt, wenn unterwegs etwas kippt.
func PutFile(ctx context.Context, cfg S3Config, localPath, name string,
	timeout time.Duration) (string, error) {

	if err := validateS3(cfg); err != nil {
		return "", err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("archiv öffnen: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > s3MaxObject {
		return "", fmt.Errorf("das archiv ist %d byte gross — mehr als %d gehen nur "+
			"in mehreren teilen, und das kann diese anbindung nicht",
			info.Size(), int64(s3MaxObject))
	}

	sum, err := sha256File(f)
	if err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	key := joinKey(cfg.Prefix, name)
	c := newS3Client(cfg, timeout)

	req, err := c.newRequest(ctx, http.MethodPut, key, f, info.Size(), sum)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/gzip")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("hochladen: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return "", s3Error(resp)
	}
	return key, nil
}

// Probe prüft eine Konfiguration, ohne etwas zu speichern.
//
// HEAD auf den Bucket: das beantwortet in einem Aufruf, ob der Endpunkt
// stimmt, ob die Schlüssel gelten und ob der Bucket existiert. Eine
// Testdatei hochzuladen und wieder zu löschen wäre die Alternative — sie
// hinterlässt bei einem Abbruch aber Müll im Bucket des Kunden.
func Probe(ctx context.Context, cfg S3Config, timeout time.Duration) error {
	if err := validateS3(cfg); err != nil {
		return err
	}
	c := newS3Client(cfg, timeout)

	req, err := c.newRequest(ctx, http.MethodHead, "", nil, 0, emptyPayloadHash)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("verbindung: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("der zugang wurde abgelehnt (403) — schlüssel, geheimnis "+
			"oder region passen nicht zu %s", cfg.Bucket)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("den bucket %q gibt es unter %s nicht", cfg.Bucket, cfg.Endpoint)
	default:
		return s3Error(resp)
	}
}

// emptyPayloadHash ist der SHA-256 der leeren Zeichenkette — der Wert, den
// AWS für eine Anfrage ohne Inhalt erwartet.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func (c *s3Client) newRequest(ctx context.Context, method, key string, body io.Reader,
	size int64, payloadHash string) (*http.Request, error) {

	host := c.cfg.Endpoint
	path := "/" + key
	if c.cfg.PathStyle {
		path = "/" + c.cfg.Bucket + "/" + key
	} else {
		host = c.cfg.Bucket + "." + c.cfg.Endpoint
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}

	raw := "https://" + host + encodePath(path)
	req, err := http.NewRequestWithContext(ctx, method, raw, body)
	if err != nil {
		return nil, err
	}
	if size > 0 {
		req.ContentLength = size
	}

	now := time.Now().UTC()
	req.Header.Set("Host", host)
	req.Host = host
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", now.Format("20060102T150405Z"))

	c.sign(req, now, payloadHash)
	return req, nil
}

// sign setzt den Authorization-Header nach AWS Signature Version 4.
//
// Die Reihenfolge ist vorgeschrieben und unversöhnlich: kanonische Anfrage →
// SHA-256 davon → "String to Sign" → abgeleiteter Schlüssel → HMAC. Weicht ein
// Zeichen ab, antwortet der Speicher mit 403 und sagt nicht, welches.
func (c *s3Client) sign(req *http.Request, now time.Time, payloadHash string) {
	date := now.Format("20060102")
	scope := strings.Join([]string{date, c.cfg.Region, s3Service, "aws4_request"}, "/")

	canonical, signed := canonicalRequest(req, payloadHash)

	toSign := strings.Join([]string{
		s3Algorithm,
		now.Format("20060102T150405Z"),
		scope,
		hex.EncodeToString(sha256Sum([]byte(canonical))),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+c.cfg.Secret), date)
	key = hmacSHA256(key, c.cfg.Region)
	key = hmacSHA256(key, s3Service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s3Algorithm, c.cfg.AccessKey, scope, signed, signature))
}

// canonicalRequest baut die kanonische Anfrage — die Zeichenkette, über die
// signiert wird. Sie ist als eigene Funktion herausgezogen, damit sie prüfbar
// ist: fast jeder Fehler in einer SigV4-Umsetzung sitzt hier, und der Speicher
// antwortet darauf mit einem 403, das nichts verrät.
func canonicalRequest(req *http.Request, payloadHash string) (string, string) {
	signed, headers := canonicalHeaders(req)
	return strings.Join([]string{
		req.Method,
		encodePath(req.URL.Path),
		canonicalQuery(req.URL.Query()),
		headers,
		signed,
		payloadHash,
	}, "\n"), signed
}

// canonicalHeaders liefert die signierten Header — klein geschrieben, sortiert,
// Werte auf einfache Leerzeichen normiert.
func canonicalHeaders(req *http.Request) (string, string) {
	names := []string{"host"}
	values := map[string]string{"host": req.Host}

	for name, vals := range req.Header {
		low := strings.ToLower(name)
		if !strings.HasPrefix(low, "x-amz-") && low != "content-type" {
			continue
		}
		names = append(names, low)
		values[low] = strings.Join(strings.Fields(strings.Join(vals, ",")), " ")
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(values[name])
		b.WriteByte('\n')
	}
	return strings.Join(names, ";"), b.String()
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, encodeSegment(k)+"="+encodeSegment(v))
		}
	}
	return strings.Join(parts, "&")
}

// encodePath kodiert jedes Segment einzeln — der Schrägstrich bleibt stehen.
func encodePath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = encodeSegment(seg)
	}
	return strings.Join(segments, "/")
}

// encodeSegment ist die Kodierung, die AWS verlangt: RFC 3986, und anders als
// url.QueryEscape wird das Leerzeichen zu %20, nicht zu +.
func encodeSegment(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if strings.IndexByte(unreserved, ch) >= 0 {
			b.WriteByte(ch)
			continue
		}
		b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{ch})))
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func sha256File(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("prüfsumme: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// joinKey setzt Präfix und Dateiname zusammen, ohne doppelte Schrägstriche.
func joinKey(prefix, name string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// s3Error macht aus der XML-Antwort eine Meldung, die weiterhilft.
//
// Der Text ist nicht schön, aber er nennt den Code (SignatureDoesNotMatch,
// NoSuchBucket, AccessDenied), und der ist die Antwort auf die Frage, was zu
// ändern ist.
func s3Error(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(body))
	if code := between(text, "<Code>", "</Code>"); code != "" {
		msg := between(text, "<Message>", "</Message>")
		return fmt.Errorf("der speicher antwortete mit %d %s: %s", resp.StatusCode, code, msg)
	}
	if text == "" {
		return fmt.Errorf("der speicher antwortete mit %d %s",
			resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return fmt.Errorf("der speicher antwortete mit %d: %s", resp.StatusCode, truncate(text, 300))
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// validateS3 fängt ab, was sonst erst der Speicher mit einem 403 beantwortet —
// und was zwischen einem Tippfehler und einer falschen Adresse nicht
// unterscheiden würde.
func validateS3(cfg S3Config) error {
	switch {
	case cfg.Endpoint == "":
		return fmt.Errorf("der endpunkt fehlt")
	case strings.Contains(cfg.Endpoint, "/"), strings.Contains(cfg.Endpoint, ":"):
		return fmt.Errorf("der endpunkt ist nur der host, ohne schema und ohne pfad "+
			"(z. B. s3.eu-central-1.amazonaws.com), nicht %q", cfg.Endpoint)
	case cfg.Bucket == "":
		return fmt.Errorf("der bucket fehlt")
	case cfg.Region == "":
		return fmt.Errorf("die region fehlt — sie steht in der signatur und muss " +
			"zu der des buckets passen")
	case cfg.AccessKey == "" || cfg.Secret == "":
		return fmt.Errorf("schlüssel oder geheimnis fehlen")
	}
	// Ein Header mit Zeilenumbruch wäre eine zweite Kopfzeile.
	for _, v := range []string{cfg.Endpoint, cfg.Bucket, cfg.Region, cfg.AccessKey, cfg.Prefix} {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("die angaben dürfen keine zeilenumbrüche enthalten")
		}
	}
	return nil
}

// ObjectName baut den Namen, unter dem ein Archiv abgelegt wird.
//
// Mit Datum im Pfad, damit die Ablage nach einem Jahr noch zu überblicken ist,
// und mit dem Namen der Datei, damit sie sich einem lokalen Archiv zuordnen
// lässt.
func ObjectName(when time.Time, filename string) string {
	return when.UTC().Format("2006/01") + "/" + filename
}

// MaxObjectSize nennt die Grenze für einen einzelnen Upload. Sie steht als
// Konstante hier und wird sonst nirgends wiederholt.
func MaxObjectSize() string { return strconv.FormatInt(s3MaxObject, 10) }
