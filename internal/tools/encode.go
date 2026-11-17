package tools

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "golang.org/x/image/webp"

	"devknife/internal/core"
)

var reBase64 = regexp.MustCompile(`^[A-Za-z0-9+/_-]+={0,2}$`)

// decodeBase64Loose accepts standard, URL-safe, padded or unpadded input and
// ignores whitespace/newlines (MIME line wrapping).
func decodeBase64Loose(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if strings.ContainsAny(s, "-_") {
		s = strings.NewReplacer("-", "+", "_", "/").Replace(s)
	}
	s = strings.TrimRight(s, "=")
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		var ce base64.CorruptInputError
		if errorsAs(err, &ce) {
			return nil, fmt.Errorf("invalid base64 at character %d", int(ce)+1)
		}
		return nil, err
	}
	return b, nil
}

func errorsAs(err error, target *base64.CorruptInputError) bool {
	if ce, ok := err.(base64.CorruptInputError); ok {
		*target = ce
		return true
	}
	return false
}

func base64Tool() *core.Tool {
	return &core.Tool{
		ID:          "base64",
		Name:        "Base64 String Encode/Decode",
		Category:    core.CatEncode,
		Description: "Encode text to Base64 or decode it back. Decoding accepts standard and URL-safe alphabets, with or without padding or line breaks.",
		Aliases:     []string{"b64"},
		Keywords:    []string{"encode", "decode", "base64url", "atob", "btoa"},
		Options: []core.Option{
			optMode("encode", "encode", "decode"),
			{Key: "urlsafe", Label: "URL-safe", Type: core.OptBool, Default: "false", Help: "Use the - and _ alphabet (RFC 4648 §5)"},
			{Key: "nopad", Label: "No padding", Type: core.OptBool, Default: "false"},
			{Key: "wrap", Label: "Wrap at 76", Type: core.OptBool, Default: "false", Help: "MIME-style line wrapping"},
		},
		Sample: "Hello, devknife! Everything you paste stays on your machine. 🔒",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			compact := strings.Join(strings.Fields(t), "")
			if len(compact) < 8 || !reBase64.MatchString(compact) || reHexOnly.MatchString(compact) || reDigits.MatchString(compact) {
				return nil
			}
			if strings.Count(t, ".") == 2 {
				return nil // JWT
			}
			b, err := decodeBase64Loose(compact)
			if err != nil || len(b) == 0 {
				return nil
			}
			if isImageBytes(b) {
				return nil
			}
			if printableRatio(string(b)) > 0.95 {
				score := 60
				if strings.HasSuffix(compact, "=") || len(compact) >= 16 {
					score = 75
				}
				return det(score, "Base64 → "+truncate(string(b), 40), "mode", "decode")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			if r.Str("mode") == "decode" {
				b, err := decodeBase64Loose(r.Input)
				if err != nil {
					return nil, err
				}
				res := &core.Result{Fields: fields("Bytes", strconv.Itoa(len(b)))}
				if utf8.Valid(b) {
					res.Output = string(b)
					if mt := http.DetectContentType(b); !strings.HasPrefix(mt, "text/plain") {
						res.Fields = append(res.Fields, core.Field{Label: "Detected type", Value: mt})
					}
				} else {
					res.Output = hexDump(b)
					res.Warnings = []string{"Decoded data is binary (not UTF-8); showing a hex dump. Detected type: " + http.DetectContentType(b)}
				}
				return res, nil
			}
			enc := base64.StdEncoding
			if r.Bool("urlsafe") {
				enc = base64.URLEncoding
			}
			if r.Bool("nopad") {
				enc = enc.WithPadding(base64.NoPadding)
			}
			data := []byte(r.Input)
			if len(r.Data) > 0 {
				data = r.Data
			}
			out := enc.EncodeToString(data)
			if r.Bool("wrap") {
				out = wrapEvery(out, 76)
			}
			return &core.Result{Output: out, Fields: fields("Bytes in", strconv.Itoa(len(data)), "Length out", strconv.Itoa(len(out)))}, nil
		},
	}
}

