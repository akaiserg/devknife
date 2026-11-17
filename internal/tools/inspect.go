package tools

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"

	"devknife/internal/core"
)

// ---------------------------------------------------------------------------
// Unix time

// now is swappable for tests.
var now = time.Now

func loadTZ(name string) (*time.Location, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "local":
		return time.Local, nil
	case "utc", "gmt", "z":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q (use an IANA name like Europe/Berlin)", name)
	}
	return loc, nil
}

var dateLayouts = []string{
	time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02T15:04",
	"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05 -0700 MST",
	"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
	time.RFC1123Z, time.RFC1123, time.RFC850, time.RFC822Z, time.RFC822, time.ANSIC, time.UnixDate, time.RubyDate,
	"Mon Jan 02 15:04:05 -0700 2006", "Mon, 2 Jan 2006 15:04:05 -0700", "2 Jan 2006 15:04:05", "02 Jan 2006", "Jan 2, 2006 15:04:05", "Jan 2, 2006 3:04 PM",
	"Jan 2, 2006", "January 2, 2006", "Monday, January 2, 2006", "Monday, Jan 2, 2006", "01/02/2006 15:04:05", "01/02/2006 15:04", "01/02/2006", "2006/01/02 15:04:05", "2006/01/02",
	"20060102T150405Z", "20060102",
}

var reDuration = regexp.MustCompile(`^(\d+(?:\.\d+)?)(ns|us|µs|ms|s|sec|m|min|h|hr|d|day|days|w|wk|weeks|y|yr|years)$`)

// timeExpr evaluates "1657272324 + 3600", "now - 2d", "(now+1w)*1000".
type timeExpr struct {
	toks []string
	pos  int
	now  float64 // in unit
	mult float64 // seconds → unit
	// depth counts nested factors: "(" and unary "-" recurse, and a stack
	// overflow is fatal, so (((… or ---… from a paste must be an error.
	depth int
}

func (e *timeExpr) peek() string {
	if e.pos < len(e.toks) {
		return e.toks[e.pos]
	}
	return ""
}

func (e *timeExpr) expr() (float64, error) {
	v, err := e.term()
	if err != nil {
		return 0, err
	}
	for e.peek() == "+" || e.peek() == "-" {
		op := e.toks[e.pos]
		e.pos++
		r, err := e.term()
		if err != nil {
			return 0, err
		}
		if op == "+" {
			v += r
		} else {
			v -= r
		}
	}
	return v, nil
}

func (e *timeExpr) term() (float64, error) {
	v, err := e.factor()
	if err != nil {
		return 0, err
	}
	for e.peek() == "*" || e.peek() == "/" {
		op := e.toks[e.pos]
		e.pos++
		r, err := e.factor()
		if err != nil {
			return 0, err
		}
		if op == "*" {
			v *= r
		} else {
			if r == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v /= r
		}
	}
	return v, nil
}

func (e *timeExpr) factor() (float64, error) {
	if e.depth++; e.depth > maxJSONDepth {
		return 0, fmt.Errorf("the expression is nested deeper than %d levels", maxJSONDepth)
	}
	defer func() { e.depth-- }()
	t := e.peek()
	e.pos++
	switch {
	case t == "(":
		v, err := e.expr()
		if err != nil {
			return 0, err
		}
		if e.peek() != ")" {
			return 0, fmt.Errorf("missing )")
		}
		e.pos++
		return v, nil
	case t == "-":
		v, err := e.factor()
		return -v, err
	case strings.EqualFold(t, "now"):
		return e.now, nil
	case reDuration.MatchString(strings.ToLower(t)):
		m := reDuration.FindStringSubmatch(strings.ToLower(t))
		n, _ := strconv.ParseFloat(m[1], 64)
		secs := map[string]float64{"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3, "s": 1, "sec": 1, "m": 60, "min": 60, "h": 3600, "hr": 3600,
			"d": 86400, "day": 86400, "days": 86400, "w": 604800, "wk": 604800, "weeks": 604800, "y": 31556952, "yr": 31556952, "years": 31556952}[m[2]]
		return n * secs * e.mult, nil
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected %q", t)
	}
	return v, nil
}

var reTimeTok = regexp.MustCompile(`\d+(?:\.\d+)?(?:ns|us|µs|ms|sec|min|hr|days|day|weeks|wk|years|yr|[smhdwy])?|now|[-+*/()]`)

func detectUnit(n float64) string {
	a := math.Abs(n)
	switch {
	case a < 1e11:
		return "seconds"
	case a < 1e14:
		return "milliseconds"
	case a < 1e17:
		return "microseconds"
	}
	return "nanoseconds"
}

var unitSecs = map[string]float64{"seconds": 1, "milliseconds": 1e3, "microseconds": 1e6, "nanoseconds": 1e9}

