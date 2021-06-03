package mailer

import (
	"bytes"
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type (
	// Message represents an email.
	Message struct {
		header      header
		parts       []*part
		attachments []*file
		embedded    []*file
		charset     string
		encoding    Encoding
		hEncoder    mimeEncoder
		buf         bytes.Buffer
	}

	messageWriter struct {
		w          io.Writer
		n          int64
		writers    [3]*multipart.Writer
		partWriter io.Writer
		depth      uint8
		err        error
	}
)

// Stubbed out for testing.
var now = time.Now

// NewMessage creates a new message. It uses UTF-8 and quoted-printable encoding
// by default.
func NewMessage(settings ...MessageSetting) *Message {
	m := &Message{
		header:   make(header),
		charset:  "UTF-8",
		encoding: QuotedPrintable,
	}

	m.applySettings(settings)

	if m.encoding == Base64 {
		m.hEncoder = bEncoding
	} else {
		m.hEncoder = qEncoding
	}

	if Config != nil {
		// Set From data Header from env variable
		m.SetAddressHeader("From", Config.SenderEmail, Config.SenderName)
	}

	return m
}

// From set sender
func (m *Message) From(email string, name string) *Message {
	m.SetAddressHeader("From", email, name)
	return m
}

// To set recipient
func (m *Message) To(to ...string) *Message {
	m.SetRecipient(to...)
	return m
}

// Subject set title
func (m *Message) Subject(to ...string) *Message {
	m.SetSubject(to...)
	return m
}

// Body set Body
func (m *Message) Body(body string, isHTML bool) *Message {
	if isHTML {
		m.SetBody("text/html", body)
	} else {
		m.SetBody("text/plain", body)
	}
	return m
}

// SetRecipient sets an list of recipient of this messages,
// it can be set multiple recipient, if you need to set email and name of recipient
// use FormatAddress instead of normal string.
func (m *Message) SetRecipient(address ...string) {
	m.encodeHeader(address)
	m.header["To"] = address
}

// SetSubject sets an value of subject email messages.
func (m *Message) SetSubject(subject ...string) {
	m.encodeHeader(subject)
	m.header["Subject"] = subject
}

// SetHeader sets a value to the given header field.
func (m *Message) SetHeader(field string, value ...string) {
	m.encodeHeader(value)
	m.header[field] = value
}

// SetHeaders sets the message headers.
func (m *Message) SetHeaders(h map[string][]string) {
	for k, v := range h {
		m.SetHeader(k, v...)
	}
}

// SetAddressHeader sets an address to the given header field.
func (m *Message) SetAddressHeader(field, address, name string) {
	m.header[field] = []string{m.FormatAddress(address, name)}
}

// SetDateHeader sets a date to the given header field.
func (m *Message) SetDateHeader(field string, date time.Time) {
	m.header[field] = []string{m.FormatDate(date)}
}

// SetBody sets the body of the message. It replaces any content previously set
// by SetBody, AddAlternative or AddAlternativeWriter.
func (m *Message) SetBody(contentType, body string, settings ...PartSetting) {
	m.parts = []*part{m.newPart(contentType, newCopier(body), settings)}
}

// GetHeader gets a header field.
func (m *Message) GetHeader(field string) []string {
	return m.header[field]
}

// FormatAddress formats an address and a name as a valid RFC 5322 address.
func (m *Message) FormatAddress(address, name string) string {
	if name == "" {
		return address
	}

	enc := m.encodeString(name)
	if enc == name {
		m.buf.WriteByte('"')
		for i := 0; i < len(name); i++ {
			b := name[i]
			if b == '\\' || b == '"' {
				m.buf.WriteByte('\\')
			}
			m.buf.WriteByte(b)
		}
		m.buf.WriteByte('"')
	} else if hasSpecials(name) {
		m.buf.WriteString(bEncoding.Encode(m.charset, name))
	} else {
		m.buf.WriteString(enc)
	}
	m.buf.WriteString(" <")
	m.buf.WriteString(address)
	m.buf.WriteByte('>')

	addr := m.buf.String()
	m.buf.Reset()
	return addr
}

// FormatDate formats a date as a valid RFC 5322 date.
func (m *Message) FormatDate(date time.Time) string {
	return date.Format(time.RFC1123Z)
}

// FormatHTML formats an html template with data interface that will be used as body
func (m *Message) FormatHTML(t *template.Template, data interface{}) string {
	buf := new(bytes.Buffer)
	if err := t.Execute(buf, data); err != nil {
		panic("mailer: Error when compiling template, " + err.Error())
	}

	return buf.String()
}

// AddAlternative adds an alternative part to the message.
//
// It is commonly used to send HTML emails that default to the plain text
// version for backward compatibility. AddAlternative appends the new part to
// the end of the message. So the plain text part should be added before the
// HTML part. See http://en.wikipedia.org/wiki/MIME#Alternative
func (m *Message) AddAlternative(contentType, body string, settings ...PartSetting) {
	m.AddAlternativeWriter(contentType, newCopier(body), settings...)
}

// AddAlternativeWriter adds an alternative part to the message. It can be
// useful with the text/template or html/template packages.
func (m *Message) AddAlternativeWriter(contentType string, f func(io.Writer) error, settings ...PartSetting) {
	m.parts = append(m.parts, m.newPart(contentType, f, settings))
}

// Attach attaches the files to the email.
func (m *Message) Attach(filename string, settings ...FileSetting) {
	m.attachments = m.appendFile(m.attachments, filename, settings)
}

// Embed embeds the images to the email.
func (m *Message) Embed(filename string, settings ...FileSetting) {
	m.embedded = m.appendFile(m.embedded, filename, settings)
}

// Reset resets the message so it can be reused. The message keeps its previous
// settings so it is in the same state that after a call to NewMessage.
func (m *Message) Reset() {
	for k := range m.header {
		delete(m.header, k)
	}
	m.parts = nil
	m.attachments = nil
	m.embedded = nil
}

// Send initialing new dialer with the messages and sending the email.
func (m *Message) Send() (err error) {
	d := NewDialer()
	if err = d.DialAndSend(m); err != nil {
		log.Fatal(err.Error())
	}
	return
}

func (m *Message) applySettings(settings []MessageSetting) {
	for _, s := range settings {
		s(m)
	}
}

func (m *Message) encodeHeader(values []string) {
	for i := range values {
		values[i] = m.encodeString(values[i])
	}
}

func (m *Message) encodeString(value string) string {
	return m.hEncoder.Encode(m.charset, value)
}

func (m *Message) newPart(contentType string, f func(io.Writer) error, settings []PartSetting) *part {
	p := &part{
		contentType: contentType,
		copier:      f,
		encoding:    m.encoding,
	}

	for _, s := range settings {
		s(p)
	}

	return p
}

func (m *Message) appendFile(list []*file, name string, settings []FileSetting) []*file {
	f := &file{
		Name:   filepath.Base(name),
		Header: make(map[string][]string),
		CopyFunc: func(w io.Writer) error {
			h, err := os.Open(name)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, h); err != nil {
				h.Close()
				return err
			}
			return h.Close()
		},
	}

	for _, s := range settings {
		s(f)
	}

	if list == nil {
		return []*file{f}
	}

	return append(list, f)
}