func wrapEvery(s string, n int) string {
	var b strings.Builder
	for i := 0; i < len(s); i += n {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s[i:min(i+n, len(s))])
	}
	return b.String()
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func hexDump(b []byte) string {
	if len(b) > 4096 {
		return hex.Dump(b[:4096]) + fmt.Sprintf("… (%d more bytes)", len(b)-4096)
	}
	return hex.Dump(b)
}

// ---------------------------------------------------------------------------

var reDataURL = regexp.MustCompile(`^data:([\w/+.-]+)?(;[\w-]+=[\w-]+)*(;base64)?,`)

func isImageBytes(b []byte) bool {
	mt := http.DetectContentType(b)
	return strings.HasPrefix(mt, "image/") || bytes.HasPrefix(bytes.TrimSpace(b), []byte("<svg"))
}

func imageInfo(b []byte) (mime string, dims string) {
	mime = http.DetectContentType(b)
	if bytes.Contains(b[:min(len(b), 512)], []byte("<svg")) {
		mime = "image/svg+xml"
		m := reSVGViewBox.FindSubmatch(b)
		if m != nil {
			dims = string(m[1]) + "×" + string(m[2]) + " (viewBox)"
		}
		return
	}
	if cfg, format, err := image.DecodeConfig(bytes.NewReader(b)); err == nil {
		dims = fmt.Sprintf("%d×%d", cfg.Width, cfg.Height)
		if format == "webp" {
			mime = "image/webp"
		}
	}
	return
}

func base64ImageTool() *core.Tool {
	return &core.Tool{
		ID:          "base64-image",
		Name:        "Base64 Image Encode/Decode",
		Category:    core.CatEncode,
		Description: "Preview a Base64 string or data URL as an image, or turn an image file into Base64, a data URL, a CSS background or an <img> tag.",
		Aliases:     []string{"b64img", "image-base64"},
		Keywords:    []string{"data url", "data uri", "png", "jpeg", "inline image"},
		Input:       core.InputImage,
		Placeholder: "Paste Base64 / a data: URL, or drop an image file",
		Options: []core.Option{
			{Key: "format", Label: "Output as", Type: core.OptSelect, Default: "data-url", Choices: []string{"raw", "data-url", "css", "html", "markdown"}, Help: "Encoding output format"},
		},
		Sample: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAKklEQVR42mNgGAWjgP8/w38GBgaG/0QaQDQYNYBUA4g2hHgD/hNtyCgYBQBnTB/xZjyPVwAAAABJRU5ErkJggg==",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if strings.HasPrefix(t, "data:image/") {
				return det(97, "Image data URL", "mode", "decode")
			}
			compact := strings.Join(strings.Fields(t), "")
			if len(compact) > 40 && reBase64.MatchString(compact) {
				if b, err := decodeBase64Loose(compact); err == nil && isImageBytes(b) {
					return det(92, "Base64-encoded image")
				}
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			var img []byte
			fromFile := false
			if len(r.Data) > 0 && isImageBytes(r.Data) {
				img, fromFile = r.Data, true
			} else {
				t := strings.TrimSpace(r.Input)
				if t == "" {
					return nil, fmt.Errorf("paste a Base64 image / data URL, or load an image file")
				}
				if m := reDataURL.FindString(t); m != "" {
					t = t[len(m):]
				}
				b, err := decodeBase64Loose(t)
				if err != nil {
					return nil, err
				}
				if !isImageBytes(b) {
					return nil, fmt.Errorf("decoded data is not an image (detected %s)", http.DetectContentType(b))
				}
				img = b
			}
			mime, dims := imageInfo(img)
			b64 := base64.StdEncoding.EncodeToString(img)
			dataURL := "data:" + mime + ";base64," + b64
			res := &core.Result{Image: dataURL, View: "image"}
			switch r.Str("format") {
			case "raw":
				res.Output = b64
			case "css":
				res.Output, res.Lang = fmt.Sprintf("background-image: url(\"%s\");", dataURL), "css"
			case "html":
				res.Output, res.Lang = fmt.Sprintf(`<img src="%s" alt="" />`, dataURL), "html"
			case "markdown":
				res.Output = fmt.Sprintf("![image](%s)", dataURL)
			default:
				res.Output = dataURL
			}
			res.Fields = fields("Type", mime, "Dimensions", dims, "Image size", humanBytes(len(img)), "Base64 length", strconv.Itoa(len(b64)))
			if !fromFile {
				res.Fields = append(res.Fields, core.Field{Label: "Tip", Value: "Use --output file.png (CLI) or Save (UI) to write the image"})
			}
			return res, nil
		},
	}
}