func parseTimeInput(in, unit string, loc *time.Location) (t time.Time, fromDate bool, usedUnit string, err error) {
	s := strings.TrimSpace(in)
	cur := now()
	if s == "" || strings.EqualFold(s, "now") {
		return cur, false, "seconds", nil
	}
	// plain integer: exact (no float rounding for ns timestamps)
	if reDigits.MatchString(s) {
		n, perr := strconv.ParseInt(s, 10, 64)
		if perr == nil {
			u := unit
			if u == "auto" {
				u = detectUnit(float64(n))
			}
			switch u {
			case "milliseconds":
				return time.UnixMilli(n), false, u, nil
			case "microseconds":
				return time.UnixMicro(n), false, u, nil
			case "nanoseconds":
				return time.Unix(0, n), false, u, nil
			}
			return time.Unix(n, 0), false, u, nil
		}
	}
	for _, l := range dateLayouts {
		if tt, perr := time.ParseInLocation(l, s, loc); perr == nil {
			return tt, true, "", nil
		}
	}
	toks := reTimeTok.FindAllString(s, -1)
	if strings.Join(toks, "") != strings.ReplaceAll(s, " ", "") {
		return time.Time{}, false, "", fmt.Errorf("cannot parse %q as a timestamp, date, or expression", s)
	}
	u := unit
	if u == "auto" {
		u = "seconds"
		for _, tk := range toks {
			if n, perr := strconv.ParseFloat(tk, 64); perr == nil {
				u = detectUnit(n)
				break
			}
		}
	}
	mult := unitSecs[u]
	e := &timeExpr{toks: toks, now: float64(cur.UnixNano()) / 1e9 * mult, mult: mult}
	v, err := e.expr()
	if err != nil {
		return time.Time{}, false, "", err
	}
	if e.pos != len(toks) {
		return time.Time{}, false, "", fmt.Errorf("unexpected %q", toks[e.pos])
	}
	secs := v / mult
	whole := math.Floor(secs)
	return time.Unix(int64(whole), int64((secs-whole)*1e9)), false, u, nil
}

func relative(t, ref time.Time) string {
	d := ref.Sub(t)
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < time.Second:
		return "now"
	case d < time.Minute:
		s = fmt.Sprintf("%d sec", int(d.Seconds()))
	case d < time.Hour:
		s = fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		s = plural(int(d.Hours()), "hour")
	case d < 60*24*time.Hour:
		s = plural(int(d.Hours()/24), "day")
	case d < 730*24*time.Hour:
		s = plural(int(d.Hours()/24/30.44), "month")
	default:
		s = fmt.Sprintf("%.1f years", d.Hours()/24/365.25)
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

func isLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

func unixTimeTool() *core.Tool {
	return &core.Tool{
		ID:          "time",
		Name:        "Unix Time Converter",
		Category:    core.CatInspect,
		Description: "Convert Unix timestamps (s/ms/µs/ns auto-detected) and dates in almost any format. Supports math like “now - 2d” or “1700000000 + 3600”, and any time zone.",
		Aliases:     []string{"unixtime", "timestamp", "epoch", "date"},
		Keywords:    []string{"unix", "epoch", "iso8601", "rfc3339", "timezone", "date"},
		Input:       core.InputOptional,
		Placeholder: "1657272324, 2024-05-01T10:00:00Z, now + 3d … (empty = now)",
		Options: []core.Option{
			{Key: "unit", Label: "Input unit", Type: core.OptSelect, Default: "auto", Choices: []string{"auto", "seconds", "milliseconds", "microseconds", "nanoseconds"}, Short: "u"},
			{Key: "tz", Label: "Time zone", Type: core.OptString, Default: "local", Placeholder: "local, UTC, America/New_York…", Short: "z"},
		},
		Sample: "1657272324",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if reDigits.MatchString(t) && !strings.HasPrefix(t, "-") {
				n, _ := strconv.ParseInt(t, 10, 64)
				switch len(t) {
				case 10:
					if n > 315532800 && n < 4102444800 { // 1980–2100
						return det(95, "Unix timestamp (seconds)")
					}
				case 13:
					return det(95, "Unix timestamp (milliseconds)")
				case 16:
					return det(85, "Unix timestamp (microseconds)")
				case 19:
					return det(85, "Unix timestamp (nanoseconds)")
				}
				return nil
			}
			if len(t) < 40 && reISODateTime.MatchString(t) {
				return det(85, "ISO 8601 date")
			}
			if len(t) < 50 {
				for _, l := range []string{time.RFC1123, time.RFC1123Z, time.UnixDate, time.RFC850} {
					if _, err := time.Parse(l, t); err == nil {
						return det(85, "Date")
					}
				}
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			loc, err := loadTZ(r.Str("tz"))
			if err != nil {
				return nil, err
			}
			t, fromDate, unit, err := parseTimeInput(r.Input, r.Str("unit"), loc)
			if err != nil {
				return nil, err
			}
			lt := t.In(loc)
			_, week := lt.ISOWeek()
			zoneLabel := "Local"
			if loc != time.Local {
				zoneLabel = loc.String()
			}
			res := &core.Result{}
			if fromDate {
				res.Output = strconv.FormatInt(t.Unix(), 10)
			} else {
				res.Output = t.UTC().Format(time.RFC3339Nano)
			}
			res.Fields = fields(
				zoneLabel, lt.Format("Mon Jan 02 15:04:05 -0700 2006"),
				"UTC (ISO 8601)", t.UTC().Format(time.RFC3339Nano),
				"Relative", relative(t, now()),
				"Unix time (s)", strconv.FormatInt(t.Unix(), 10),
				"Unix time (ms)", strconv.FormatInt(t.UnixMilli(), 10),
				"Day of year", strconv.Itoa(lt.YearDay()),
				"Week of year (ISO)", strconv.Itoa(week),
				"Is leap year?", strconv.FormatBool(isLeap(lt.Year())),
				"RFC 2822", lt.Format(time.RFC1123Z),
				"RFC 3339 ("+zoneLabel+")", lt.Format(time.RFC3339),
				"Long date", lt.Format("Monday, Jan 2, 2006"),
				"US date", lt.Format("01/02/2006"),
				"ISO date", lt.Format("2006-01-02"),
				"Date + time", lt.Format("01-02-2006 15:04"),
				"Short", lt.Format("Jan 2, 3:04 PM"),
				"Month", lt.Format("January 2006"),
				"Time", lt.Format("15:04:05.000"),
			)
			if unit != "" && !fromDate && strings.TrimSpace(r.Input) != "" {
				res.Fields = append(res.Fields, core.Field{Label: "Interpreted as", Value: unit})
			}
			return res, nil
		},
	}
}

// ---------------------------------------------------------------------------
// JWT

func b64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

var jwtHashes = map[string]crypto.Hash{"256": crypto.SHA256, "384": crypto.SHA384, "512": crypto.SHA512}

func parseKey(pemText string, private bool) (any, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemText)))
	if block == nil {
		return nil, fmt.Errorf("expected a PEM-encoded key")
	}
	if private {
		if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			return k, nil
		}
		if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return k, nil
		}
		if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
			return k, nil
		}
		return nil, fmt.Errorf("unsupported private key format")
	}
	switch block.Type {
	case "CERTIFICATE":
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		return c.PublicKey, nil
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	}
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return k, nil
	}
	// a private key also verifies
	if k, err := parseKey(pemText, true); err == nil {
		switch pk := k.(type) {
		case *rsa.PrivateKey:
			return &pk.PublicKey, nil
		case *ecdsa.PrivateKey:
			return &pk.PublicKey, nil
		case ed25519.PrivateKey:
			return pk.Public(), nil
		}
	}
	return nil, fmt.Errorf("unsupported public key format")
}

