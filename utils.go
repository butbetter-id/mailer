package mailer

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"mime"
	"mime/quotedprintable"
	"os"
	"path/filepath"
	"strings"
)

type (
	mimeEncoder struct {
		mime.WordEncoder
	}

	file struct {
		Name     string
		Header   map[string][]string
		CopyFunc func(w io.Writer) error
	}

	// header type represents an request header
	header map[string][]string

	// Encoding represents a MIME encoding scheme like quoted-printable or base64.
	Encoding string

	// A MessageSetting can be used as an argument in NewMessage to configure an email.
	MessageSetting func(m *Message)

	// A FileSetting can be used as an argument in Message.Attach or Message.Embed.
	FileSetting func(*file)

	part struct {
		contentType string
		copier      func(io.Writer) error
		encoding    Encoding
	}

	// A PartSetting can be used as an argument in Message.SetBody,
	// Message.AddAlternative or Message.AddAlternativeWriter to configure the part
	// added to a message.
	PartSetting func(*part)

	// base64LineWriter limits text encoded in base64 to 76 characters per line
	base64LineWriter struct {
		w       io.Writer
		lineLen int
	}
)

var (
	newQPWriter   = quotedprintable.NewWriter
	bEncoding     = mimeEncoder{mime.BEncoding}
	qEncoding     = mimeEncoder{mime.QEncoding}
	lastIndexByte = strings.LastIndexByte
)

const (
	// QuotedPrintable represents the quoted-printable encoding as defined in
	// RFC 2045.
	QuotedPrintable Encoding = "quoted-printable"
	// Base64 represents the base64 encoding as defined in RFC 2045.
	Base64 Encoding = "base64"
	// Unencoded can be used to avoid encoding the body of an email. The headers
	// will still be encoded using quoted-printable encoding.
	Unencoded Encoding = "8bit"

	// As required by RFC 2045, 6.7. (page 21) for quoted-printable, and
	// RFC 2045, 6.8. (page 25) for base64.
	maxLineLen = 76
)

func (f *file) setHeader(field, value string) {
	f.Header[field] = []string{value}
}

func newBase64LineWriter(w io.Writer) *base64LineWriter {
	return &base64LineWriter{w: w}
}

func (w *base64LineWriter) Write(p []byte) (int, error) {
	n := 0
	for len(p)+w.lineLen > maxLineLen {
		w.w.Write(p[:maxLineLen-w.lineLen])
		w.w.Write([]byte("\r\n"))
		p = p[maxLineLen-w.lineLen:]
		n += maxLineLen - w.lineLen
		w.lineLen = 0
	}

	w.w.Write(p)
	w.lineLen += len(p)

	return n + len(p), nil
}

// SetCharset is a message setting to set the charset of the email.
func SetCharset(charset string) MessageSetting {
	return func(m *Message) {
		m.charset = charset
	}
}

// SetEncoding is a message setting to set the encoding of the email.
func SetEncoding(enc Encoding) MessageSetting {
	return func(m *Message) {
		m.encoding = enc
	}
}

// ParseTemplate perform template parsing from path into template html
func ParseTemplate(filename string, data interface{}) string {
	tf := filepath.Join(os.Getenv("EMAIL_TEMPLATE_DIR"), filename)

	t, err := template.ParseFiles(tf)
	if err != nil {
		panic("mailer: Error when parsing template, " + err.Error())
	}

	buf := new(bytes.Buffer)
	if err := t.Execute(buf, data); err != nil {
		panic("mailer: Error when compiling template, " + err.Error())
	}

	return buf.String()
}

func hasSpecials(text string) bool {
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '(', ')', '<', '>', '[', ']', ':', ';', '@', '\\', ',', '.', '"':
			return true
		}
	}

	return false
}

func newCopier(s string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	}
}

// SetHeader is a file setting to set the MIME header of the message part that
// contains the file content.
//
// Mandatory headers are automatically added if they are not set when sending
// the email.
func SetHeader(h map[string][]string) FileSetting {
	return func(f *file) {
		for k, v := range h {
			f.Header[k] = v
		}
	}
}

// Rename is a file setting to set the name of the attachment if the name is
// different than the filename on disk.
func Rename(name string) FileSetting {
	return func(f *file) {
		f.Name = name
	}
}

// SetCopyFunc is a file setting to replace the function that runs when the
// message is sent. It should copy the content of the file to the io.Writer.
//
// The default copy function opens the file with the given filename, and copy
// its content to the io.Writer.
func SetCopyFunc(f func(io.Writer) error) FileSetting {
	return func(fi *file) {
		fi.CopyFunc = f
	}
}

// SetPartEncoding sets the encoding of the part added to the message. By
// default, parts use the same encoding than the message.
func SetPartEncoding(e Encoding) PartSetting {
	return PartSetting(func(p *part) {
		p.encoding = e
	})
}

func addr(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}