// ---------------------------------------------------------------------------

func urlEncodeTool() *core.Tool {
	return &core.Tool{
		ID:          "url",
		Name:        "URL Encode/Decode",
		Category:    core.CatEncode,
		Description: "Percent-encode or decode text. Choose component (encodeURIComponent), form (spaces as +), path segment, or full-URL semantics.",
		Aliases:     []string{"urlencode", "urldecode", "percent"},
		Keywords:    []string{"percent encoding", "escape", "encodeURIComponent", "query string"},
		Options: []core.Option{
			optMode("encode", "encode", "decode"),
			{Key: "variant", Label: "Variant", Type: core.OptSelect, Default: "component", Choices: []string{"component", "form", "path", "full-url"}},
		},
		Sample: "https://example.com/search?q=dev tools & more&lang=en#top",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if rePercentEsc.MatchString(t) {
				if dec, err := url.QueryUnescape(t); err == nil && dec != t {
					score := 70
					if strings.Count(t, "%") >= 3 {
						score = 82
					}
					return det(score, "Percent-encoded → "+truncate(dec, 40), "mode", "decode")
				}
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			in := r.Input
			v := r.Str("variant")
			if r.Str("mode") == "decode" {
				var out string
				var err error
				if v == "form" {
					out, err = url.QueryUnescape(in)
				} else {
					out, err = url.PathUnescape(in)
				}
				if err != nil {
					return nil, fmt.Errorf("invalid percent-encoding: %v", err)
				}
				return &core.Result{Output: out}, nil
			}
			var out string
			switch v {
			case "form":
				out = url.QueryEscape(in)
			case "path":
				out = url.PathEscape(in)
			case "full-url":
				out = percentEncode(in, "-_.!~*'();,/?:@&=+$#")
			default:
				out = percentEncode(in, "-_.!~*'()")
			}
			return &core.Result{Output: out}, nil
		},
	}
}