// isPEMKey reports whether s is a PEM block (a key or certificate), which is
// never a legitimate HMAC secret.
func isPEMKey(s string) (string, bool) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(s)))
	if block == nil {
		return "", false
	}
	return strings.ToLower(block.Type), true
}

// ecCurveFor is the curve RFC 7518 §3.4 pairs with each ES* algorithm.
var ecCurveFor = map[string]elliptic.Curve{"ES256": elliptic.P256(), "ES384": elliptic.P384(), "ES512": elliptic.P521()}

func checkECCurve(alg string, c elliptic.Curve) error {
	want, ok := ecCurveFor[alg]
	if !ok {
		return fmt.Errorf("unsupported alg %q", alg)
	}
	if c.Params().Name != want.Params().Name {
		return fmt.Errorf("%s needs a %s key, but this key uses %s", alg, want.Params().Name, c.Params().Name)
	}
	return nil
}

// jwtVerify checks a signature. It returns nil only for a valid signature
// made with the algorithm the header claims; an unsigned token (alg none) is
// never "verified" and callers report it separately.
func jwtVerify(alg, signingInput string, sig []byte, secret string, secretB64 bool) error {
	if strings.EqualFold(alg, "none") {
		return fmt.Errorf("the token is not signed (alg: none)")
	}
	if len(alg) < 5 {
		return fmt.Errorf("unsupported alg %q", alg)
	}
	h, ok := jwtHashes[alg[2:]]
	if !ok && alg != "EdDSA" {
		return fmt.Errorf("unsupported alg %q", alg)
	}
	switch {
	case strings.HasPrefix(alg, "HS"):
		key := []byte(secret)
		if secretB64 {
			k, err := decodeBase64Loose(secret)
			if err != nil {
				return fmt.Errorf("secret is not valid base64")
			}
			key = k
		} else if kind, ok := isPEMKey(secret); ok {
			// RS256 → HS256 "algorithm confusion": an attacker signs with the
			// server's public key as the HMAC secret. A match proves nothing.
			return fmt.Errorf("the key is a PEM %s, but the token claims %s (an HMAC algorithm). Using a public key as an HMAC secret is the classic algorithm-confusion forgery, so this signature is not trusted", kind, alg)
		}
		mac := hmac.New(h.New, key)
		mac.Write([]byte(signingInput))
		if !hmac.Equal(mac.Sum(nil), sig) {
			return fmt.Errorf("invalid signature")
		}
		return nil
	}
	pub, err := parseKey(secret, false)
	if err != nil {
		return err
	}
	if alg == "EdDSA" {
		k, ok := pub.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("EdDSA needs an Ed25519 public key")
		}
		if !ed25519.Verify(k, []byte(signingInput), sig) {
			return fmt.Errorf("invalid signature")
		}
		return nil
	}
	hh := h.New()
	hh.Write([]byte(signingInput))
	digest := hh.Sum(nil)
	switch {
	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "PS"):
		k, ok := pub.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%s needs an RSA public key", alg)
		}
		if strings.HasPrefix(alg, "RS") {
			err = rsa.VerifyPKCS1v15(k, h, digest, sig)
		} else {
			// RFC 7518 §3.5: the salt is as long as the hash.
			err = rsa.VerifyPSS(k, h, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			if err != nil && rsa.VerifyPSS(k, h, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto}) == nil {
				return fmt.Errorf("the signature matches the key, but its PSS salt length is not the hash length that RFC 7518 requires; strict verifiers will reject it")
			}
		}
		if err != nil {
			return fmt.Errorf("invalid signature")
		}
		return nil
	case strings.HasPrefix(alg, "ES"):
		k, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%s needs an EC public key", alg)
		}
		if err := checkECCurve(alg, k.Curve); err != nil {
			return err
		}
		// RFC 7518 §3.4: R and S, each zero-padded to the curve size.
		n := (k.Curve.Params().BitSize + 7) / 8
		if len(sig) != 2*n {
			return fmt.Errorf("invalid signature: %s signatures are %d bytes, this one is %d", alg, 2*n, len(sig))
		}
		rr, ss := new(big.Int).SetBytes(sig[:n]), new(big.Int).SetBytes(sig[n:])
		if !ecdsa.Verify(k, digest, rr, ss) {
			return fmt.Errorf("invalid signature")
		}
		return nil
	}
	return fmt.Errorf("unsupported alg %q", alg)
}

