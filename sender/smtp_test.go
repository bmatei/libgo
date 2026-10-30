package sender

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 30, 14, 52, 0, 0, time.FixedZone("EET", 2*60*60))

type testPart struct {
	contentType string
	fileName    string
	body        string
}

func testEmail() email {
	return email{
		from:    "noreply@example.com",
		to:      []string{"a@example.com", "b@example.com"},
		subject: "Hello",
		text:    "Hi there",
		ua:      "test",
	}
}

func build(t *testing.T, e email) []byte {
	t.Helper()

	raw, err := buildMessage(e, testNow)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	return raw
}

func parse(t *testing.T, raw []byte) *mail.Message {
	t.Helper()

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v", err)
	}

	return msg
}

// readMultipart reads every part of a multipart body, failing unless the closing
// boundary is reached. Quoted-printable parts come back decoded.
func readMultipart(t *testing.T, contentType string, body io.Reader) (string, []testPart) {
	t.Helper()

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parsing Content-Type %q: %v", contentType, err)
	}

	var parts []testPart
	r := multipart.NewReader(body, params["boundary"])
	for {
		p, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			return mediaType, parts
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}

		b, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("reading part: %v", err)
		}

		parts = append(parts, testPart{
			contentType: p.Header.Get("Content-Type"),
			fileName:    p.FileName(),
			body:        string(b),
		})
	}
}

// alternative returns the decoded text/plain and text/html parts of a message
// without attachments.
func alternative(t *testing.T, e email) (string, string) {
	t.Helper()

	msg := parse(t, build(t, e))
	mediaType, parts := readMultipart(t, msg.Header.Get("Content-Type"), msg.Body)
	if mediaType != "multipart/alternative" || len(parts) != 2 {
		t.Fatalf("got %s with %d parts, want multipart/alternative with 2", mediaType, len(parts))
	}

	return parts[0].body, parts[1].body
}

func htmlBodyOf(t *testing.T, text string) string {
	t.Helper()

	e := testEmail()
	e.text = text
	_, h := alternative(t, e)

	return strings.TrimSuffix(strings.TrimPrefix(h, htmlPrefix), htmlSuffix)
}

func TestBuildMessageWithoutAttachments(t *testing.T) {
	msg := parse(t, build(t, testEmail()))

	mediaType, parts := readMultipart(t, msg.Header.Get("Content-Type"), msg.Body)
	if mediaType != "multipart/alternative" {
		t.Fatalf("top level is %s, want multipart/alternative", mediaType)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(parts))
	}
	if !strings.HasPrefix(parts[0].contentType, "text/plain") {
		t.Errorf("first part is %s, want text/plain", parts[0].contentType)
	}
	if !strings.HasPrefix(parts[1].contentType, "text/html") {
		t.Errorf("second part is %s, want text/html", parts[1].contentType)
	}
}

func TestBuildMessageWithAttachments(t *testing.T) {
	files := map[string][]byte{
		"report.pdf": bytes.Repeat([]byte("%PDF-1.4 binary \x00\x01\x02"), 100),
		"notes.txt":  []byte("some notes"),
	}

	e := testEmail()
	for _, name := range []string{"report.pdf", "notes.txt"} {
		e.attachments = append(e.attachments, attachment{
			fileName: name,
			content:  base64.StdEncoding.EncodeToString(files[name]),
		})
	}

	msg := parse(t, build(t, e))
	mediaType, parts := readMultipart(t, msg.Header.Get("Content-Type"), msg.Body)
	if mediaType != "multipart/mixed" {
		t.Fatalf("top level is %s, want multipart/mixed", mediaType)
	}
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}

	altType, altParts := readMultipart(t, parts[0].contentType, strings.NewReader(parts[0].body))
	if altType != "multipart/alternative" || len(altParts) != 2 {
		t.Fatalf("first part is %s with %d parts, want multipart/alternative with 2", altType, len(altParts))
	}
	if altParts[0].body != e.text {
		t.Errorf("text part = %q, want %q", altParts[0].body, e.text)
	}

	for i, name := range []string{"report.pdf", "notes.txt"} {
		p := parts[i+1]
		if p.fileName != name {
			t.Errorf("attachment %d filename = %q, want %q", i, p.fileName, name)
		}

		decoded, err := base64.StdEncoding.DecodeString(p.body)
		if err != nil {
			t.Fatalf("decoding attachment %s: %v", name, err)
		}
		if !bytes.Equal(decoded, files[name]) {
			t.Errorf("attachment %s content differs", name)
		}
	}
}