// percentEncode mirrors JavaScript's encodeURI/encodeURIComponent: ASCII
// alphanumerics plus the given safe characters are left untouched.
func percentEncode(s, safe string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------

func htmlEntityTool() *core.Tool {
	return &core.Tool{
		ID:          "html-entity",
		Name:        "HTML Entity Encode/Decode",
		Category:    core.CatEncode,
		Description: "Escape text for safe HTML embedding, or decode named (&amp;nbsp;), decimal (&amp;#169;) and hex (&amp;#x1F600;) entities.",
		Aliases:     []string{"entity", "entities", "html-escape"},
		Keywords:    []string{"escape", "unescape", "xss", "amp", "nbsp"},
		Options: []core.Option{
			optMode("encode", "encode", "decode"),
			{Key: "non_ascii", Label: "Encode non-ASCII", Type: core.OptBool, Default: "false", Help: "Also convert non-ASCII characters to numeric entities"},
		},
		Sample: `<a href="/search?q=café&lang=fr">Tom & Jerry's "Café" © 2024 → 😀</a>`,
		Detect: func(in string) *core.Detection {
			n := len(reHTMLEntityRef.FindAllString(in, -1))
			if n > 0 {
				return det(min(60+n*8, 88), plural(n, "HTML entity"), "mode", "decode")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			if r.Str("mode") == "decode" {
				return &core.Result{Output: html.UnescapeString(r.Input)}, nil
			}
			// named entities read better than html.EscapeString's &#34; / &#39;
			out := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(r.Input)
			if r.Bool("non_ascii") {
				var b strings.Builder
				for _, c := range out {
					if c > 127 {
						fmt.Fprintf(&b, "&#x%X;", c)
					} else {
						b.WriteRune(c)
					}
				}
				out = b.String()
			}
			return &core.Result{Output: out, Lang: "html"}, nil
		},
	}
}

// ---------------------------------------------------------------------------

func backslashTool() *core.Tool {
	return &core.Tool{
		ID:          "backslash",
		Name:        "Backslash Escape/Unescape",
		Category:    core.CatEncode,
		Description: `Escape text for string literals (\n, \t, \", \\, \u0000) or unescape them back — handles \xHH, \uXXXX, \u{1F600}, \UXXXXXXXX and octal.`,
		Aliases:     []string{"escape", "unescape", "string-escape"},
		Keywords:    []string{"newline", "quote", "string literal", "json string"},
		Options: []core.Option{
			optMode("escape", "escape", "unescape"),
			{Key: "unicode", Label: "Escape non-ASCII", Type: core.OptBool, Default: "false", Help: `Write non-ASCII characters as \uXXXX`},
			{Key: "quote", Label: "Quote style", Type: core.OptSelect, Default: "double", Choices: []string{"double", "single", "both", "none"}, Help: "Which quote characters to escape"},
		},
		Sample: "Line one\nLine \"two\"\twith a tab\\backslash and emoji 🚀",
		Detect: func(in string) *core.Detection {
			n := len(reBackslashEsc.FindAllString(in, -1))
			if n >= 2 || (n == 1 && isSingleLine(in)) {
				return det(min(50+n*5, 72), plural(n, "escape sequence"), "mode", "unescape")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			if r.Str("mode") == "unescape" {
				out, err := unescapeBackslash(r.Input)
				if err != nil {
					return nil, err
				}
				return &core.Result{Output: out}, nil
			}
			return &core.Result{Output: escapeBackslash(r.Input, r.Bool("unicode"), r.Str("quote"))}, nil
		},
	}
}

func escapeBackslash(s string, unicodeEsc bool, quote string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '"' && (quote == "double" || quote == "both"):
			b.WriteString(`\"`)
		case c == '\'' && (quote == "single" || quote == "both"):
			b.WriteString(`\'`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\u%04x`, c)
		case c > 127 && unicodeEsc:
			if c > 0xFFFF {
				r1, r2 := utf16Pair(c)
				fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
			} else {
				fmt.Fprintf(&b, `\u%04x`, c)
			}
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func utf16Pair(c rune) (rune, rune) {
	c -= 0x10000
	return 0xD800 + (c>>10)&0x3FF, 0xDC00 + c&0x3FF
}

func unescapeBackslash(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch e := s[i]; e {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case 'a':
			b.WriteByte('\a')
		case 'e':
			b.WriteByte(0x1b)
		case '\\', '"', '\'', '/', '`', '?':
			b.WriteByte(e)
		case 'x':
			if i+2 < len(s) && isHex(s[i+1:i+3]) {
				v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
				b.WriteByte(byte(v))
				i += 2
			} else {
				return "", fmt.Errorf(`invalid \x escape at position %d`, i)
			}
		case 'u':
			if i+1 < len(s) && s[i+1] == '{' {
				end := strings.IndexByte(s[i:], '}')
				if end < 0 {
					return "", fmt.Errorf(`unterminated \u{...} at position %d`, i)
				}
				v, err := strconv.ParseUint(s[i+2:i+end], 16, 32)
				if err != nil {
					return "", fmt.Errorf(`invalid \u{...} escape at position %d`, i)
				}
				b.WriteRune(rune(v))
				i += end
				continue
			}
			if !(i+4 < len(s) && isHex(s[i+1:i+5])) {
				return "", fmt.Errorf(`invalid \u escape at position %d`, i)
			}
			v, _ := strconv.ParseUint(s[i+1:i+5], 16, 32)
			i += 4
			r := rune(v)
			// UTF-16 surrogate pair: \uD83D\uDE00
			if r >= 0xD800 && r < 0xDC00 && i+6 < len(s) && s[i+1] == '\\' && s[i+2] == 'u' && isHex(s[i+3:i+7]) {
				lo, _ := strconv.ParseUint(s[i+3:i+7], 16, 32)
				if lo >= 0xDC00 && lo < 0xE000 {
					r = 0x10000 + (r-0xD800)<<10 + (rune(lo) - 0xDC00)
					i += 6
				}
			}
			b.WriteRune(r)
		case 'U':
			if !(i+8 < len(s) && isHex(s[i+1:i+9])) {
				return "", fmt.Errorf(`invalid \U escape at position %d`, i)
			}
			v, _ := strconv.ParseUint(s[i+1:i+9], 16, 32)
			b.WriteRune(rune(v))
			i += 8
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
				j++
			}
			v, _ := strconv.ParseUint(s[i:j], 8, 16)
			b.WriteByte(byte(v))
			i = j - 1
		case '\n':
			// line continuation
		default:
			b.WriteByte('\\')
			b.WriteByte(e)
		}
	}
	return b.String(), nil
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Certificate decoder

var sampleCert = sync.OnceValue(func() string {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now().Truncate(time.Hour)
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(0x5eed_cafe_f00d),
		Subject:               pkix.Name{CommonName: "devknife.local", Organization: []string{"devknife"}, Country: []string{"US"}, Province: []string{"California"}, Locality: []string{"San Francisco"}},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"devknife.local", "*.devknife.local", "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
})

func certTool() *core.Tool {
	return &core.Tool{
		ID:          "cert",
		Name:        "Certificate Decoder (X.509)",
		Category:    core.CatEncode,
		Description: "Decode PEM/DER certificates, chains and CSRs: subject, issuer, validity (with days remaining), SANs, key details, extensions and fingerprints — openssl-style.",
		Aliases:     []string{"x509", "certificate", "pem", "csr"},
		Keywords:    []string{"ssl", "tls", "openssl", "fingerprint", "chain", "expiry"},
		Sample:      sampleCert(),
		Detect: func(in string) *core.Detection {
			if strings.Contains(in, "-----BEGIN CERTIFICATE") || strings.Contains(in, "-----BEGIN CERTIFICATE REQUEST") || strings.Contains(in, "-----BEGIN NEW CERTIFICATE REQUEST") {
				return det(99, "PEM certificate")
			}
			t := strings.Join(strings.Fields(in), "")
			if strings.HasPrefix(t, "MII") && len(t) > 200 && reBase64.MatchString(t) {
				if b, err := decodeBase64Loose(t); err == nil {
					if _, err := x509.ParseCertificate(b); err == nil {
						return det(95, "Base64 DER certificate")
					}
				}
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			certs, csrs, err := parseCerts(r.Input, r.Data)
			if err != nil {
				return nil, err
			}
			var out strings.Builder
			res := &core.Result{}
			for i, c := range certs {
				if i > 0 {
					out.WriteString("\n")
				}
				if len(certs) > 1 {
					fmt.Fprintf(&out, "# Certificate %d of %d\n", i+1, len(certs))
				}
				out.WriteString(describeCert(c))
				if i == 0 {
					res.Fields = certFields(c)
				}
			}
			for _, c := range csrs {
				out.WriteString(describeCSR(c))
				if len(certs) == 0 && res.Fields == nil {
					res.Fields = fields("Type", "Certificate Signing Request", "Subject", c.Subject.String(), "Key", keyDesc(c.PublicKey), "SANs", strings.Join(sans(c.DNSNames, c.IPAddresses, c.EmailAddresses, c.URIs), ", "))
				}
			}
			if len(certs) > 1 {
				res.Fields = append([]core.Field{{Label: "Chain", Value: fmt.Sprintf("%d certificates", len(certs))}}, res.Fields...)
			}
			res.Output = out.String()
			return res, nil
		},
	}
}

func parseCerts(in string, data []byte) ([]*x509.Certificate, []*x509.CertificateRequest, error) {
	var certs []*x509.Certificate
	var csrs []*x509.CertificateRequest
	rest := []byte(in)
	if len(data) > 0 {
		rest = data
	}
	found := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		found = true
		switch block.Type {
		case "CERTIFICATE", "TRUSTED CERTIFICATE":
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid certificate: %v", err)
			}
			certs = append(certs, c)
		case "CERTIFICATE REQUEST", "NEW CERTIFICATE REQUEST":
			c, err := x509.ParseCertificateRequest(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid CSR: %v", err)
			}
			csrs = append(csrs, c)
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "ENCRYPTED PRIVATE KEY":
			return nil, nil, fmt.Errorf("this is a private key, not a certificate — careful where you paste it")
		}
	}
	if !found {
		raw := data
		if len(raw) == 0 {
			b, err := decodeBase64Loose(strings.TrimSpace(in))
			if err != nil {
				return nil, nil, fmt.Errorf("no PEM block found and input is not base64 DER")
			}
			raw = b
		}
		if c, err := x509.ParseCertificate(raw); err == nil {
			certs = append(certs, c)
		} else if csr, err2 := x509.ParseCertificateRequest(raw); err2 == nil {
			csrs = append(csrs, csr)
		} else {
			return nil, nil, fmt.Errorf("cannot parse certificate: %v", err)
		}
	}
	if len(certs) == 0 && len(csrs) == 0 {
		return nil, nil, fmt.Errorf("no certificate found in input")
	}
	return certs, csrs, nil
}

func colonHex(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	return strings.Join(parts, ":")
}

func sans(dns []string, ips []net.IP, emails []string, uris []*url.URL) []string {
	var out []string
	for _, d := range dns {
		out = append(out, "DNS:"+d)
	}
	for _, ip := range ips {
		out = append(out, "IP:"+ip.String())
	}
	for _, e := range emails {
		out = append(out, "email:"+e)
	}
	for _, u := range uris {
		out = append(out, "URI:"+u.String())
	}
	return out
}

func keyDesc(pub any) string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d bit", k.N.BitLen())
	case *ecdsa.PublicKey:
		return "ECDSA " + k.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	}
	return fmt.Sprintf("%T", pub)
}