func (m *Message) getFrom() (string, error) {
	from := m.header["Sender"]
	if len(from) == 0 {
		from = m.header["From"]
		if len(from) == 0 {
			return "", errors.New(`mailer: invalid message, "From" field is absent`)
		}
	}

	return parseAddress(from[0])
}

func (m *Message) getRecipients() ([]string, error) {
	n := 0
	for _, field := range []string{"To", "Cc", "Bcc"} {
		if addresses, ok := m.header[field]; ok {
			n += len(addresses)
		}
	}
	list := make([]string, 0, n)

	for _, field := range []string{"To", "Cc", "Bcc"} {
		if addresses, ok := m.header[field]; ok {
			for _, a := range addresses {
				addr, err := parseAddress(a)
				if err != nil {
					return nil, err
				}
				list = addAddress(list, addr)
			}
		}
	}

	return list, nil
}

// WriteTo implements io.WriterTo. It dumps the whole message into w.
func (m *Message) WriteTo(w io.Writer) (int64, error) {
	mw := &messageWriter{w: w}
	mw.writeMessage(m)
	return mw.n, mw.err
}

func (m *Message) hasMixedPart() bool {
	return (len(m.parts) > 0 && len(m.attachments) > 0) || len(m.attachments) > 1
}

func (m *Message) hasRelatedPart() bool {
	return (len(m.parts) > 0 && len(m.embedded) > 0) || len(m.embedded) > 1
}

func (m *Message) hasAlternativePart() bool {
	return len(m.parts) > 1
}

func (w *messageWriter) writeMessage(m *Message) {
	if _, ok := m.header["Mime-Version"]; !ok {
		w.writeString("Mime-Version: 1.0\r\n")
	}
	if _, ok := m.header["Date"]; !ok {
		w.writeHeader("Date", m.FormatDate(now()))
	}
	w.writeHeaders(m.header)

	if m.hasMixedPart() {
		w.openMultipart("mixed")
	}

	if m.hasRelatedPart() {
		w.openMultipart("related")
	}

	if m.hasAlternativePart() {
		w.openMultipart("alternative")
	}
	for _, part := range m.parts {
		w.writePart(part, m.charset)
	}
	if m.hasAlternativePart() {
		w.closeMultipart()
	}

	w.addFiles(m.embedded, false)
	if m.hasRelatedPart() {
		w.closeMultipart()
	}

	w.addFiles(m.attachments, true)
	if m.hasMixedPart() {
		w.closeMultipart()
	}
}

func (w *messageWriter) openMultipart(mimeType string) {
	mw := multipart.NewWriter(w)
	contentType := "multipart/" + mimeType + ";\r\n boundary=" + mw.Boundary()
	w.writers[w.depth] = mw

	if w.depth == 0 {
		w.writeHeader("Content-Type", contentType)
		w.writeString("\r\n")
	} else {
		w.createPart(map[string][]string{
			"Content-Type": {contentType},
		})
	}
	w.depth++
}

