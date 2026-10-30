package sender

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	MessagePropertyAttachment = "attachment"
	MessagePropertySubject    = "subject"
	MessagePropertyRecipient  = "recipient"
	MessagePropertyHTMLBody   = "html_body"
)

func NewAttachmentFromFilePath(path string) (MessageProperty, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return MessageProperty{}, err
	}

	return NewAttachment(filepath.Base(path), content), nil
}

func NewAttachment(fileName string, fileContent []byte) MessageProperty {
	encoded := base64.StdEncoding.EncodeToString(fileContent)

	return MessageProperty{
		Type:  MessagePropertyAttachment,
		Value: fmt.Sprintf("%s:%s", fileName, encoded),
	}
}

func AttachmentGetFileName(attachment MessageProperty) string {
	if attachment.Type == MessagePropertyAttachment {
		return strings.Split(attachment.Value, ":")[0]
	}

	return ""
}

func AttachmentGetContent(attachment MessageProperty) string {
	if attachment.Type == MessagePropertyAttachment {
		return strings.Split(attachment.Value, ":")[1]
	}

	return ""
}

func NewSubject(subject string) MessageProperty {
	return MessageProperty{
		Type:  MessagePropertySubject,
		Value: subject,
	}
}

func NewRecipient(address string) MessageProperty {
	return MessageProperty{
		Type:  MessagePropertyRecipient,
		Value: address,
	}
}

// NewHTMLBody sets the text/html part of an SMTP email; the message stays the
// text/plain part. html is sent as-is. Without it, the HTML part is rendered from
// the message, which is HTML-escaped. Slack and Discord ignore it.
func NewHTMLBody(html string) MessageProperty {
	return MessageProperty{
		Type:  MessagePropertyHTMLBody,
		Value: html,
	}
}