func certFields(c *x509.Certificate) []core.Field {
	now := time.Now()
	status := ""
	switch {
	case now.Before(c.NotBefore):
		status = "not yet valid"
	case now.After(c.NotAfter):
		status = fmt.Sprintf("EXPIRED %d days ago", int(now.Sub(c.NotAfter).Hours()/24))
	default:
		status = fmt.Sprintf("valid, %d days remaining", int(c.NotAfter.Sub(now).Hours()/24))
	}
	fp256 := sha256.Sum256(c.Raw)
	fp1 := sha1.Sum(c.Raw)
	f := fields(
		"Subject", c.Subject.String(),
		"Issuer", c.Issuer.String(),
		"Status", status,
		"Not before", c.NotBefore.UTC().Format(time.RFC1123),
		"Not after", c.NotAfter.UTC().Format(time.RFC1123),
		"SANs", strings.Join(sans(c.DNSNames, c.IPAddresses, c.EmailAddresses, c.URIs), ", "),
		"Key", keyDesc(c.PublicKey),
		"Signature", c.SignatureAlgorithm.String(),
		"Serial", colonHex(c.SerialNumber.Bytes()),
		"Is CA", strconv.FormatBool(c.IsCA),
		"Self-signed", strconv.FormatBool(bytes.Equal(c.RawIssuer, c.RawSubject)),
		"SHA-256 fingerprint", colonHex(fp256[:]),
		"SHA-1 fingerprint", colonHex(fp1[:]),
	)
	return f
}

