package mailer

import "net/smtp"

func serverInfo(name string, tls bool) *smtp.ServerInfo {
	return &smtp.ServerInfo{Name: name, TLS: tls}
}
