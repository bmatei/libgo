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