func wrapHexBlock(b []byte, indent string) string {
	h := make([]string, len(b))
	for i, x := range b {
		h[i] = fmt.Sprintf("%02x", x)
	}
	var out strings.Builder
	for i := 0; i < len(h); i += 15 {
		out.WriteString(indent + strings.Join(h[i:min(i+15, len(h))], ":"))
		if i+15 < len(h) {
			out.WriteString(":")
		}
		out.WriteString("\n")
	}
	return out.String()
}

var extKeyUsageNames = map[x509.ExtKeyUsage]string{
	x509.ExtKeyUsageAny: "Any", x509.ExtKeyUsageServerAuth: "TLS Web Server Authentication", x509.ExtKeyUsageClientAuth: "TLS Web Client Authentication",
	x509.ExtKeyUsageCodeSigning: "Code Signing", x509.ExtKeyUsageEmailProtection: "E-mail Protection", x509.ExtKeyUsageTimeStamping: "Time Stamping", x509.ExtKeyUsageOCSPSigning: "OCSP Signing",
}

func keyUsageNames(u x509.KeyUsage) []string {
	names := []string{"Digital Signature", "Content Commitment", "Key Encipherment", "Data Encipherment", "Key Agreement", "Certificate Sign", "CRL Sign", "Encipher Only", "Decipher Only"}
	var out []string
	for i, n := range names {
		if u&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return out
}

func describePublicKey(b *strings.Builder, pub any, pad string) {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		fmt.Fprintf(b, "%sPublic Key Algorithm: rsaEncryption\n%s    RSA Public-Key: (%d bit)\n%s    Modulus:\n", pad, pad, k.N.BitLen(), pad)
		b.WriteString(wrapHexBlock(append([]byte{0}, k.N.Bytes()...), pad+"        "))
		fmt.Fprintf(b, "%s    Exponent: %d (0x%x)\n", pad, k.E, k.E)
	case *ecdsa.PublicKey:
		raw, _ := k.ECDH()
		fmt.Fprintf(b, "%sPublic Key Algorithm: id-ecPublicKey\n%s    Public-Key: (%d bit)\n%s    pub:\n", pad, pad, k.Curve.Params().BitSize, pad)
		if raw != nil {
			b.WriteString(wrapHexBlock(raw.Bytes(), pad+"        "))
		}
		fmt.Fprintf(b, "%s    NIST CURVE: %s\n", pad, k.Curve.Params().Name)
	case ed25519.PublicKey:
		fmt.Fprintf(b, "%sPublic Key Algorithm: ED25519\n%s    pub:\n", pad, pad)
		b.WriteString(wrapHexBlock(k, pad+"        "))
	default:
		fmt.Fprintf(b, "%sPublic Key Algorithm: %T\n", pad, pub)
	}
}

