package sender

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/smtp"
	"strings"

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

	buf := bytes.NewBuffer(nil)
	writer := multipart.NewWriter(buf)
	boundary := writer.Boundary()
	altBuf := &bytes.Buffer{}
	altWriter := multipart.NewWriter(altBuf)
	altBoundary := altWriter.Boundary()
	subject := getSubject(properties)
	nl := "\r\n"

	buf.WriteString(fmt.Sprintf(`Content-Type: multipart/mixed; boundary="%s"%s`, boundary, nl))

	buf.WriteString(fmt.Sprintf("Date: %s\r\n", "abc"))
	buf.WriteString("MIME-version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("User-Agent: %s\r\n", s.ua))
	buf.WriteString("Content-Language: en-US\r\n")
	buf.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(to, ",")))
	buf.WriteString(fmt.Sprintf("From: %s\r\n", s.from))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buf.WriteString("\r\nThis is a multi-part message in MIME format.\r\n")

	buf.WriteString(fmt.Sprintf("--%s%s", boundary, nl))
	buf.WriteString(fmt.Sprintf(
		`Content-Type: multipart/alternative;%s boundary="%s"%s%s`,
		nl, altBoundary, nl, nl,
	))

	buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
	buf.WriteString("Content-Type: text/plain; charset=UTF-8; format=flowed\r\n")
	buf.WriteString("Content-Transfer-Encoding: 7bit\r\n")

	buf.WriteString(fmt.Sprintf("\r\n%s\r\n\r\n", message))

	buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 7bit\r\n")

	buf.WriteString("\r\n")
	buf.WriteString(`<!DOCTYPE html><html><head><meta http-equiv="content-type" content="text/html; charset=UTF-8"></head><body>`)
	message = strings.ReplaceAll(message, "\n", "<br>\n")
	buf.WriteString(message)
	buf.WriteString(`</body></html>`)

	buf.WriteString(fmt.Sprintf("\r\n\r\n--%s--\r\n", altBoundary))

	if hasAttachments(attachments) {
		for _, a := range attachments {
			fdata, err := base64.StdEncoding.DecodeString(a.content)
			if err != nil {
				logger.Error().Err(err).Msg("Error decoding string")
				continue
			}

			buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
			buf.WriteString(fmt.Sprintf(`Content-Type: %s; name="%s"%s`, http.DetectContentType(fdata), a.fileName, nl))
			buf.WriteString(fmt.Sprintf(`Content-Disposition: attachment; filename="%s"%s`, a.fileName, nl))
			buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
			buf.WriteString(a.content)
			buf.WriteString("\r\n")
		}

		buf.WriteString(fmt.Sprintf("\r\n\r\n--%s--", boundary))
	}

	logger.Debug().Str("full_message", buf.String()).Msg("Sending")

	auth := smtp.PlainAuth("", s.from, s.password, s.host)

	err := smtp.SendMail(smtpAddress(s.host, s.port), auth, s.from, to, buf.Bytes())
	if err != nil {
		logger.Error().Err(err).Msg("Failed sending email")

		return err
	}

	logger.Debug().Msg("Sent email")

	return nil
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
