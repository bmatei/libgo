package sender

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/smtp"
	"net/textproto"
	"regexp"
	"strings"
	"time"

	"github.com/bmatei/libgo/observability/logs"
)

type smtpPlainText struct {
	from     string
	host     string
	port     int
	password string
	ua       string
}

type attachment struct {
	fileName string
	content  string
}

// NewSMTPPlainText returns a Sender that emails message as the text/plain part and
// an HTML rendering of it as the text/html part. The HTML part is the message
// HTML-escaped, with http(s) URLs turned into links and newlines into <br>. Pass
// NewHTMLBody to send a caller-built HTML part instead; raw HTML in message is
// escaped.
func NewSMTPPlainText(from, host string, port int, password, ua string) *smtpPlainText {
	return &smtpPlainText{
		from:     from,
		host:     host,
		port:     port,
		password: password,
		ua:       ua,
	}
}

func (s *smtpPlainText) Send(ctx context.Context, message string, properties ...MessageProperty) error {
	to := getRecipients(properties)
	if len(to) == 0 {
		return fmt.Errorf("need someone to send email to")
	}

	attachments := getAttachments(properties)

	logger := logs.FromContext(ctx).With().
		Str("from", s.from).
		Str("host", s.host).
		Int("port", s.port).
		Strs("to", to).
		Str("text", message).
		Bool("has_attachments", hasAttachments(attachments)).
		Logger()

	logger.Debug().Msg("Sending message")

	valid := make([]attachment, 0, len(attachments))
	for _, a := range attachments {
		if _, err := base64.StdEncoding.DecodeString(a.content); err != nil {
			logger.Error().Err(err).Str("file_name", a.fileName).Msg("Error decoding string")
			continue
		}

		valid = append(valid, a)
	}

	msg, err := buildMessage(email{
		from:        s.from,
		to:          to,
		subject:     getSubject(properties),
		text:        message,
		html:        getHTMLBody(properties),
		ua:          s.ua,
		attachments: valid,
	}, time.Now())
	if err != nil {
		logger.Error().Err(err).Msg("Failed building email")

		return err
	}

	logger.Debug().Str("full_message", string(msg)).Msg("Sending")

	auth := smtp.PlainAuth("", s.from, s.password, s.host)

	err = smtp.SendMail(smtpAddress(s.host, s.port), auth, s.from, to, msg)
	if err != nil {
		logger.Error().Err(err).Msg("Failed sending email")

		return err
	}

	logger.Debug().Msg("Sent email")

	return nil
}

type email struct {
	from    string
	to      []string
	subject string
	text    string
	// html is the text/html part; empty means render it from text.
	html        string
	ua          string
	attachments []attachment
}

