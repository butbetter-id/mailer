package mailer

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// A Dialer is a dialer to an SMTP server.
type (
	Dialer struct {
		// Host represents the host of the SMTP server.
		Host string
		// Port represents the port of the SMTP server.
		Port int
		// Username is the username to use to authenticate to the SMTP server.
		Username string
		// Password is the password to use to authenticate to the SMTP server.
		Password string
		// Auth represents the authentication mechanism used to authenticate to the
		// SMTP server.
		Auth smtp.Auth
		// SSL defines whether an SSL connection is used. It should be false in
		// most cases since the authentication mechanism should use the STARTTLS
		// extension instead.
		SSL bool
		// TSLConfig represents the TLS configuration used for the TLS (when the
		// STARTTLS extension is used) or SSL connection.
		TLSConfig *tls.Config
		// LocalName is the hostname sent to the SMTP server with the HELO command.
		// By default, "localhost" is sent.
		LocalName string
	}

	smtpSender struct {
		smtpClient
		d *Dialer
	}

	smtpClient interface {
		Hello(string) error
		Extension(string) (bool, string)
		StartTLS(*tls.Config) error
		Auth(smtp.Auth) error
		Mail(string) error
		Rcpt(string) error
		Data() (io.WriteCloser, error)
		Quit() error
		Close() error
	}

	loginAuth struct {
		username string
		password string
		host     string
	}
)

var (
	netDialTimeout = net.DialTimeout
	tlsClient      = tls.Client
	smtpNewClient  = func(conn net.Conn, host string) (smtpClient, error) {
		return smtp.NewClient(conn, host)
	}
)

// NewDialer returns a new SMTP Dialer.
// The given parameters are used to connect to the SMTP server.
func NewDialer() *Dialer {
	if Config == nil {
		log.Fatal("please define smtp config")

		return nil
	}

	d := &Dialer{
		Host:     Config.Host,
		Username: Config.Username,
		Password: Config.Password,
		Port:     Config.Port,
		SSL:      Config.Port == 465,
	}

	return d
}

// Dial dials and authenticates to an SMTP server. The returned SendCloser
// should be closed when done using it.
func (d *Dialer) Dial() (SendCloser, error) {
	conn, err := netDialTimeout("tcp", addr(d.Host, d.Port), 10*time.Second)
	if err != nil {
		return nil, err
	}

	if d.SSL {
		conn = tlsClient(conn, d.tlsConfig())
	}

	c, err := smtpNewClient(conn, d.Host)
	if err != nil {
		return nil, err
	}

	if d.LocalName != "" {
		if err := c.Hello(d.LocalName); err != nil {
			return nil, err
		}
	}

	if !d.SSL {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(d.tlsConfig()); err != nil {
				c.Close()
				return nil, err
			}
		}
	}

	if d.Auth == nil && d.Username != "" {
		if ok, auths := c.Extension("AUTH"); ok {
			if strings.Contains(auths, "CRAM-MD5") {
				d.Auth = smtp.CRAMMD5Auth(d.Username, d.Password)
			} else if strings.Contains(auths, "LOGIN") &&
				!strings.Contains(auths, "PLAIN") {
				d.Auth = &loginAuth{
					username: d.Username,
					password: d.Password,
					host:     d.Host,
				}
			} else {
				d.Auth = smtp.PlainAuth("", d.Username, d.Password, d.Host)
			}
		}
	}

	if d.Auth != nil {
		if err = c.Auth(d.Auth); err != nil {
			c.Close()
			return nil, err
		}
	}

	return &smtpSender{c, d}, nil
}

func (d *Dialer) tlsConfig() *tls.Config {
	if d.TLSConfig == nil {
		return &tls.Config{ServerName: d.Host}
	}
	return d.TLSConfig
}

// DialAndSend opens a connection to the SMTP server, sends the given emails and
// closes the connection.
func (d *Dialer) DialAndSend(m ...*Message) error {
	s, err := d.Dial()
	if err != nil {
		return err
	}
	defer s.Close()

	return Send(s, m...)
}

func (c *smtpSender) Send(from string, to []string, msg io.WriterTo) error {
	if err := c.Mail(from); err != nil {
		if err == io.EOF {
			// This is probably due to a timeout, so reconnect and try again.
			sc, derr := c.d.Dial()
			if derr == nil {
				if sx, ok := sc.(*smtpSender); ok {
					*c = *sx
					return c.Send(from, to, msg)
				}
			}
		}
		return err
	}

	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return err
		}
	}

	w, err := c.Data()
	if err != nil {
		return err
	}

	if _, err = msg.WriteTo(w); err != nil {
		w.Close()
		return err
	}

	return w.Close()
}

func (c *smtpSender) Close() error {
	return c.Quit()
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		advertised := false
		for _, mechanism := range server.Auth {
			if mechanism == "LOGIN" {
				advertised = true
				break
			}
		}
		if !advertised {
			return "", nil, errors.New("unencrypted connection")
		}
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}

	switch {
	case bytes.Equal(fromServer, []byte("Username:")):
		return []byte(a.username), nil
	case bytes.Equal(fromServer, []byte("Password:")):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected server challenge: %s", fromServer)
	}
}
