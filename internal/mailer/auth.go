package mailer

import (
	"errors"
	"net"
	"net/smtp"
	"strings"
)

// 标准库的 smtp.PlainAuth 会校验主机名且不支持 LOGIN 机制,
// 而国内外不少邮件服务商只支持 LOGIN,所以这里自行实现两种认证。

func newAuth(cfg Config) smtp.Auth {
	base := authBase{
		username:      cfg.Username,
		password:      cfg.Password,
		allowInsecure: cfg.Encryption == EncryptionNone,
	}
	if cfg.Auth == AuthLogin {
		return &loginAuth{base}
	}
	return &plainAuth{base}
}

type authBase struct {
	username      string
	password      string
	allowInsecure bool // 用户明确选择了"无加密"
}

// checkSecurity 拒绝在未加密的连接上发送密码,除非是本机服务器或用户明确选择了明文。
func (a authBase) checkSecurity(server *smtp.ServerInfo) error {
	if a.allowInsecure || server.TLS || isLoopback(server.Name) {
		return nil
	}
	return errors.New("连接未加密，已拒绝发送密码；如确需明文认证，请将加密方式设为“无”")
}

func isLoopback(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type plainAuth struct{ authBase }

func (a *plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if err := a.checkSecurity(server); err != nil {
		return "", nil, err
	}
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

type loginAuth struct{ authBase }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if err := a.checkSecurity(server); err != nil {
		return "", nil, err
	}
	return "LOGIN", nil, nil
}

// Next 依次回答服务器的 "Username:" 与 "Password:" 提示。
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if strings.Contains(strings.ToLower(string(fromServer)), "user") {
		return []byte(a.username), nil
	}
	return []byte(a.password), nil
}