// buildMessage renders e as a MIME message. Without attachments the top level is
// multipart/alternative (text + html); with attachments it is multipart/mixed,
// wrapping the alternative part followed by the attachments. Every attachment's
// content must be valid base64.
func buildMessage(e email, now time.Time) ([]byte, error) {
	htmlBody := e.html
	if htmlBody == "" {
		htmlBody = textToHTML(e.text)
	}

	alt := &bytes.Buffer{}
	altWriter := multipart.NewWriter(alt)
	if err := writeQuotedPrintablePart(altWriter, "text/plain; charset=UTF-8", e.text); err != nil {
		return nil, err
	}
	if err := writeQuotedPrintablePart(altWriter, "text/html; charset=UTF-8", htmlBody); err != nil {
		return nil, err
	}
	if err := altWriter.Close(); err != nil {
		return nil, err
	}
	altType := mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": altWriter.Boundary()})

	messageID, err := newMessageID(e.from)
	if err != nil {
		return nil, err
	}

	buf := &bytes.Buffer{}
	writeHeader := func(key, value string) {
		fmt.Fprintf(buf, "%s: %s\r\n", key, value)
	}

	writeHeader("MIME-Version", "1.0")
	writeHeader("Date", now.Format(time.RFC1123Z))
	writeHeader("Message-ID", messageID)
	writeHeader("User-Agent", e.ua)
	writeHeader("Content-Language", "en-US")
	writeHeader("To", strings.Join(e.to, ","))
	writeHeader("From", e.from)
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", e.subject))

	if !hasAttachments(e.attachments) {
		writeHeader("Content-Type", altType)
		buf.WriteString("\r\n")
		buf.Write(alt.Bytes())

		return buf.Bytes(), nil
	}

	mixed := multipart.NewWriter(buf)
	writeHeader("Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()}))
	buf.WriteString("\r\nThis is a multi-part message in MIME format.\r\n")

	part, err := mixed.CreatePart(textproto.MIMEHeader{"Content-Type": {altType}})
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(alt.Bytes()); err != nil {
		return nil, err
	}

	for _, a := range e.attachments {
		if err := writeAttachmentPart(mixed, a); err != nil {
			return nil, err
		}
	}

	if err := mixed.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func writeQuotedPrintablePart(w *multipart.Writer, contentType, body string) error {
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {contentType},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}

	qp := quotedprintable.NewWriter(part)
	if _, err := io.WriteString(qp, body); err != nil {
		return err
	}

	return qp.Close()
}

// base64LineLength is RFC 2045's maximum encoded line length.
const base64LineLength = 76

func writeAttachmentPart(w *multipart.Writer, a attachment) error {
	data, err := base64.StdEncoding.DecodeString(a.content)
	if err != nil {
		return fmt.Errorf("decoding attachment %q: %w", a.fileName, err)
	}

	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {fmt.Sprintf(`%s; name="%s"`, http.DetectContentType(data), a.fileName)},
		"Content-Disposition":       {fmt.Sprintf(`attachment; filename="%s"`, a.fileName)},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 0 {
		n := min(base64LineLength, len(encoded))
		if _, err := io.WriteString(part, encoded[:n]+"\r\n"); err != nil {
			return err
		}
		encoded = encoded[n:]
	}

	return nil
}

func newMessageID(from string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}

	domain := "localhost"
	if at := strings.LastIndex(from, "@"); at >= 0 {
		domain = strings.TrimRight(from[at+1:], ">")
	}

	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(random), domain), nil
}

const (
	htmlPrefix = `<!DOCTYPE html><html><head><meta http-equiv="content-type" content="text/html; charset=UTF-8"></head><body>`
	htmlSuffix = `</body></html>`
)

var urlPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

// textToHTML renders plain text as an HTML document: escaped, with http(s) URLs
// as links and newlines as <br>. URLs are found in the raw text, so escaping
// can't leak entities such as &gt; into a link.
func textToHTML(text string) string {
	var b strings.Builder

	last := 0
	for _, loc := range urlPattern.FindAllStringIndex(text, -1) {
		start := loc[0]
		end := start + len(trimURL(text[start:loc[1]]))

		b.WriteString(html.EscapeString(text[last:start]))
		u := html.EscapeString(text[start:end])
		fmt.Fprintf(&b, `<a href="%s">%s</a>`, u, u)
		last = end
	}
	b.WriteString(html.EscapeString(text[last:]))

	return htmlPrefix + strings.ReplaceAll(b.String(), "\n", "<br>\n") + htmlSuffix
}

// trimURL drops trailing punctuation that ends the sentence rather than the URL.
// A trailing ")" is kept only when it closes a "(" inside the URL.
func trimURL(u string) string {
	for len(u) > 0 {
		last := u[len(u)-1]
		switch {
		case strings.IndexByte(".,;:!?'", last) >= 0:
			u = u[:len(u)-1]
		case last == ')' && strings.Count(u, "(") < strings.Count(u, ")"):
			u = u[:len(u)-1]
		default:
			return u
		}
	}

	return u
}

func smtpAddress(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

func hasAttachments(attachments []attachment) bool {
	return len(attachments) > 0
}

func getSubject(properties []MessageProperty) string {
	for _, p := range properties {
		if p.Type == MessagePropertySubject {
			return p.Value
		}
	}

	return ""
}

func getHTMLBody(properties []MessageProperty) string {
	for _, p := range properties {
		if p.Type == MessagePropertyHTMLBody {
			return p.Value
		}
	}

	return ""
}

func getRecipients(properties []MessageProperty) []string {
	recipients := []string{}

	for _, p := range properties {
		if p.Type == MessagePropertyRecipient {
			recipients = append(recipients, p.Value)
		}
	}

	return recipients
}

func getAttachments(properties []MessageProperty) []attachment {
	attachments := []attachment{}

	for _, p := range properties {
		if p.Type == MessagePropertyAttachment {
			attachments = append(attachments, attachment{
				fileName: AttachmentGetFileName(p),
				content:  AttachmentGetContent(p),
			})
		}
	}

	return attachments
}
