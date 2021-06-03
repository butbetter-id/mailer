package mailer

// Config represents all configurable mailer data smtp credentials
type (
	ConfigMailer struct {
		Identity    string
		Username    string
		Password    string
		Host        string
		Port        int
		SenderEmail string
		SenderName  string
	}
)

// Config represents all configurable mailer data smtp credentials
var Config *ConfigMailer

func New(host string, port int, username string, password string, senderEmail string, senderName string) {
	Config = &ConfigMailer{
		Username:    username,
		Password:    password,
		Host:        host,
		Port:        port,
		SenderEmail: senderEmail,
		SenderName:  senderName,
	}
}