func describeCert(c *x509.Certificate) string {
	var b strings.Builder
	b.WriteString("Certificate:\n    Data:\n")
	fmt.Fprintf(&b, "        Version: %d (0x%x)\n", c.Version, c.Version-1)
	fmt.Fprintf(&b, "        Serial Number:\n            %s\n", strings.ToLower(colonHex(c.SerialNumber.Bytes())))
	fmt.Fprintf(&b, "        Signature Algorithm: %s\n", c.SignatureAlgorithm)
	fmt.Fprintf(&b, "        Issuer: %s\n", c.Issuer)
	fmt.Fprintf(&b, "        Validity\n            Not Before: %s\n            Not After : %s\n", c.NotBefore.UTC().Format("Jan _2 15:04:05 2006 GMT"), c.NotAfter.UTC().Format("Jan _2 15:04:05 2006 GMT"))
	fmt.Fprintf(&b, "        Subject: %s\n", c.Subject)
	b.WriteString("        Subject Public Key Info:\n")
	describePublicKey(&b, c.PublicKey, "            ")
	b.WriteString("        X509v3 extensions:\n")
	if s := sans(c.DNSNames, c.IPAddresses, c.EmailAddresses, c.URIs); len(s) > 0 {
		fmt.Fprintf(&b, "            X509v3 Subject Alternative Name:\n                %s\n", strings.Join(s, ", "))
	}
	if ku := keyUsageNames(c.KeyUsage); len(ku) > 0 {
		fmt.Fprintf(&b, "            X509v3 Key Usage: critical\n                %s\n", strings.Join(ku, ", "))
	}
	if len(c.ExtKeyUsage) > 0 {
		var eku []string
		for _, u := range c.ExtKeyUsage {
			if n, ok := extKeyUsageNames[u]; ok {
				eku = append(eku, n)
			} else {
				eku = append(eku, fmt.Sprintf("Unknown (%d)", u))
			}
		}
		fmt.Fprintf(&b, "            X509v3 Extended Key Usage:\n                %s\n", strings.Join(eku, ", "))
	}
	if c.BasicConstraintsValid {
		fmt.Fprintf(&b, "            X509v3 Basic Constraints: critical\n                CA:%s", strings.ToUpper(strconv.FormatBool(c.IsCA)))
		if c.MaxPathLen > 0 || c.MaxPathLenZero {
			fmt.Fprintf(&b, ", pathlen:%d", c.MaxPathLen)
		}
		b.WriteString("\n")
	}
	if len(c.SubjectKeyId) > 0 {
		fmt.Fprintf(&b, "            X509v3 Subject Key Identifier:\n                %s\n", colonHex(c.SubjectKeyId))
	}
	if len(c.AuthorityKeyId) > 0 {
		fmt.Fprintf(&b, "            X509v3 Authority Key Identifier:\n                keyid:%s\n", colonHex(c.AuthorityKeyId))
	}
	if len(c.OCSPServer) > 0 || len(c.IssuingCertificateURL) > 0 {
		b.WriteString("            Authority Information Access:\n")
		for _, o := range c.OCSPServer {
			fmt.Fprintf(&b, "                OCSP - URI:%s\n", o)
		}
		for _, u := range c.IssuingCertificateURL {
			fmt.Fprintf(&b, "                CA Issuers - URI:%s\n", u)
		}
	}
	if len(c.CRLDistributionPoints) > 0 {
		fmt.Fprintf(&b, "            X509v3 CRL Distribution Points:\n                URI:%s\n", strings.Join(c.CRLDistributionPoints, ", URI:"))
	}
	fmt.Fprintf(&b, "    Signature Algorithm: %s\n", c.SignatureAlgorithm)
	b.WriteString(wrapHexBlock(c.Signature, "         "))
	return b.String()
}