func TestBuildMessageNonASCIIBody(t *testing.T) {
	e := testEmail()
	e.text = "Café — déjà vu\n" + strings.Repeat("é—", 1000) + "\nend"

	raw := build(t, e)
	for i, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line %d is %d octets, want <= 998", i, len(line))
		}
	}

	text, _ := alternative(t, e)
	if got := strings.ReplaceAll(text, "\r\n", "\n"); got != e.text {
		t.Errorf("text part does not decode back exactly:\n got %q\nwant %q", got, e.text)
	}
}

func TestBuildMessageSubject(t *testing.T) {
	tests := []struct {
		subject string
		encoded bool
	}{
		{"Plain ASCII subject", false},
		{"Bine ai venit la Café — ACCPI", true},
	}

	for _, tt := range tests {
		e := testEmail()
		e.subject = tt.subject

		raw := parse(t, build(t, e)).Header.Get("Subject")
		if encoded := strings.HasPrefix(raw, "=?utf-8?"); encoded != tt.encoded {
			t.Errorf("Subject %q: raw header %q, encoded = %v, want %v", tt.subject, raw, encoded, tt.encoded)
		}

		decoded, err := new(mime.WordDecoder).DecodeHeader(raw)
		if err != nil {
			t.Fatalf("decoding Subject %q: %v", raw, err)
		}
		if decoded != tt.subject {
			t.Errorf("Subject decodes to %q, want %q", decoded, tt.subject)
		}
	}
}

func TestBuildMessageDate(t *testing.T) {
	date, err := mail.ParseDate(parse(t, build(t, testEmail())).Header.Get("Date"))
	if err != nil {
		t.Fatalf("parsing Date: %v", err)
	}
	if !date.Equal(testNow) {
		t.Errorf("Date = %v, want %v", date, testNow)
	}
}

func TestBuildMessageMessageID(t *testing.T) {
	first := parse(t, build(t, testEmail())).Header.Get("Message-ID")
	second := parse(t, build(t, testEmail())).Header.Get("Message-ID")

	for _, id := range []string{first, second} {
		if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@example.com>") {
			t.Errorf("Message-ID = %q, want <…@example.com>", id)
		}
	}
	if first == second {
		t.Errorf("Message-ID %q repeated across calls", first)
	}
}

func TestBuildMessageHTMLBody(t *testing.T) {
	e := testEmail()
	e.text = "Plain version"
	e.html = `<p>Hello <b>there</b> — <a href="https://example.com/?a=1&b=2">link</a></p>`

	text, h := alternative(t, e)
	if text != e.text {
		t.Errorf("text part = %q, want %q", text, e.text)
	}
	if h != e.html {
		t.Errorf("html part = %q, want %q", h, e.html)
	}
}

func TestBuildMessageHTMLFallbackLinks(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "single URL",
			text: "Go to https://example.com/verify",
			want: `Go to <a href="https://example.com/verify">https://example.com/verify</a>`,
		},
		{
			name: "trailing period",
			text: "Verify at https://example.com/verify.",
			want: `Verify at <a href="https://example.com/verify">https://example.com/verify</a>.`,
		},
		{
			name: "multiple URLs and query strings",
			text: "A http://a.example/x?token=abc&x=1, B https://b.example/y",
			want: `A <a href="http://a.example/x?token=abc&amp;x=1">http://a.example/x?token=abc&amp;x=1</a>, ` +
				`B <a href="https://b.example/y">https://b.example/y</a>`,
		},
		{
			name: "angle brackets",
			text: "See <https://x.example>",
			want: `See &lt;<a href="https://x.example">https://x.example</a>&gt;`,
		},
		{
			name: "balanced parens",
			text: "Wiki https://en.wikipedia.org/wiki/Foo_(bar)",
			want: `Wiki <a href="https://en.wikipedia.org/wiki/Foo_(bar)">https://en.wikipedia.org/wiki/Foo_(bar)</a>`,
		},
		{
			name: "closing paren after URL",
			text: "(see https://example.com/x)",
			want: `(see <a href="https://example.com/x">https://example.com/x</a>)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := htmlBodyOf(t, tt.text); got != tt.want {
				t.Errorf("html body:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestBuildMessageHTMLFallbackEscapes(t *testing.T) {
	got := htmlBodyOf(t, `<script>alert("x")</script> & co`)
	want := `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; co`
	if got != want {
		t.Errorf("html body:\n got %q\nwant %q", got, want)
	}
}

func TestBuildMessageHTMLFallbackNewlines(t *testing.T) {
	got := strings.ReplaceAll(htmlBodyOf(t, "one\ntwo\nthree"), "\r\n", "\n")
	want := "one<br>\ntwo<br>\nthree"
	if got != want {
		t.Errorf("html body:\n got %q\nwant %q", got, want)
	}
}