func (w *messageWriter) createPart(h map[string][]string) {
	w.partWriter, w.err = w.writers[w.depth-1].CreatePart(h)
}

func (w *messageWriter) closeMultipart() {
	if w.depth > 0 {
		w.writers[w.depth-1].Close()
		w.depth--
	}
}

func (w *messageWriter) writePart(p *part, charset string) {
	w.writeHeaders(map[string][]string{
		"Content-Type":              {p.contentType + "; charset=" + charset},
		"Content-Transfer-Encoding": {string(p.encoding)},
	})
	w.writeBody(p.copier, p.encoding)
}

func (w *messageWriter) addFiles(files []*file, isAttachment bool) {
	for _, f := range files {
		if _, ok := f.Header["Content-Type"]; !ok {
			mediaType := mime.TypeByExtension(filepath.Ext(f.Name))
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			f.setHeader("Content-Type", mediaType+`; name="`+f.Name+`"`)
		}

		if _, ok := f.Header["Content-Transfer-Encoding"]; !ok {
			f.setHeader("Content-Transfer-Encoding", string(Base64))
		}

		if _, ok := f.Header["Content-Disposition"]; !ok {
			var disp string
			if isAttachment {
				disp = "attachment"
			} else {
				disp = "inline"
			}
			f.setHeader("Content-Disposition", disp+`; filename="`+f.Name+`"`)
		}

		if !isAttachment {
			if _, ok := f.Header["Content-ID"]; !ok {
				f.setHeader("Content-ID", "<"+f.Name+">")
			}
		}
		w.writeHeaders(f.Header)
		w.writeBody(f.CopyFunc, Base64)
	}
}

func (w *messageWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, errors.New("mailer: cannot write as writer is in error")
	}

	var n int
	n, w.err = w.w.Write(p)
	w.n += int64(n)
	return n, w.err
}

func (w *messageWriter) writeString(s string) {
	n, _ := io.WriteString(w.w, s)
	w.n += int64(n)
}

func (w *messageWriter) writeHeader(k string, v ...string) {
	w.writeString(k)
	if len(v) == 0 {
		w.writeString(":\r\n")
		return
	}
	w.writeString(": ")

	// Max header line length is 78 characters in RFC 5322 and 76 characters
	// in RFC 2047. So for the sake of simplicity we use the 76 characters
	// limit.
	charsLeft := 76 - len(k) - len(": ")

	for i, s := range v {
		// If the line is already too long, insert a newline right away.
		if charsLeft < 1 {
			if i == 0 {
				w.writeString("\r\n ")
			} else {
				w.writeString(",\r\n ")
			}
			charsLeft = 75
		} else if i != 0 {
			w.writeString(", ")
			charsLeft -= 2
		}

		// While the header content is too long, fold it by inserting a newline.
		for len(s) > charsLeft {
			s = w.writeLine(s, charsLeft)
			charsLeft = 75
		}
		w.writeString(s)
		if i := lastIndexByte(s, '\n'); i != -1 {
			charsLeft = 75 - (len(s) - i - 1)
		} else {
			charsLeft -= len(s)
		}
	}
	w.writeString("\r\n")
}

func (w *messageWriter) writeLine(s string, charsLeft int) string {
	// If there is already a newline before the limit. Write the line.
	if i := strings.IndexByte(s, '\n'); i != -1 && i < charsLeft {
		w.writeString(s[:i+1])
		return s[i+1:]
	}

	for i := charsLeft - 1; i >= 0; i-- {
		if s[i] == ' ' {
			w.writeString(s[:i])
			w.writeString("\r\n ")
			return s[i+1:]
		}
	}

	// We could not insert a newline cleanly so look for a space or a newline
	// even if it is after the limit.
	for i := 75; i < len(s); i++ {
		if s[i] == ' ' {
			w.writeString(s[:i])
			w.writeString("\r\n ")
			return s[i+1:]
		}
		if s[i] == '\n' {
			w.writeString(s[:i+1])
			return s[i+1:]
		}
	}

	// Too bad, no space or newline in the whole string. Just write everything.
	w.writeString(s)
	return ""
}

func (w *messageWriter) writeHeaders(h map[string][]string) {
	if w.depth == 0 {
		for k, v := range h {
			if k != "Bcc" {
				w.writeHeader(k, v...)
			}
		}
	} else {
		w.createPart(h)
	}
}

func (w *messageWriter) writeBody(f func(io.Writer) error, enc Encoding) {
	var subWriter io.Writer
	if w.depth == 0 {
		w.writeString("\r\n")
		subWriter = w.w
	} else {
		subWriter = w.partWriter
	}

	if enc == Base64 {
		wc := base64.NewEncoder(base64.StdEncoding, newBase64LineWriter(subWriter))
		w.err = f(wc)
		wc.Close()
	} else if enc == Unencoded {
		w.err = f(subWriter)
	} else {
		wc := newQPWriter(subWriter)
		w.err = f(wc)
		wc.Close()
	}
}