func jwtSign(alg, signingInput, secret string, secretB64 bool) ([]byte, error) {
	if alg == "none" {
		return nil, nil
	}
	h, ok := jwtHashes[strings.TrimLeft(alg, "HSRPE")]
	if !ok && alg != "EdDSA" {
		return nil, fmt.Errorf("unsupported alg %q", alg)
	}
	if strings.HasPrefix(alg, "HS") {
		key := []byte(secret)
		if secretB64 {
			k, err := decodeBase64Loose(secret)
			if err != nil {
				return nil, fmt.Errorf("secret is not valid base64")
			}
			key = k
		}
		if len(key) == 0 {
			return nil, fmt.Errorf("enter a secret to sign with")
		}
		mac := hmac.New(h.New, key)
		mac.Write([]byte(signingInput))
		return mac.Sum(nil), nil
	}
	priv, err := parseKey(secret, true)
	if err != nil {
		return nil, fmt.Errorf("%s needs a PEM private key: %v", alg, err)
	}
	if alg == "EdDSA" {
		k, ok := priv.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("EdDSA needs an Ed25519 private key")
		}
		return ed25519.Sign(k, []byte(signingInput)), nil
	}
	hh := h.New()
	hh.Write([]byte(signingInput))
	digest := hh.Sum(nil)
	switch {
	case strings.HasPrefix(alg, "RS"):
		k, ok := priv.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s needs an RSA private key", alg)
		}
		return rsa.SignPKCS1v15(rand.Reader, k, h, digest)
	case strings.HasPrefix(alg, "PS"):
		k, ok := priv.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s needs an RSA private key", alg)
		}
		// RFC 7518 §3.5 fixes the salt length to the hash length; Go's
		// default (nil options) uses the maximum, which strict verifiers reject.
		return rsa.SignPSS(rand.Reader, k, h, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	case strings.HasPrefix(alg, "ES"):
		k, ok := priv.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s needs an EC private key", alg)
		}
		if err := checkECCurve(alg, k.Curve); err != nil {
			return nil, err
		}
		rr, ss, err := ecdsa.Sign(rand.Reader, k, digest)
		if err != nil {
			return nil, err
		}
		size := (k.Curve.Params().BitSize + 7) / 8
		out := make([]byte, 2*size)
		rr.FillBytes(out[:size])
		ss.FillBytes(out[size:])
		return out, nil
	}
	return nil, fmt.Errorf("unsupported alg %q", alg)
}

