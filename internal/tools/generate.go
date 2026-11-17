package tools

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"image"
	"math"
	"math/big"
	mrand "math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/makiuchi-d/gozxing"
	gzqr "github.com/makiuchi-d/gozxing/qrcode"
	"github.com/oklog/ulid/v2"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/md4"

	"devknife/internal/core"
)

var (
	reUUID     = regexp.MustCompile(`(?i)^(urn:uuid:)?\{?[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}\}?$`)
	reULID     = regexp.MustCompile(`(?i)^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	reObjectID = regexp.MustCompile(`(?i)^[0-9a-f]{24}$`)
)

const nanoAlphabet = "_-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

var objectIDCounter atomic.Uint32

func init() {
	var b [4]byte
	_, _ = rand.Read(b[:])
	objectIDCounter.Store(binary.BigEndian.Uint32(b[:]))
	// v1 and v6 UUIDs carry a node ID, by default this machine's MAC
	// address, so every ID would identify the machine. RFC 9562 §6.10 allows
	// a random node ID with the multicast bit set instead.
	node := make([]byte, 6)
	_, _ = rand.Read(node)
	node[0] |= 0x01
	uuid.SetNodeID(node)
}

func randomFrom(alphabet string, n int) string {
	runes := []rune(alphabet)
	out := make([]rune, n)
	max := big.NewInt(int64(len(runes)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		out[i] = runes[v.Int64()]
	}
	return string(out)
}

func newObjectID() string {
	var b [12]byte
	binary.BigEndian.PutUint32(b[0:4], uint32(time.Now().Unix()))
	_, _ = rand.Read(b[4:9])
	c := objectIDCounter.Add(1)
	b[9], b[10], b[11] = byte(c>>16), byte(c>>8), byte(c)
	return hex.EncodeToString(b[:])
}

func uuidNamespace(ns string) (uuid.UUID, error) {
	switch strings.ToLower(ns) {
	case "", "dns":
		return uuid.NameSpaceDNS, nil
	case "url":
		return uuid.NameSpaceURL, nil
	case "oid":
		return uuid.NameSpaceOID, nil
	case "x500":
		return uuid.NameSpaceX500, nil
	}
	u, err := uuid.Parse(ns)
	if err != nil {
		return uuid.Nil, fmt.Errorf("namespace must be dns, url, oid, x500 or a UUID")
	}
	return u, nil
}

func uuidTool() *core.Tool {
	return &core.Tool{
		ID:          "uuid",
		Name:        "UUID/ULID Generate/Decode",
		Category:    core.CatGenerate,
		Description: "Generate UUID v1/v3/v4/v5/v6/v7, ULID, Nano ID or MongoDB ObjectID in bulk — or decode one to see its version, variant, embedded timestamp and raw bytes.",
		Aliases:     []string{"ulid", "guid", "nanoid", "objectid"},
		Keywords:    []string{"unique id", "identifier", "v4", "v7", "random id"},
		Input:       core.InputOptional,
		Placeholder: "Paste a UUID / ULID / ObjectID to decode it",
		Args:        []string{"?mode", "?type", "input"},
		Options: []core.Option{
			optMode("generate", "generate", "decode"),
			{Key: "type", Label: "Type", Type: core.OptSelect, Default: "v4", Choices: []string{"v4", "v7", "v1", "v6", "v5", "v3", "ulid", "nanoid", "objectid"}, Short: "t"},
			{Key: "count", Label: "Count", Type: core.OptNumber, Default: "1", Short: "n"},
			{Key: "upper", Label: "Uppercase", Type: core.OptBool, Default: "false"},
			{Key: "hyphens", Label: "Hyphens", Type: core.OptBool, Default: "true"},
			{Key: "namespace", Label: "Namespace (v3/v5)", Type: core.OptString, Default: "dns", Placeholder: "dns | url | oid | x500 | <uuid>"},
			{Key: "name", Label: "Name (v3/v5)", Type: core.OptString, Default: "", Placeholder: "example.com"},
		},
		Sample: "018f6b1e-5c3a-7d4e-9f1a-2b3c4d5e6f70",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			switch {
			case reUUID.MatchString(t):
				return det(96, "UUID", "mode", "decode")
			case reULID.MatchString(t):
				return det(93, "ULID", "mode", "decode")
			case reObjectID.MatchString(t):
				return det(55, "Possibly a MongoDB ObjectID", "mode", "decode")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			if r.Str("mode") == "decode" {
				return decodeID(strings.TrimSpace(r.Input))
			}
			n := r.Int("count", 1)
			if n < 1 {
				n = 1
			}
			if n > 10000 {
				return nil, fmt.Errorf("count is capped at 10000")
			}
			ids := make([]string, 0, n)
			for i := 0; i < n; i++ {
				var s string
				switch r.Str("type") {
				case "v7":
					u, err := uuid.NewV7()
					if err != nil {
						return nil, err
					}
					s = u.String()
				case "v1":
					u, err := uuid.NewUUID()
					if err != nil {
						return nil, err
					}
					s = u.String()
				case "v6":
					u, err := uuid.NewV6()
					if err != nil {
						return nil, err
					}
					s = u.String()
				case "v5", "v3":
					ns, err := uuidNamespace(r.Str("namespace"))
					if err != nil {
						return nil, err
					}
					if r.Str("name") == "" {
						return nil, fmt.Errorf("--name is required for name-based UUIDs (%s)", r.Str("type"))
					}
					if r.Str("type") == "v5" {
						s = uuid.NewSHA1(ns, []byte(r.Str("name"))).String()
					} else {
						s = uuid.NewMD5(ns, []byte(r.Str("name"))).String()
					}
					n = 1 // deterministic: one is enough
				case "ulid":
					s = ulid.Make().String()
				case "nanoid":
					s = randomFrom(nanoAlphabet, 21)
				case "objectid":
					s = newObjectID()
				default:
					s = uuid.New().String()
				}
				if strings.HasPrefix(r.Str("type"), "v") {
					if !r.Bool("hyphens") {
						s = strings.ReplaceAll(s, "-", "")
					}
					if r.Bool("upper") {
						s = strings.ToUpper(s)
					}
				} else if r.Bool("upper") && r.Str("type") == "objectid" {
					s = strings.ToUpper(s)
				}
				ids = append(ids, s)
			}
			res := &core.Result{Output: strings.Join(ids, "\n"), Fields: fields("Generated", plural(len(ids), "ID"))}
			// like DevUtils, decode whatever ID sits in the input alongside generation
			if in := strings.TrimSpace(r.Input); in != "" {
				if d, err := decodeID(in); err == nil {
					res.Fields = append(res.Fields, d.Fields...)
				}
			}
			return res, nil
		},
	}
}

var uuidVersionNames = map[uuid.Version]string{
	1: "1 (time + MAC address)", 2: "2 (DCE security)", 3: "3 (MD5 name-based)", 4: "4 (random)",
	5: "5 (SHA-1 name-based)", 6: "6 (reordered time)", 7: "7 (Unix-epoch time + random)", 8: "8 (custom)",
}

func decodeID(t string) (*core.Result, error) {
	switch {
	case t == "":
		return nil, fmt.Errorf("paste an ID to decode")
	case reUUID.MatchString(t):
		u, err := uuid.Parse(t)
		if err != nil {
			return nil, err
		}
		ver := uuidVersionNames[u.Version()]
		if ver == "" {
			ver = strconv.Itoa(int(u.Version()))
		}
		if u == uuid.Nil {
			ver = "Nil UUID"
		} else if u == uuid.Max {
			ver = "Max UUID"
		}
		variant := map[uuid.Variant]string{uuid.RFC4122: "Standard (RFC 9562 / DCE 1.1)", uuid.Reserved: "Reserved (NCS backward compatibility)", uuid.Microsoft: "Reserved (Microsoft GUID)", uuid.Future: "Reserved (future)"}[u.Variant()]
		res := &core.Result{Output: u.String()}
		res.Fields = fields(
			"Standard format", u.String(),
			"Raw contents", strings.ToLower(colonHex(u[:])),
			"Version", ver,
			"Variant", variant,
			"Integer", new(big.Int).SetBytes(u[:]).String(),
			"Base64", base64.RawURLEncoding.EncodeToString(u[:]),
		)
		switch u.Version() {
		case 1, 6, 7:
			sec, nsec := u.Time().UnixTime()
			ts := time.Unix(sec, nsec)
			res.Fields = append(res.Fields, fields("Timestamp (UTC)", ts.UTC().Format(time.RFC3339Nano), "Timestamp (local)", ts.Local().Format("Mon Jan 02 2006 15:04:05.000 MST"))...)
			if u.Version() != 7 {
				node := "Node (MAC address of the machine that made it)"
				if u.NodeID()[0]&0x01 != 0 {
					node = "Node (random)" // multicast bit: not a real MAC
				}
				res.Fields = append(res.Fields, fields("Clock sequence", strconv.Itoa(u.ClockSequence()), node, colonHex(u.NodeID()))...)
			}
		}
		return res, nil
	case reULID.MatchString(t):
		id, err := ulid.ParseStrict(strings.ToUpper(t))
		if err != nil {
			return nil, err
		}
		ts := ulid.Time(id.Time())
		ent := id.Entropy()
		return &core.Result{Output: id.String(), Fields: fields(
			"ULID", id.String(),
			"Timestamp (UTC)", ts.UTC().Format(time.RFC3339Nano),
			"Timestamp (local)", ts.Local().Format("Mon Jan 02 2006 15:04:05.000 MST"),
			"Unix ms", strconv.FormatUint(id.Time(), 10),
			"Randomness", strings.ToUpper(hex.EncodeToString(ent)),
			"As UUID", uuid.UUID(id).String(),
		)}, nil
	case reObjectID.MatchString(t):
		b, _ := hex.DecodeString(t)
		ts := time.Unix(int64(binary.BigEndian.Uint32(b[0:4])), 0)
		return &core.Result{Output: strings.ToLower(t), Fields: fields(
			"ObjectID", strings.ToLower(t),
			"Timestamp (UTC)", ts.UTC().Format(time.RFC3339),
			"Timestamp (local)", ts.Local().Format("Mon Jan 02 2006 15:04:05 MST"),
			"Random", hex.EncodeToString(b[4:9]),
			"Counter", strconv.Itoa(int(b[9])<<16|int(b[10])<<8|int(b[11])),
		)}, nil
	}
	return nil, fmt.Errorf("not a UUID, ULID or ObjectID")
}

// ---------------------------------------------------------------------------

var loremWords = strings.Fields(`lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua enim ad minim veniam quis nostrud exercitation ullamco laboris nisi aliquip ex ea commodo consequat duis aute irure in reprehenderit voluptate velit esse cillum fugiat nulla pariatur excepteur sint occaecat cupidatat non proident sunt culpa qui officia deserunt mollit anim id est laborum curabitur pretium tincidunt lacus nunc pulvinar sapien ligula ullamcorper malesuada proin libero nunc consequat interdum varius quam pellentesque nec nam aliquam sem et tortor vitae congue mauris rhoncus aenean vel elit scelerisque mauris pellentesque pulvinar mattis nunc faucibus a pellentesque sit amet porttitor eget dolor morbi non arcu risus quis varius quam quisque id diam vel quam elementum pulvinar etiam non quam lacus suspendisse faucibus interdum posuere`)

func loremSentence() string {
	n := 6 + mrand.IntN(10)
	words := make([]string, n)
	for i := range words {
		words[i] = loremWords[mrand.IntN(len(loremWords))]
	}
	if n > 8 {
		words[3+mrand.IntN(n-6)] += ","
	}
	s := strings.Join(words, " ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func loremTool() *core.Tool {
	return &core.Tool{
		ID:          "lorem",
		Name:        "Lorem Ipsum Generator",
		Category:    core.CatGenerate,
		Description: "Generate placeholder text as paragraphs, sentences or words, optionally wrapped in <p> tags.",
		Aliases:     []string{"lipsum", "placeholder"},
		Keywords:    []string{"dummy text", "filler"},
		Input:       core.InputNone,
		Args:        []string{"?unit", "count"},
		Options: []core.Option{
			{Key: "unit", Label: "Unit", Type: core.OptSelect, Default: "paragraphs", Choices: []string{"paragraphs", "sentences", "words"}, Short: "u"},
			{Key: "count", Label: "Count", Type: core.OptNumber, Default: "3", Short: "n"},
			{Key: "lorem", Label: "Start with “Lorem ipsum”", Type: core.OptBool, Default: "true"},
			{Key: "html", Label: "Wrap in <p>", Type: core.OptBool, Default: "false"},
		},
		Run: func(r core.Request) (*core.Result, error) {
			n := max(1, min(r.Int("count", 3), 1000))
			var out string
			switch r.Str("unit") {
			case "words":
				words := make([]string, n)
				for i := range words {
					words[i] = loremWords[mrand.IntN(len(loremWords))]
				}
				if r.Bool("lorem") {
					copy(words, []string{"lorem", "ipsum", "dolor", "sit", "amet"}[:min(5, n)])
				}
				out = strings.Join(words, " ")
				out = strings.ToUpper(out[:1]) + out[1:]
			case "sentences":
				ss := make([]string, n)
				for i := range ss {
					ss[i] = loremSentence()
				}
				if r.Bool("lorem") {
					ss[0] = "Lorem ipsum dolor sit amet, consectetur adipiscing elit."
				}
				out = strings.Join(ss, " ")
			default:
				ps := make([]string, n)
				for i := range ps {
					ss := make([]string, 4+mrand.IntN(4))
					for j := range ss {
						ss[j] = loremSentence()
					}
					if i == 0 && r.Bool("lorem") {
						ss[0] = "Lorem ipsum dolor sit amet, consectetur adipiscing elit."
					}
					ps[i] = strings.Join(ss, " ")
					if r.Bool("html") {
						ps[i] = "<p>" + ps[i] + "</p>"
					}
				}
				sep := "\n\n"
				if r.Bool("html") {
					sep = "\n"
				}
				out = strings.Join(ps, sep)
			}
			lang := ""
			if r.Bool("html") {
				lang = "html"
			}
			return &core.Result{Output: out, Lang: lang, Fields: fields("Words", strconv.Itoa(len(strings.Fields(out))), "Characters", strconv.Itoa(len([]rune(out))))}, nil
		},
	}
}

// ---------------------------------------------------------------------------

func qrTool() *core.Tool {
	return &core.Tool{
		ID:          "qr",
		Name:        "QR Code Reader/Generator",
		Category:    core.CatGenerate,
		Description: "Generate QR codes (PNG, with error-correction level) for text, URLs, Wi-Fi, vCards and more — or read the content of a QR code image. In the terminal it renders the code inline.",
		Aliases:     []string{"qrcode"},
		Keywords:    []string{"barcode", "wifi", "vcard", "scan"},
		Input:       core.InputImage,
		Placeholder: "Text or URL to encode — or drop a QR image to read it",
		Options: []core.Option{
			optMode("generate", "generate", "read"),
			{Key: "level", Label: "Error correction", Type: core.OptSelect, Default: "M", Choices: []string{"L", "M", "Q", "H"}, Help: "L 7%, M 15%, Q 25%, H 30% recoverable"},
			{Key: "size", Label: "Size (px)", Type: core.OptNumber, Default: "320"},
		},
		Sample: "https://github.com/",
		Templates: []core.Template{
			{Name: "URL", Input: "https://example.com"},
			{Name: "Wi-Fi network", Input: "WIFI:T:WPA;S:MyNetwork;P:my-secret-password;H:false;;"},
			{Name: "vCard contact", Input: "BEGIN:VCARD\nVERSION:3.0\nFN:Full Name\nN:Last;First\nTITLE:Your Title\nTEL;TYPE=CELL:+1-555-0100\nEMAIL;TYPE=WORK:you@example.com\nURL:https://example.com\nORG:Company\nEND:VCARD"},
			{Name: "Email", Input: "mailto:someone@example.com?subject=Hello&body=Hi%20there"},
			{Name: "SMS", Input: "SMSTO:+15550100:Hello from devknife"},
			{Name: "Phone", Input: "tel:+15550100"},
			{Name: "Geo location", Input: "geo:37.7749,-122.4194"},
			{Name: "Calendar event", Input: "BEGIN:VEVENT\nSUMMARY:Team sync\nDTSTART:20250101T100000Z\nDTEND:20250101T103000Z\nLOCATION:Room 1\nEND:VEVENT"},
		},
		Run: func(r core.Request) (*core.Result, error) {
			if r.Str("mode") == "read" || len(r.Data) > 0 {
				return readQR(r)
			}
			if r.Input == "" {
				return nil, fmt.Errorf("enter text to encode")
			}
			level := map[string]qrcode.RecoveryLevel{"L": qrcode.Low, "M": qrcode.Medium, "Q": qrcode.High, "H": qrcode.Highest}[r.Str("level")]
			q, err := qrcode.New(r.Input, level)
			if err != nil {
				return nil, fmt.Errorf("cannot encode: %v (too much data for this error-correction level?)", err)
			}
			size := max(64, min(r.Int("size", 320), 2048))
			png, err := q.PNG(size)
			if err != nil {
				return nil, err
			}
			return &core.Result{
				Output: qrTerminal(q.Bitmap()),
				Image:  "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
				View:   "image",
				Fields: fields("Content length", fmt.Sprintf("%d bytes", len(r.Input)), "QR version", strconv.Itoa(q.VersionNumber), "Modules", fmt.Sprintf("%d×%d", len(q.Bitmap())-8, len(q.Bitmap())-8)),
			}, nil
		},
	}
}

// qrTerminal renders a QR bitmap with Unicode half blocks (two rows per
// line). Light modules are drawn, so it scans on dark terminal backgrounds.
func qrTerminal(bm [][]bool) string {
	var b strings.Builder
	for y := 0; y < len(bm); y += 2 {
		for x := 0; x < len(bm[y]); x++ {
			top := !bm[y][x]
			bot := y+1 >= len(bm) || !bm[y+1][x]
			switch {
			case top && bot:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bot:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func readQR(r core.Request) (*core.Result, error) {
	data := r.Data
	if len(data) == 0 {
		t := strings.TrimSpace(r.Input)
		if m := reDataURL.FindString(t); m != "" {
			t = t[len(m):]
		}
		b, err := decodeBase64Loose(t)
		if err != nil {
			return nil, fmt.Errorf("load a QR image (file, clipboard, or base64/data URL) to read it")
		}
		data = b
	}
	img, err := decodeImageBounded(data)
	if err != nil {
		return nil, err
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return nil, err
	}
	res, err := gzqr.NewQRCodeReader().Decode(bmp, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true})
	if err != nil {
		return nil, fmt.Errorf("no QR code found in the image")
	}
	return &core.Result{Output: res.GetText(), Fields: fields("Format", res.GetBarcodeFormat().String(), "Length", strconv.Itoa(len(res.GetText())))}, nil
}

// maxImagePixels bounds images decoded from user data. Decoding allocates
// width×height pixels up front, and a compressed PNG of a blank 12000×12000
// image is only ~140 KB, so the header is checked before any pixel is read.
// 32 MP covers phone photos and 6K screenshots.
const maxImagePixels = 32_000_000

// decodeImageBounded decodes an image after checking its declared size.
// Every image.Decode on user data must go through it.
func decodeImageBounded(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cannot decode image: %v", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("cannot decode image: it has no pixels (%d×%d)", cfg.Width, cfg.Height)
	}
	if px := int64(cfg.Width) * int64(cfg.Height); px > maxImagePixels {
		return nil, fmt.Errorf("image is too large to scan (%d×%d, %.0f megapixels; the limit is %d); crop or scale it down", cfg.Width, cfg.Height, float64(px)/1e6, maxImagePixels/1_000_000)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cannot decode image: %v", err)
	}
	return img, nil
}

// ---------------------------------------------------------------------------

type hashAlgo struct {
	name string
	new  func() hash.Hash
	hmac bool
}

var hashAlgos = []hashAlgo{
	{"md4", md4.New, true},
	{"md5", md5.New, true},
	{"sha1", sha1.New, true},
	{"sha224", sha256.New224, true},
	{"sha256", sha256.New, true},
	{"sha384", sha512.New384, true},
	{"sha512", sha512.New, true},
	{"sha512-256", sha512.New512_256, true},
	{"sha3-256", func() hash.Hash { return sha3.New256() }, true},
	{"sha3-512", func() hash.Hash { return sha3.New512() }, true},
	{"blake2b-256", func() hash.Hash { h, _ := blake2b.New256(nil); return h }, true},
	{"blake2b-512", func() hash.Hash { h, _ := blake2b.New512(nil); return h }, true},
	{"crc32", func() hash.Hash { return crc32.NewIEEE() }, false},
}

func hashTool() *core.Tool {
	choices := []string{"all"}
	for _, a := range hashAlgos {
		choices = append(choices, a.name)
	}
	return &core.Tool{
		ID:          "hash",
		Name:        "Hash Generator",
		Category:    core.CatGenerate,
		Description: "Compute MD5, SHA-1/2/3, BLAKE2b, CRC32 and more for text or files — with optional HMAC key and hex/base64 output.",
		Aliases:     []string{"md5", "sha", "sha256", "checksum", "hmac"},
		Keywords:    []string{"digest", "fingerprint", "sha1", "sha512", "sha3", "blake2", "crc"},
		Input:       core.InputImage, // accepts any file
		Placeholder: "Text to hash — or drop a file",
		Args:        []string{"?algorithm", "input"},
		Options: []core.Option{
			{Key: "algorithm", Label: "Algorithm", Type: core.OptSelect, Default: "all", Choices: choices, Short: "a"},
			{Key: "hmac_key", Label: "HMAC key", Type: core.OptString, Default: "", Placeholder: "optional secret", Short: "k", Sensitive: true},
			{Key: "encoding", Label: "Encoding", Type: core.OptSelect, Default: "hex", Choices: []string{"hex", "base64"}},
			{Key: "upper", Label: "Uppercase", Type: core.OptBool, Default: "false"},
		},
		Sample: "The quick brown fox jumps over the lazy dog",
		Run: func(r core.Request) (*core.Result, error) {
			data := []byte(r.Input)
			src := "string"
			if len(r.Data) > 0 {
				data, src = r.Data, "file"
			}
			key := r.Str("hmac_key")
			var lines []string
			res := &core.Result{}
			for _, a := range hashAlgos {
				if r.Str("algorithm") != "all" && r.Str("algorithm") != a.name {
					continue
				}
				var h hash.Hash
				label := strings.ToUpper(a.name)
				if key != "" {
					if !a.hmac {
						continue
					}
					h = hmac.New(a.new, []byte(key))
					label = "HMAC-" + label
				} else {
					h = a.new()
				}
				h.Write(data)
				sum := h.Sum(nil)
				var s string
				if r.Str("encoding") == "base64" {
					s = base64.StdEncoding.EncodeToString(sum)
				} else {
					s = hex.EncodeToString(sum)
					if r.Bool("upper") {
						s = strings.ToUpper(s)
					}
				}
				res.Fields = append(res.Fields, core.Field{Label: label, Value: s})
				lines = append(lines, s)
			}
			if r.Str("algorithm") == "all" {
				var b strings.Builder
				for _, f := range res.Fields {
					fmt.Fprintf(&b, "%-16s %s\n", f.Label, f.Value)
				}
				res.Output = strings.TrimRight(b.String(), "\n")
			} else {
				res.Output = strings.Join(lines, "\n")
			}
			res.Fields = append([]core.Field{{Label: "Input", Value: fmt.Sprintf("%d bytes (%s)", len(data), src)}}, res.Fields...)
			return res, nil
		},
	}
}

// maxBcryptCost bounds both hashing and verifying. Cost 16 takes a few
// seconds; real-world hashes use 10–14.
const maxBcryptCost = 16

// bcryptEstimate is a rough wall time for a cost, from ~60 ms at cost 10.
func bcryptEstimate(cost int) string {
	d := time.Duration(float64(60*time.Millisecond) * math.Pow(2, float64(cost-10)))
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	case d >= 2*time.Hour:
		return fmt.Sprintf("%.0f hours", d.Hours())
	case d >= 2*time.Minute:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	}
	return fmt.Sprintf("%.0f seconds", d.Seconds())
}

func bcryptTool() *core.Tool {
	return &core.Tool{
		ID:          "bcrypt",
		Name:        "Bcrypt Hash/Verify",
		Category:    core.CatGenerate,
		Description: "Hash a password with bcrypt (configurable cost) or check a password against an existing bcrypt hash.",
		Aliases:     []string{"password-hash"},
		Keywords:    []string{"password", "hash", "verify", "salt"},
		Extra:       true,
		// the input is a password in both modes
		SensitiveInput: true,
		Options: []core.Option{
			optMode("hash", "hash", "verify"),
			{Key: "cost", Label: "Cost", Type: core.OptNumber, Default: "10", Help: "4–16; each +1 doubles the work"},
			{Key: "hash", Label: "Hash to verify", Type: core.OptString, Default: "", Placeholder: "$2a$10$..."},
		},
		Sample: "correct horse battery staple",
		Detect: func(in string) *core.Detection {
			if reBcryptHash.MatchString(strings.TrimSpace(in)) {
				return det(90, "bcrypt hash")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			if r.Str("mode") == "verify" {
				h := strings.TrimSpace(r.Str("hash"))
				if h == "" {
					return nil, fmt.Errorf("provide the bcrypt hash to verify against (--hash)")
				}
				// The cost comes from the pasted hash and each step doubles the
				// work: $2a$31$ is about a day of CPU, rerun on every keystroke.
				if cost, err := bcrypt.Cost([]byte(h)); err == nil && cost > maxBcryptCost {
					return nil, fmt.Errorf("this hash uses cost %d; devknife verifies up to cost %d (each step doubles the time, so cost %d would take about %s)", cost, maxBcryptCost, cost, bcryptEstimate(cost))
				}
				err := bcrypt.CompareHashAndPassword([]byte(h), []byte(r.Input))
				if err == bcrypt.ErrMismatchedHashAndPassword {
					return &core.Result{Output: "✗ Password does NOT match"}, nil
				} else if err != nil {
					return nil, err
				}
				cost, _ := bcrypt.Cost([]byte(h))
				return &core.Result{Output: "✓ Password matches", Fields: fields("Cost", strconv.Itoa(cost))}, nil
			}
			if len(r.Input) > 72 {
				return nil, fmt.Errorf("bcrypt only uses the first 72 bytes of a password (input is %d bytes)", len(r.Input))
			}
			cost := max(bcrypt.MinCost, min(r.Int("cost", 10), maxBcryptCost))
			start := time.Now()
			h, err := bcrypt.GenerateFromPassword([]byte(r.Input), cost)
			if err != nil {
				return nil, err
			}
			return &core.Result{Output: string(h), Fields: fields("Cost", strconv.Itoa(cost), "Took", time.Since(start).Round(time.Millisecond).String())}, nil
		},
	}
}

// ---------------------------------------------------------------------------

const (
	charsUpper   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	charsLower   = "abcdefghijklmnopqrstuvwxyz"
	charsDigits  = "0123456789"
	charsSymbols = "!@#$%^&*()-_=+[]{};:,.<>/?~"
	charsSimilar = "Il1O0o"
)

func randomStringTool() *core.Tool {
	return &core.Tool{
		ID:          "random",
		Name:        "Random String Generator",
		Category:    core.CatGenerate,
		Description: "Cryptographically secure random strings, passwords, tokens and PINs. Pick character sets, exclude look-alikes, group with separators; shows the entropy.",
		Aliases:     []string{"password", "rand", "pwgen"},
		Keywords:    []string{"secret", "api key", "pin", "passphrase", "generator"},
		Input:       core.InputNone,
		Args:        []string{"length"},
		Options: []core.Option{
			{Key: "length", Label: "Length", Type: core.OptNumber, Default: "24", Short: "l"},
			{Key: "count", Label: "Count", Type: core.OptNumber, Default: "10", Short: "n"},
			{Key: "upper", Label: "A-Z", Type: core.OptBool, Default: "true"},
			{Key: "lower", Label: "a-z", Type: core.OptBool, Default: "true"},
			{Key: "digits", Label: "0-9", Type: core.OptBool, Default: "true"},
			{Key: "symbols", Label: "Symbols", Type: core.OptBool, Default: "false"},
			{Key: "no_similar", Label: "No look-alikes", Type: core.OptBool, Default: "false", Help: "Exclude I l 1 O 0 o"},
			{Key: "custom", Label: "Custom set", Type: core.OptString, Default: "", Placeholder: "overrides the sets above"},
			{Key: "group", Label: "Group size", Type: core.OptNumber, Default: "0", Help: "Insert the separator every N characters"},
			{Key: "separator", Label: "Separator", Type: core.OptString, Default: "-"},
		},
		Templates: []core.Template{
			{Name: "Strong password", Opts: map[string]string{"length": "24", "count": "5", "upper": "true", "lower": "true", "digits": "true", "symbols": "true", "no_similar": "false", "custom": "", "group": "0"}},
			{Name: "Memorable (grouped)", Opts: map[string]string{"length": "20", "count": "5", "upper": "true", "lower": "true", "digits": "true", "symbols": "false", "no_similar": "true", "custom": "", "group": "5", "separator": "-"}},
			{Name: "API key (base62, 40)", Opts: map[string]string{"length": "40", "count": "5", "upper": "true", "lower": "true", "digits": "true", "symbols": "false", "custom": "", "group": "0"}},
			{Name: "Hex token (64)", Opts: map[string]string{"length": "64", "count": "5", "custom": "0123456789abcdef", "group": "0"}},
			{Name: "PIN (6 digits)", Opts: map[string]string{"length": "6", "count": "10", "custom": "0123456789", "group": "0"}},
		},
		Run: func(r core.Request) (*core.Result, error) {
			set := r.Str("custom")
			if set == "" {
				if r.Bool("upper") {
					set += charsUpper
				}
				if r.Bool("lower") {
					set += charsLower
				}
				if r.Bool("digits") {
					set += charsDigits
				}
				if r.Bool("symbols") {
					set += charsSymbols
				}
				if r.Bool("no_similar") {
					set = strings.Map(func(c rune) rune {
						if strings.ContainsRune(charsSimilar, c) {
							return -1
						}
						return c
					}, set)
				}
			}
			uniq := map[rune]bool{}
			for _, c := range set {
				uniq[c] = true
			}
			if len(uniq) == 0 {
				return nil, fmt.Errorf("select at least one character set")
			}
			alphabet := make([]rune, 0, len(uniq))
			for _, c := range set {
				if uniq[c] {
					alphabet = append(alphabet, c)
					uniq[c] = false
				}
			}
			length := max(1, min(r.Int("length", 24), 4096))
			count := max(1, min(r.Int("count", 10), 1000))
			group := r.Int("group", 0)
			out := make([]string, count)
			for i := range out {
				s := randomFrom(string(alphabet), length)
				if group > 0 {
					rs := []rune(s)
					var parts []string
					for j := 0; j < len(rs); j += group {
						parts = append(parts, string(rs[j:min(j+group, len(rs))]))
					}
					s = strings.Join(parts, r.Str("separator"))
				}
				out[i] = s
			}
			bits := float64(length) * math.Log2(float64(len(alphabet)))
			strength := "weak"
			switch {
			case bits >= 128:
				strength = "excellent"
			case bits >= 80:
				strength = "strong"
			case bits >= 60:
				strength = "fair"
			}
			return &core.Result{Output: strings.Join(out, "\n"), Fields: fields("Alphabet size", strconv.Itoa(len(alphabet)), "Entropy", fmt.Sprintf("%.0f bits (%s)", bits, strength))}, nil
		},
	}
}

func generateTools() []*core.Tool {
	return []*core.Tool{uuidTool(), loremTool(), qrTool(), hashTool(), randomStringTool(), bcryptTool()}
}

// Compiled once: detectors run on every paste, tools on every keystroke.
var (
	reBcryptHash = regexp.MustCompile(`^\$2[abxy]?\$\d{2}\$[./A-Za-z0-9]{53}$`)
)
