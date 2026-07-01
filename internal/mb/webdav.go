package mb

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func resolveWebDAVURL(raw string, filename string, allowDir bool) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("WebDAV 地址不能为空")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("WebDAV 地址格式不正确")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("WebDAV 仅支持 http/https")
	}
	if u.Host == "" {
		return "", errors.New("WebDAV 地址缺少主机")
	}
	if strings.HasSuffix(u.Path, "/") {
		if !allowDir {
			return "", errors.New("请填写 WebDAV 文件地址（不能以 / 结尾）")
		}
		name := strings.TrimSpace(filename)
		if name == "" {
			name = "mb-backup-" + time.Now().In(time.Local).Format("20060102-150405") + ".json"
		}
		u.Path = strings.TrimRight(u.Path, "/") + "/" + name
	}
	return u.String(), nil
}
func doWebDAVRequest(method, targetURL, user, pass string, body []byte, contentType string, signed bool) (*http.Response, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	authHeader := ""
	if signed {
		user, pass = "", ""
	}
	resp, err := sendWebDAVRequest(client, method, targetURL, body, contentType, authHeader)
	if err != nil || signed || (user == "" && pass == "") {
		return resp, err
	}
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		challenges := resp.Header.Values("WWW-Authenticate")
		resp.Body.Close()
		if challenge, ok := parseDigestChallenge(challenges); ok {
			uri := requestURIFromURL(targetURL)
			digestHeader, err := buildDigestAuthHeader(challenge, method, uri, user, pass)
			if err == nil {
				return sendWebDAVRequest(client, method, targetURL, body, contentType, digestHeader)
			}
		}
		if hasBasicChallenge(challenges) {
			return sendWebDAVRequest(client, method, targetURL, body, contentType, basicAuthHeader(user, pass))
		}
	}
	return resp, nil
}
func sendWebDAVRequest(client *http.Client, method, targetURL string, body []byte, contentType string, authHeader string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, targetURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "MB-Backup/1.0")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return client.Do(req)
}
func basicAuthHeader(user, pass string) string {
	token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	return "Basic " + token
}
func parseDigestChallenge(headers []string) (digestChallenge, bool) {
	for _, h := range headers {
		h = strings.TrimSpace(h)
		if len(h) < 6 {
			continue
		}
		if strings.HasPrefix(strings.ToLower(h), "digest") {
			params := parseAuthParams(strings.TrimSpace(h[len("digest"):]))
			return digestChallenge{
				Realm:     params["realm"],
				Nonce:     params["nonce"],
				Qop:       params["qop"],
				Opaque:    params["opaque"],
				Algorithm: params["algorithm"],
			}, true
		}
	}
	return digestChallenge{}, false
}
func hasBasicChallenge(headers []string) bool {
	for _, h := range headers {
		h = strings.TrimSpace(h)
		if strings.HasPrefix(strings.ToLower(h), "basic") {
			return true
		}
	}
	return false
}
func parseAuthParams(raw string) map[string]string {
	out := make(map[string]string)
	s := strings.TrimSpace(raw)
	for len(s) > 0 {
		for len(s) > 0 && (s[0] == ',' || s[0] == ' ') {
			s = s[1:]
		}
		if len(s) == 0 {
			break
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = strings.TrimSpace(s[eq+1:])
		if len(s) == 0 {
			break
		}
		var val string
		if s[0] == '"' {
			s = s[1:]
			quote := strings.IndexByte(s, '"')
			if quote == -1 {
				val = s
				s = ""
			} else {
				val = s[:quote]
				s = s[quote+1:]
			}
		} else {
			comma := strings.IndexByte(s, ',')
			if comma == -1 {
				val = strings.TrimSpace(s)
				s = ""
			} else {
				val = strings.TrimSpace(s[:comma])
				s = s[comma+1:]
			}
		}
		if key != "" {
			out[key] = val
		}
	}
	return out
}
func buildDigestAuthHeader(ch digestChallenge, method, uri, user, pass string) (string, error) {
	realm := ch.Realm
	nonce := ch.Nonce
	if realm == "" || nonce == "" {
		return "", errors.New("invalid digest challenge")
	}
	algorithm := strings.ToUpper(strings.TrimSpace(ch.Algorithm))
	if algorithm == "" {
		algorithm = "MD5"
	}
	if algorithm != "MD5" && algorithm != "MD5-SESS" {
		return "", errors.New("unsupported digest algorithm")
	}

	qop := pickDigestQop(ch.Qop)
	nc := "00000001"
	cnonce := randomHex(12)
	if cnonce == "" {
		cnonce = "abcdef123456"
	}

	ha1 := md5Hex(user + ":" + realm + ":" + pass)
	if algorithm == "MD5-SESS" {
		ha1 = md5Hex(ha1 + ":" + nonce + ":" + cnonce)
	}
	ha2 := md5Hex(method + ":" + uri)

	var response string
	if qop != "" {
		response = md5Hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)
	} else {
		response = md5Hex(ha1 + ":" + nonce + ":" + ha2)
	}

	var b strings.Builder
	b.WriteString(`Digest username="`)
	b.WriteString(user)
	b.WriteString(`", realm="`)
	b.WriteString(realm)
	b.WriteString(`", nonce="`)
	b.WriteString(nonce)
	b.WriteString(`", uri="`)
	b.WriteString(uri)
	b.WriteString(`", response="`)
	b.WriteString(response)
	b.WriteString(`"`)
	if ch.Opaque != "" {
		b.WriteString(`, opaque="`)
		b.WriteString(ch.Opaque)
		b.WriteString(`"`)
	}
	if qop != "" {
		b.WriteString(`, qop=`)
		b.WriteString(qop)
		b.WriteString(`, nc=`)
		b.WriteString(nc)
		b.WriteString(`, cnonce="`)
		b.WriteString(cnonce)
		b.WriteString(`"`)
	}
	if ch.Algorithm != "" {
		b.WriteString(`, algorithm=`)
		b.WriteString(ch.Algorithm)
	}
	return b.String(), nil
}
func pickDigestQop(raw string) string {
	raw = strings.ToLower(raw)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "auth" {
			return "auth"
		}
	}
	return ""
}
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", sum)
}
func randomHex(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", b)
}
func requestURIFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	uri := u.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	if u.RawQuery != "" {
		uri += "?" + u.RawQuery
	}
	return uri
}
func isSignedWebDAVURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	q := u.Query()
	for k := range q {
		key := strings.ToLower(k)
		if strings.HasPrefix(key, "x-amz-") {
			return true
		}
		switch key {
		case "signature", "sig", "sign", "x-signature", "awsaccesskeyid", "accesskeyid", "expires":
			return true
		}
	}
	return false
}