func claimTime(m *OMap, k string) (time.Time, bool) {
	v, ok := m.Get(k)
	if !ok {
		return time.Time{}, false
	}
	n, ok := v.(json.Number)
	if !ok {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

var reJWT = regexp.MustCompile(`^[A-Za-z0-9_-]{2,}\.[A-Za-z0-9_-]{2,}\.[A-Za-z0-9_-]*$`)

func jwtTool() *core.Tool {
	return &core.Tool{
		ID:          "jwt",
		Name:        "JWT Debugger",
		Category:    core.CatInspect,
		Description: "Decode JSON Web Tokens, check expiry, and verify signatures (HS*, RS*, PS*, ES*, EdDSA) — or sign a new token from a JSON payload.",
		Aliases:     []string{"jwt-debugger", "token"},
		Keywords:    []string{"json web token", "bearer", "oauth", "claims", "signature", "verify", "sign"},
		Options: []core.Option{
			optMode("decode", "decode", "encode"),
			{Key: "secret", Label: "Secret / key", Type: core.OptText, Default: "", Placeholder: "HMAC secret, or a PEM public key (verify) / private key (sign)", Short: "k", Sensitive: true},
			{Key: "secret_b64", Label: "Secret is base64", Type: core.OptBool, Default: "false"},
			{Key: "alg", Label: "Algorithm (sign)", Type: core.OptSelect, Default: "HS256", Choices: []string{"HS256", "HS384", "HS512", "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA", "none"}},
		},
		Sample: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		Detect: func(in string) *core.Detection {
			t := strings.TrimPrefix(strings.TrimSpace(in), "Bearer ")
			if !reJWT.MatchString(t) || !strings.HasPrefix(t, "eyJ") {
				return nil
			}
			h, err := b64urlDecode(strings.SplitN(t, ".", 2)[0])
			if err == nil && json.Valid(h) {
				return det(98, "JSON Web Token", "mode", "decode")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			if r.Str("mode") == "encode" {
				return signJWT(r)
			}
			tok := strings.TrimSpace(r.Input)
			tok = strings.TrimPrefix(strings.TrimPrefix(tok, "Bearer "), "bearer ")
			parts := strings.Split(tok, ".")
			if len(parts) == 5 {
				return nil, fmt.Errorf("this is an encrypted JWE (5 parts); only signed JWS tokens can be decoded")
			}
			if len(parts) != 3 {
				return nil, fmt.Errorf("a JWT has 3 dot-separated parts; found %d", len(parts))
			}
			hb, err := b64urlDecode(parts[0])
			if err != nil {
				return nil, fmt.Errorf("header is not valid base64url")
			}
			pb, err := b64urlDecode(parts[1])
			if err != nil {
				return nil, fmt.Errorf("payload is not valid base64url")
			}
			hv, err := decodeOrdered(hb)
			if err != nil {
				return nil, fmt.Errorf("header is not valid JSON")
			}
			hdr, _ := hv.(*OMap)
			pv, err := decodeOrdered(pb)
			if err != nil {
				return nil, fmt.Errorf("payload is not valid JSON")
			}
			payload, _ := pv.(*OMap)
			sig, err := b64urlDecode(parts[2])
			if err != nil {
				return nil, fmt.Errorf("signature is not valid base64url")
			}
			alg := ""
			if hdr != nil {
				if a, ok := hdr.Get("alg"); ok {
					alg, _ = a.(string)
				}
			}
			status, msg := "unverified", "Enter the secret or public key to verify the signature"
			switch {
			case strings.EqualFold(alg, "none"):
				// Never "verified": an unsigned token proves nothing about who made it.
				if len(sig) > 0 {
					status, msg = "invalid", "The header says alg: none, but a signature is present"
				} else {
					status, msg = "unsigned", "Token is not signed (alg: none): anyone can create or change it"
				}
			case r.Str("secret") != "":
				if err := jwtVerify(alg, parts[0]+"."+parts[1], sig, r.Str("secret"), r.Bool("secret_b64")); err != nil {
					status, msg = "invalid", capFirst(err.Error())
				} else {
					status, msg = "verified", "Signature verified"
				}
			}
			out := newOMap()
			out.Set("header", hv)
			out.Set("payload", pv)
			res := &core.Result{Output: encodeOrdered(out, "  ", false), Lang: "json", View: "jwt"}
			res.Fields = fields("Algorithm", alg)
			str := func(k string) string {
				if payload == nil {
					return ""
				}
				if v, ok := payload.Get(k); ok {
					if s, ok := v.(string); ok {
						return s
					}
					return encodeOrdered(v, "", false)
				}
				return ""
			}
			if hdr != nil {
				if v, ok := hdr.Get("kid"); ok {
					res.Fields = append(res.Fields, core.Field{Label: "Key ID", Value: fmt.Sprint(v)})
				}
			}
			for _, kv := range [][2]string{{"iss", "Issuer"}, {"sub", "Subject"}, {"aud", "Audience"}, {"jti", "JWT ID"}} {
				if v := str(kv[0]); v != "" {
					res.Fields = append(res.Fields, core.Field{Label: kv[1], Value: v})
				}
			}
			cur := now()
			if payload != nil {
				if t, ok := claimTime(payload, "iat"); ok {
					res.Fields = append(res.Fields, core.Field{Label: "Issued at", Value: t.Local().Format(time.RFC3339) + " (" + relative(t, cur) + ")"})
				}
				if t, ok := claimTime(payload, "nbf"); ok {
					v := t.Local().Format(time.RFC3339) + " (" + relative(t, cur) + ")"
					if cur.Before(t) {
						v += " — NOT YET VALID"
					}
					res.Fields = append(res.Fields, core.Field{Label: "Not before", Value: v})
				}
				if t, ok := claimTime(payload, "exp"); ok {
					v := t.Local().Format(time.RFC3339) + " (" + relative(t, cur) + ")"
					if cur.After(t) {
						v += " — EXPIRED"
						res.Warnings = append(res.Warnings, "Token expired "+relative(t, cur))
					}
					res.Fields = append(res.Fields, core.Field{Label: "Expires", Value: v})
				}
			}
			res.Fields = append(res.Fields, core.Field{Label: "Signature", Value: msg})
			res.Data = map[string]any{
				"parts":   parts,
				"header":  encodeOrdered(hv, "  ", false),
				"payload": encodeOrdered(pv, "  ", false),
				"status":  status,
				"message": msg,
				"alg":     alg,
			}
			return res, nil
		},
	}
}

func signJWT(r core.Request) (*core.Result, error) {
	v, _, err := parseJSONInput(r.Input, true)
	if err != nil {
		return nil, fmt.Errorf("payload: %v", err)
	}
	if _, ok := v.(*OMap); !ok {
		return nil, fmt.Errorf("payload must be a JSON object")
	}
	alg := r.Str("alg")
	hdr := newOMap()
	hdr.Set("alg", alg)
	hdr.Set("typ", "JWT")
	signingInput := base64.RawURLEncoding.EncodeToString([]byte(encodeOrdered(hdr, "", false))) + "." + base64.RawURLEncoding.EncodeToString([]byte(encodeOrdered(v, "", false)))
	sig, err := jwtSign(alg, signingInput, r.Str("secret"), r.Bool("secret_b64"))
	if err != nil {
		return nil, err
	}
	tok := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
	res := &core.Result{Output: tok, Fields: fields("Algorithm", alg, "Length", strconv.Itoa(len(tok)))}
	if kind, ok := isPEMKey(r.Str("secret")); ok && strings.HasPrefix(alg, "HS") && !r.Bool("secret_b64") {
		res.Warnings = append(res.Warnings, fmt.Sprintf("The secret is a PEM %s used as an HMAC key. That is how algorithm-confusion forgeries are made; for a key pair, sign with RS*, PS*, ES* or EdDSA.", kind))
	}
	if alg == "none" {
		res.Warnings = append(res.Warnings, "This token is unsigned (alg: none); verifiers should reject it.")
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// RegExp

type reGroup struct {
	Name string `json:"name,omitempty"`
	S    int    `json:"s"`
	E    int    `json:"e"`
	Text string `json:"text"`
}

type reMatch struct {
	S      int       `json:"s"` // UTF-16 offsets for the browser
	E      int       `json:"e"`
	Text   string    `json:"text"`
	Groups []reGroup `json:"groups,omitempty"`
}

var reJSLiteral = regexp.MustCompile(`^/(.+)/([a-z]*)$`)

func utf16Offsets(s string) func(int) int {
	// map byte offset → UTF-16 offset, computed lazily in one pass
	idx := make([]int, len(s)+1)
	u := 0
	for i, r := range s {
		for j := i; j < i+utf8.RuneLen(r) && j < len(s); j++ {
			idx[j] = u
		}
		if r >= 0x10000 {
			u += 2
		} else {
			u++
		}
	}
	idx[len(s)] = u
	return func(b int) int { return idx[b] }
}

func regexTool() *core.Tool {
	return &core.Tool{
		ID:          "regex",
		Name:        "RegExp Tester",
		Category:    core.CatInspect,
		Description: "Test regular expressions (Go RE2 syntax) with live match highlighting, capture groups, replace, split, and an output template like $1\\n. JS-style /pattern/gi literals and $<name> references are accepted.",
		Aliases:     []string{"regexp", "re", "grep"},
		Keywords:    []string{"regular expression", "match", "replace", "capture", "pattern"},
		Args:        []string{"pattern", "input"},
		Options: []core.Option{
			{Key: "pattern", Label: "Pattern", Type: core.OptString, Default: "", Placeholder: `([A-Z])\w+`, Short: "e"},
			optMode("matches", "matches", "replace", "template", "split"),
			{Key: "replacement", Label: "Replace / template", Type: core.OptString, Default: "", Placeholder: `$1 — or ${name}; \n = newline`, Short: "r"},
			{Key: "ignore_case", Label: "i", Type: core.OptBool, Default: "false", Help: "Case-insensitive"},
			{Key: "multiline", Label: "m", Type: core.OptBool, Default: "false", Help: "^ and $ match at line breaks"},
			{Key: "dotall", Label: "s", Type: core.OptBool, Default: "false", Help: ". matches newlines"},
		},
		Sample: "DevUtils helps you with your tiny daily tasks. It works entirely offline!\nStop pasting your JSON strings, JWT tokens, or any sensitive data to random websites.\nContact: ada@example.com, linus@example.org — call +1 (555) 010-0199.",
		Templates: []core.Template{
			{Name: "Capitalized words", Opts: map[string]string{"pattern": `\b[A-Z]\w+`}},
			{Name: "Email address", Opts: map[string]string{"pattern": `[\w.+-]+@[\w-]+\.[\w.-]+`}},
			{Name: "URL", Opts: map[string]string{"pattern": `https?://[^\s"'<>]+`}},
			{Name: "IPv4 address", Opts: map[string]string{"pattern": `\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`}},
			{Name: "UUID", Opts: map[string]string{"pattern": `(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`}},
			{Name: "Hex color", Opts: map[string]string{"pattern": `#(?:[0-9a-fA-F]{3}){1,2}\b`}},
			{Name: "ISO date", Opts: map[string]string{"pattern": `\b(?P<year>\d{4})-(?P<month>\d{2})-(?P<day>\d{2})\b`}},
			{Name: "Semantic version", Opts: map[string]string{"pattern": `\bv?(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*)\.(?P<patch>0|[1-9]\d*)(?:-[\w.]+)?\b`}},
			{Name: "Phone number", Opts: map[string]string{"pattern": `\+?\d{1,3}[\s.-]?\(?\d{3}\)?[\s.-]?\d{3}[\s.-]?\d{4}`}},
			{Name: "Quoted string", Opts: map[string]string{"pattern": `"(?:[^"\\]|\\.)*"`}},
		},
		Run: func(r core.Request) (*core.Result, error) {
			pat := r.Str("pattern")
			if pat == "" {
				return nil, fmt.Errorf("enter a regular expression")
			}
			flags := ""
			if m := reJSLiteral.FindStringSubmatch(pat); m != nil {
				pat = m[1]
				for _, f := range m[2] {
					if strings.ContainsRune("ims", f) {
						flags += string(f)
					}
				}
			}
			if r.Bool("ignore_case") && !strings.Contains(flags, "i") {
				flags += "i"
			}
			if r.Bool("multiline") && !strings.Contains(flags, "m") {
				flags += "m"
			}
			if r.Bool("dotall") && !strings.Contains(flags, "s") {
				flags += "s"
			}
			// accept JS named groups (?<name>...)
			pat = reJSNamedGroup.ReplaceAllString(pat, "(?P<$1>")
			full := pat
			if flags != "" {
				full = "(?" + flags + ")" + pat
			}
			re, err := regexp.Compile(full)
			if err != nil {
				msg := strings.TrimPrefix(err.Error(), "error parsing regexp: ")
				if strings.Contains(msg, "Perl syntax") || strings.Contains(msg, "escape sequence: `\\") && reBackref.MatchString(pat) {
					msg += " — Go uses RE2, which has no lookahead/lookbehind or backreferences"
				}
				return nil, fmt.Errorf("invalid pattern: %s", msg)
			}
			in := r.Input
			tmpl := r.Str("replacement")
			tmpl = reJSNamedRef.ReplaceAllString(tmpl, "$${$1}")
			tmpl = strings.ReplaceAll(tmpl, "$&", "${0}")
			if t2, err := unescapeBackslash(tmpl); err == nil {
				tmpl = t2
			}
			locs := re.FindAllStringSubmatchIndex(in, 10000)
			conv := utf16Offsets(in)
			names := re.SubexpNames()
			matches := make([]reMatch, 0, len(locs))
			for _, l := range locs {
				m := reMatch{S: conv(l[0]), E: conv(l[1]), Text: in[l[0]:l[1]]}
				for g := 1; g*2 < len(l); g++ {
					if l[g*2] < 0 {
						m.Groups = append(m.Groups, reGroup{Name: names[g], S: -1, E: -1})
						continue
					}
					m.Groups = append(m.Groups, reGroup{Name: names[g], S: conv(l[g*2]), E: conv(l[g*2+1]), Text: in[l[g*2]:l[g*2+1]]})
				}
				matches = append(matches, m)
			}
			res := &core.Result{View: "regex", Data: map[string]any{"matches": matches, "pattern": full}}
			switch r.Str("mode") {
			case "replace":
				out, err := regexReplace(re, in, tmpl)
				if err != nil {
					return nil, err
				}
				res.Output = out
			case "template":
				if tmpl == "" {
					tmpl = "${0}\n"
				}
				out, err := regexExpand(re, in, tmpl, locs, false)
				if err != nil {
					return nil, err
				}
				res.Output = out
			case "split":
				if n := len(re.FindAllStringIndex(in, maxRegexOps+1)); n > maxRegexOps {
					return nil, fmt.Errorf("the pattern matches more than %d times, which is the most split handles here; for bigger jobs use a shell tool", maxRegexOps)
				}
				res.Output = strings.Join(re.Split(in, -1), "\n")
			default:
				lines := make([]string, len(matches))
				for i, m := range matches {
					lines[i] = m.Text
				}
				res.Output = strings.Join(lines, "\n")
			}
			res.Fields = fields("Matches", strconv.Itoa(len(matches)), "Groups", strconv.Itoa(re.NumSubexp()), "Go pattern", full)
			if len(locs) == 10000 {
				res.Warnings = []string{"Stopped after 10,000 matches"}
			}
			return res, nil
		},
	}
}

// maxRegexOps bounds how many matches replace and split process. Every match
// is held in memory, and a pattern that can match empty (x*) matches at every
// position of the input.
const maxRegexOps = 1_000_000

// regexReplace is re.ReplaceAllString with limits. FindAll and ReplaceAll
// visit the same matches (both skip an empty match right after a previous
// match), so the result is identical.
func regexReplace(re *regexp.Regexp, in, tmpl string) (string, error) {
	locs := re.FindAllStringSubmatchIndex(in, maxRegexOps+1)
	if len(locs) > maxRegexOps {
		return "", fmt.Errorf("the pattern matches more than %d times, which is the most replace handles here; for bigger jobs use sed", maxRegexOps)
	}
	return regexExpand(re, in, tmpl, locs, true)
}

// regexExpand expands tmpl for each match; withGaps keeps the text between
// matches (replace) instead of only the expansions (template). Replacing an
// empty-match pattern (x*) with a long template multiplies the input size,
// so the exact result size is measured first and the result is only built
// when it fits: failing after allocating hundreds of MB would be too late.
func regexExpand(re *regexp.Regexp, in, tmpl string, locs [][]int, withGaps bool) (string, error) {
	refs := strings.Count(tmpl, "$")
	total := 0
	if withGaps {
		total = len(in)
		for _, l := range locs {
			total -= l[1] - l[0]
		}
	}
	var scratch []byte
	for _, l := range locs {
		// each $reference expands to at most the whole match (groups lie inside it)
		if bound := len(tmpl) + refs*(l[1]-l[0]); bound > core.MaxOutput {
			return "", core.OutputTooLarge(bound, core.MaxOutput)
		}
		scratch = re.ExpandString(scratch[:0], tmpl, in, l)
		if total += len(scratch); total > core.MaxOutput {
			return "", core.OutputTooLarge(total, core.MaxOutput)
		}
	}
	b := make([]byte, 0, total)
	last := 0
	for _, l := range locs {
		if withGaps {
			b = append(b, in[last:l[0]]...)
		}
		b = re.ExpandString(b, tmpl, in, l)
		last = l[1]
	}
	if withGaps {
		b = append(b, in[last:]...)
	}
	return string(b), nil
}

// ---------------------------------------------------------------------------
// HTML & Markdown preview

func htmlPreviewTool() *core.Tool {
	return &core.Tool{
		ID:          "html-preview",
		Name:        "HTML Preview",
		Category:    core.CatInspect,
		Description: "Render HTML in an isolated sandbox. Scripts are off by default; enable them per preview. From the CLI, use --open to view it in your browser.",
		Aliases:     []string{"preview"},
		Keywords:    []string{"render", "browser", "iframe", "sandbox"},
		Options: []core.Option{
			{Key: "scripts", Label: "Allow scripts", Type: core.OptBool, Default: "false"},
			optRemote(),
		},
		Sample: `<style>body{font-family:system-ui;padding:24px}.card{border:1px solid #ddd;border-radius:12px;padding:16px;max-width:360px;box-shadow:0 2px 8px #0001}button{background:#4f46e5;color:#fff;border:0;border-radius:8px;padding:8px 14px}</style>
<div class="card"><h2>Hello 👋</h2><p>This HTML renders in a sandboxed frame.</p><button onclick="this.textContent='Clicked!'">Click me</button></div>`,
		Detect: func(in string) *core.Detection {
			if reHTMLDoc.MatchString(in) || (looksLikeMarkup(in) && reHTMLTags.MatchString(in)) {
				return det(55, "HTML")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			return &core.Result{Output: r.Input, Lang: "html", HTML: r.Input, View: "preview", Data: map[string]any{"scripts": r.Bool("scripts"), "remote": r.Bool("remote")}}, nil
		},
	}
}

const markdownCSS = `<style>
:root{color-scheme:light dark}
body{font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif;max-width:860px;margin:0 auto;padding:24px;color:#1f2328;background:#fff}
@media (prefers-color-scheme:dark){body{color:#e6edf3;background:#0d1117}a{color:#4493f8}code,pre{background:#161b22!important}th,td{border-color:#30363d!important}blockquote{color:#9198a1!important;border-color:#30363d!important}hr{background:#30363d!important}}
h1,h2{border-bottom:1px solid #8884;padding-bottom:.3em}a{color:#0969da}
code{font:13px ui-monospace,SFMono-Regular,Menlo,monospace;background:#f6f8fa;padding:.2em .4em;border-radius:6px}
pre{background:#f6f8fa;padding:16px;border-radius:6px;overflow:auto}pre code{padding:0;background:none}
blockquote{margin:0;padding:0 1em;color:#59636e;border-left:.25em solid #d1d9e0}
table{border-collapse:collapse}th,td{border:1px solid #d1d9e0;padding:6px 13px}img{max-width:100%}
hr{height:.25em;border:0;background:#d1d9e0}ul.contains-task-list{list-style:none;padding-left:1em}
</style>`

func markdownTool() *core.Tool {
	return &core.Tool{
		ID:          "markdown",
		Name:        "Markdown Preview",
		Category:    core.CatInspect,
		Description: "Render GitHub-flavored Markdown (tables, task lists, strikethrough, autolinks, footnotes) and get the HTML. From the CLI, use --open to view it in your browser.",
		Aliases:     []string{"md", "markdown-preview"},
		Keywords:    []string{"gfm", "readme", "render", "html"},
		Options: []core.Option{
			{Key: "raw_html", Label: "Allow raw HTML", Type: core.OptBool, Default: "true", Help: "Previews never run scripts or load remote content, so raw HTML is safe to render"},
			optRemote(),
		},
		Sample: "# devknife\n\nA **fast**, _offline_ toolbox for developers.\n\n## Features\n\n- [x] 45+ tools\n- [x] CLI, web UI and MCP server\n- [ ] Coffee maker\n\n| Tool | Shortcut |\n|------|----------|\n| JSON | `devknife json` |\n| JWT  | `devknife jwt`  |\n\n> Everything stays on your machine.\n\n```go\nfmt.Println(\"hello\")\n```\n\nVisit https://github.com — ~~not~~ autolinked.[^1]\n\n[^1]: Footnotes work too.\n",
		Detect: func(in string) *core.Detection {
			n := len(reMarkdownHint.FindAllString(in, -1))
			if n >= 2 {
				return det(min(40+n*5, 70), "Markdown")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			opts := []goldmark.Option{
				goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.DefinitionList),
				goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			}
			if r.Bool("raw_html") {
				opts = append(opts, goldmark.WithRendererOptions(ghtml.WithUnsafe()))
			}
			var b bytes.Buffer
			if err := goldmark.New(opts...).Convert([]byte(r.Input), &b); err != nil {
				return nil, err
			}
			words := len(strings.Fields(r.Input))
			return &core.Result{
				Output: b.String(),
				Lang:   "html",
				HTML:   "<!doctype html><meta charset=utf-8>" + markdownCSS + "<body>" + b.String(),
				View:   "preview",
				Data:   map[string]any{"remote": r.Bool("remote")},
				Fields: fields("Words", strconv.Itoa(words), "Reading time", fmt.Sprintf("%d min", max(1, words/230)), "Headings", strconv.Itoa(len(reMDHeading.FindAllString(r.Input, -1)))),
			}, nil
		},
	}
}

// utf16Len is the JavaScript string length.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// Compiled once: detectors run on every paste, tools on every keystroke.
var (
	reISODateTime  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$`)
	reJSNamedGroup = regexp.MustCompile(`\(\?<([A-Za-z_]\w*)>`)
	reBackref      = regexp.MustCompile(`\\[1-9]`)
	reJSNamedRef   = regexp.MustCompile(`\$<([A-Za-z_]\w*)>`)
	reMarkdownHint = regexp.MustCompile(`(?m)^#{1,6} \S|^\s*[-*+] \[[ x]\] |^\s*[-*+] \S|\[[^\]]+\]\([^)]+\)|^\x60\x60\x60|\*\*\S`)
	reMDHeading    = regexp.MustCompile(`(?m)^#{1,6} `)
)