func describeCSR(c *x509.CertificateRequest) string {
	var b strings.Builder
	b.WriteString("Certificate Request:\n    Data:\n")
	fmt.Fprintf(&b, "        Version: %d\n", c.Version)
	fmt.Fprintf(&b, "        Subject: %s\n", c.Subject)
	b.WriteString("        Subject Public Key Info:\n")
	describePublicKey(&b, c.PublicKey, "            ")
	if s := sans(c.DNSNames, c.IPAddresses, c.EmailAddresses, c.URIs); len(s) > 0 {
		fmt.Fprintf(&b, "        Requested Extensions:\n            X509v3 Subject Alternative Name:\n                %s\n", strings.Join(s, ", "))
	}
	sig := "valid"
	if err := c.CheckSignature(); err != nil {
		sig = "INVALID: " + err.Error()
	}
	fmt.Fprintf(&b, "    Signature Algorithm: %s (%s)\n", c.SignatureAlgorithm, sig)
	return b.String()
}

func encodeTools() []*core.Tool {
	return []*core.Tool{base64Tool(), base64ImageTool(), urlEncodeTool(), htmlEntityTool(), backslashTool(), certTool()}
}

// Compiled once: detectors run on every paste, tools on every keystroke.
var (
	reSVGViewBox    = regexp.MustCompile(`viewBox="[\d.\s-]*?([\d.]+)\s+([\d.]+)"`)
	rePercentEsc    = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)
	reHTMLEntityRef = regexp.MustCompile(`&(#\d+|#x[0-9a-fA-F]+|[a-zA-Z]{2,8});`)
	reBackslashEsc  = regexp.MustCompile(`\\[nrt"'\\/bfv0]|\\u[0-9a-fA-F]{4}|\\x[0-9a-fA-F]{2}`)
)
