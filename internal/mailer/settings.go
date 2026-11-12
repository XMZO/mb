// Package mailer 通过 SMTP 发送纯文本邮件,并管理 SMTP 相关的设置项。
package mailer

import (
	"context"
	"net"
	"net/mail"
	"strconv"
	"strings"

	"mb/internal/store"
	"mb/internal/usererr"
)

// 设置项 key。
const (
	keyHost       = "email_host"
	keyPort       = "email_port"
	keyEncryption = "email_encryption"
	keyAuth       = "email_auth"
	keyUsername   = "email_user"
	keyPassword   = "email_pass"
	keyFrom       = "email_from"
	keyTo         = "email_to"

	keyLegacyProtocol = "email_protocol" // 旧版本的 smtp / smtps 选项,已并入加密方式
)

// 加密方式。
const (
	EncryptionAuto     = "auto"     // 465 端口用 SSL,其余端口在服务器支持时使用 STARTTLS
	EncryptionSTARTTLS = "starttls" // 必须使用 STARTTLS
	EncryptionSSL      = "ssl"      // 连接即 TLS(SMTPS)
	EncryptionNone     = "none"     // 明文
)

// 认证方式。
const (
	AuthPlain = "plain"
	AuthLogin = "login" // Outlook / Office365 / 163 等只支持 LOGIN
)

// Settings 是保存在设置项中的 SMTP 配置,字段都是用户填写的原始文本。
type Settings struct {
	Host       string
	Port       string
	Encryption string
	Auth       string
	Username   string
	Password   string
	From       string
	To         string
}

// LoadSettings 读取已保存的 SMTP 配置。
func LoadSettings(st *store.Store) Settings {
	s := Settings{
		Host:       st.String(keyHost, ""),
		Port:       st.String(keyPort, ""),
		Encryption: st.String(keyEncryption, EncryptionAuto),
		Auth:       st.String(keyAuth, AuthPlain),
		Username:   st.String(keyUsername, ""),
		Password:   st.String(keyPassword, ""),
		From:       st.String(keyFrom, ""),
		To:         st.String(keyTo, ""),
	}
	if s.Encryption == EncryptionAuto && st.String(keyLegacyProtocol, "") == "smtps" {
		s.Encryption = EncryptionSSL
	}
	if normalized, err := s.Normalize(); err == nil {
		return normalized
	}
	return s
}

// SaveSettings 保存 SMTP 配置(需先通过 Normalize 校验)。
func SaveSettings(ctx context.Context, st *store.Store, s Settings) error {
	return st.SetMany(ctx, map[string]string{
		keyHost:           s.Host,
		keyPort:           s.Port,
		keyEncryption:     s.Encryption,
		keyAuth:           s.Auth,
		keyUsername:       s.Username,
		keyPassword:       s.Password,
		keyFrom:           s.From,
		keyTo:             s.To,
		keyLegacyProtocol: "", // 已并入 Encryption
	})
}

// Normalize 整理并校验各字段的格式;此时允许主机、收件人等留空(用户可能只是先保存一部分)。
func (s Settings) Normalize() (Settings, error) {
	s.Host = strings.TrimSpace(s.Host)
	s.Port = strings.TrimSpace(s.Port)
	s.Username = strings.TrimSpace(s.Username)
	s.From = strings.TrimSpace(s.From)
	s.To = strings.TrimSpace(s.To)

	switch strings.ToLower(strings.TrimSpace(s.Encryption)) {
	case "", EncryptionAuto:
		s.Encryption = EncryptionAuto
	case EncryptionSTARTTLS, "tls":
		s.Encryption = EncryptionSTARTTLS
	case EncryptionSSL, "ssltls":
		s.Encryption = EncryptionSSL
	case EncryptionNone, "plain":
		s.Encryption = EncryptionNone
	default:
		return s, usererr.New("加密方式仅支持 自动 / STARTTLS / SSL / 无")
	}

	switch strings.ToLower(strings.TrimSpace(s.Auth)) {
	case "", AuthPlain:
		s.Auth = AuthPlain
	case AuthLogin:
		s.Auth = AuthLogin
	default:
		return s, usererr.New("认证方式仅支持 PLAIN / LOGIN")
	}

	if s.Port != "" {
		if _, err := parsePort(s.Port); err != nil {
			return s, err
		}
	}
	return s, nil
}

// Config 把设置转换为可直接用于发信的配置,并检查必填项是否齐全。
func (s Settings) Config() (Config, error) {
	s, err := s.Normalize()
	if err != nil {
		return Config{}, err
	}

	host, portText := s.Host, s.Port
	if portText == "" {
		// 允许把端口直接写在服务器地址里:smtp.example.com:465
		if h, p, err := net.SplitHostPort(host); err == nil {
			host, portText = h, p
		}
	}
	if host == "" {
		return Config{}, usererr.New("请填写邮件服务器地址")
	}
	port := defaultPort(s.Encryption)
	if portText != "" {
		if port, err = parsePort(portText); err != nil {
			return Config{}, err
		}
	}

	fromText := s.From
	if fromText == "" {
		fromText = s.Username
	}
	if fromText == "" {
		return Config{}, usererr.New("请填写发件邮箱")
	}
	from, err := mail.ParseAddress(fromText)
	if err != nil {
		return Config{}, usererr.New("发件邮箱格式不正确")
	}

	if s.To == "" {
		return Config{}, usererr.New("请填写接收邮箱")
	}
	to, err := mail.ParseAddressList(strings.NewReplacer(";", ",", "\n", ",", "，", ",").Replace(s.To))
	if err != nil || len(to) == 0 {
		return Config{}, usererr.New("接收邮箱格式不正确")
	}

	cfg := Config{
		Host:       host,
		Port:       port,
		Encryption: s.Encryption,
		Auth:       s.Auth,
		Username:   s.Username,
		Password:   s.Password,
		From:       *from,
	}
	for _, addr := range to {
		cfg.To = append(cfg.To, *addr)
	}
	return cfg, nil
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return 0, usererr.New("端口应为 1-65535 之间的数字")
	}
	return port, nil
}

func defaultPort(encryption string) int {
	switch encryption {
	case EncryptionSSL:
		return 465
	case EncryptionNone:
		return 25
	}
	return 587
}
