package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// sendTimeout 是一次发信(连接、握手、认证、投递)的总时限。
const sendTimeout = 30 * time.Second

// Config 是一次发信所需的完整配置。
type Config struct {
	Host       string
	Port       int
	Encryption string
	Auth       string
	Username   string // 为空时不做认证
	Password   string
	From       mail.Address
	To         []mail.Address
}

// Message 是一封纯文本邮件。
type Message struct {
	Subject string
	Body    string
}

// Send 通过 SMTP 发送一封邮件。
func Send(ctx context.Context, cfg Config, msg Message) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	client, err := dial(ctx, cfg)
	if err != nil {
		return fmt.Errorf("连接邮件服务器失败: %w", err)
	}
	defer client.Close()

	if cfg.Username != "" {
		if err := client.Auth(newAuth(cfg)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := client.Mail(cfg.From.Address); err != nil {
		return fmt.Errorf("发件人被拒绝: %w", err)
	}
	for _, addr := range cfg.To {
		if err := client.Rcpt(addr.Address); err != nil {
			return fmt.Errorf("收件人 %s 被拒绝: %w", addr.Address, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("发送邮件内容失败: %w", err)
	}
	if _, err := w.Write(buildMessage(cfg, msg, time.Now())); err != nil {
		w.Close()
		return fmt.Errorf("发送邮件内容失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("邮件服务器拒收: %w", err)
	}
	_ = client.Quit() // 邮件此时已被服务器接收,QUIT 失败无关紧要
	return nil
}

// dial 建立 SMTP 连接并按配置完成 TLS 协商。
func dial(ctx context.Context, cfg Config) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	tlsConfig := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	implicitTLS := cfg.Encryption == EncryptionSSL || (cfg.Encryption == EncryptionAuto && cfg.Port == 465)
	if implicitTLS {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if implicitTLS || cfg.Encryption == EncryptionNone {
		return client, nil
	}

	supported, _ := client.Extension("STARTTLS")
	if !supported {
		if cfg.Encryption == EncryptionSTARTTLS {
			client.Close()
			return nil, errors.New("服务器不支持 STARTTLS")
		}
		return client, nil // 自动模式:退回明文,是否允许认证由 checkAuthSecurity 把关
	}
	if err := client.StartTLS(tlsConfig); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

// buildMessage 生成符合 RFC 5322 的邮件原文:主题按 RFC 2047 编码,正文用 base64 传输。
func buildMessage(cfg Config, msg Message, now time.Time) []byte {
	var b bytes.Buffer
	header := func(name, value string) {
		b.WriteString(name + ": " + value + "\r\n")
	}

	to := make([]string, len(cfg.To))
	for i := range cfg.To {
		to[i] = cfg.To[i].String()
	}
	header("From", cfg.From.String())
	header("To", strings.Join(to, ", "))
	header("Subject", mime.BEncoding.Encode("UTF-8", singleLine(msg.Subject)))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", messageID(cfg.From.Address))
	header("MIME-Version", "1.0")
	header("Content-Type", `text/plain; charset="UTF-8"`)
	header("Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")

	encoded := base64.StdEncoding.EncodeToString([]byte(msg.Body))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	return b.Bytes()
}

func messageID(from string) string {
	random := make([]byte, 12)
	_, _ = rand.Read(random)
	domain := "localhost"
	if at := strings.LastIndexByte(from, '@'); at >= 0 && at < len(from)-1 {
		domain = from[at+1:]
	}
	return "<" + hex.EncodeToString(random) + "@" + domain + ">"
}

// singleLine 去掉换行,防止用户内容注入额外的邮件头。
func singleLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
}
