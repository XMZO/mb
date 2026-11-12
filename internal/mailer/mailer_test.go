package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"mime"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildMessage(t *testing.T) {
	cfg := Config{
		From: mail.Address{Name: "面板", Address: "noreply@example.com"},
		To:   []mail.Address{{Address: "a@example.com"}, {Address: "b@example.com"}},
	}
	body := "以下资产即将到期：\n- example.com ｜ 2026-12-01\n" + strings.Repeat("长内容", 40)
	raw := buildMessage(cfg, Message{Subject: "到期提醒：3 项资产\r\nBcc: evil@example.com", Body: body}, time.Now())

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("message is not valid RFC 5322: %v", err)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Error("subject must not be able to inject headers")
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || !strings.HasPrefix(subject, "到期提醒：3 项资产") {
		t.Errorf("subject = %q, %v", subject, err)
	}
	if to, err := msg.Header.AddressList("To"); err != nil || len(to) != 2 {
		t.Errorf("To = %v, %v", to, err)
	}
	if msg.Header.Get("Message-ID") == "" || msg.Header.Get("Date") == "" {
		t.Error("Message-ID and Date headers are required")
	}

	var encoded strings.Builder
	scanner := bufio.NewScanner(msg.Body)
	for scanner.Scan() {
		if len(scanner.Text()) > 76 {
			t.Fatalf("body line longer than 76 characters: %d", len(scanner.Text()))
		}
		encoded.WriteString(scanner.Text())
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil || string(decoded) != body {
		t.Errorf("body did not survive encoding: %v", err)
	}
}

func TestSettingsConfig(t *testing.T) {
	valid := Settings{Host: "smtp.example.com", Username: "me@example.com", To: "a@example.com; b@example.com"}

	cfg, err := valid.Config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 587 || cfg.Encryption != EncryptionAuto || cfg.Auth != AuthPlain ||
		cfg.From.Address != "me@example.com" || len(cfg.To) != 2 {
		t.Errorf("unexpected config: %+v", cfg)
	}

	withPort := valid
	withPort.Host, withPort.Encryption = "smtp.example.com:2525", "SSL"
	if cfg, err := withPort.Config(); err != nil || cfg.Host != "smtp.example.com" || cfg.Port != 2525 || cfg.Encryption != EncryptionSSL {
		t.Errorf("host:port not split: %+v, %v", cfg, err)
	}
	ssl := valid
	ssl.Encryption = EncryptionSSL
	if cfg, _ := ssl.Config(); cfg.Port != 465 {
		t.Errorf("default SSL port = %d, want 465", cfg.Port)
	}

	invalid := map[string]Settings{
		"missing host":   {Username: "me@example.com", To: "a@example.com"},
		"missing sender": {Host: "h", To: "a@example.com"},
		"missing to":     {Host: "h", Username: "me@example.com"},
		"bad to":         {Host: "h", Username: "me@example.com", To: "not an address"},
		"bad port":       {Host: "h", Port: "99999", Username: "me@example.com", To: "a@example.com"},
		"bad encryption": {Host: "h", Encryption: "rot13", Username: "me@example.com", To: "a@example.com"},
		"bad auth":       {Host: "h", Auth: "ntlm", Username: "me@example.com", To: "a@example.com"},
	}
	for name, s := range invalid {
		if _, err := s.Config(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// fakeSMTP 是只够完成一次投递的 SMTP 服务器,返回收到的全部命令与邮件内容。
func fakeSMTP(t *testing.T) (addr string, received <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	out := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var log strings.Builder
		reader := bufio.NewReader(conn)
		reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }

		reply("220 fake ESMTP")
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			log.WriteString(line)
			line = strings.TrimRight(line, "\r\n")
			switch {
			case inData:
				if line == "." {
					inData = false
					reply("250 queued")
				}
			case strings.HasPrefix(line, "EHLO"):
				reply("250-fake")
				reply("250 AUTH PLAIN LOGIN")
			case strings.HasPrefix(line, "AUTH PLAIN"):
				reply("235 ok")
			case line == "DATA":
				inData = true
				reply("354 go ahead")
			case line == "QUIT":
				reply("221 bye")
				out <- log.String()
				return
			default:
				reply("250 ok")
			}
		}
		out <- log.String()
	}()
	return listener.Addr().String(), out
}

func TestSendDeliversMessage(t *testing.T) {
	addr, received := fakeSMTP(t)
	host, portText, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portText)

	cfg := Config{
		Host: host, Port: port, Encryption: EncryptionAuto, Auth: AuthPlain,
		Username: "user", Password: "secret", // 本机回环地址允许在未加密的连接上认证
		From: mail.Address{Address: "from@example.com"},
		To:   []mail.Address{{Address: "to@example.com"}},
	}
	if err := Send(context.Background(), cfg, Message{Subject: "测试", Body: "正文"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case log := <-received:
		for _, want := range []string{"AUTH PLAIN", "MAIL FROM:<from@example.com>", "RCPT TO:<to@example.com>", "Subject: =?UTF-8?b?"} {
			if !strings.Contains(log, want) {
				t.Errorf("server did not receive %q\n%s", want, log)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake server did not finish")
	}
}

func TestAuthRefusesPlaintextToRemoteHost(t *testing.T) {
	remote := authBase{username: "u", password: "p"}
	if err := (&plainAuth{remote}).checkSecurity(serverInfo("smtp.example.com", false)); err == nil {
		t.Error("password must not be sent to a remote host without TLS")
	}
	if err := (&plainAuth{remote}).checkSecurity(serverInfo("smtp.example.com", true)); err != nil {
		t.Errorf("TLS connection should be accepted: %v", err)
	}
	remote.allowInsecure = true
	if err := (&loginAuth{remote}).checkSecurity(serverInfo("smtp.example.com", false)); err != nil {
		t.Errorf("explicit 'no encryption' should be honoured: %v", err)
	}
}
