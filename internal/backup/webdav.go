package backup

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"mb/internal/store"
	"mb/internal/usererr"
)

// 设置项 key。
const (
	keyWebDAVURL      = "webdav_url"
	keyWebDAVFilename = "webdav_filename"
	keyWebDAVUsername = "webdav_user"
	keyWebDAVPassword = "webdav_pass"
)

// webdavTimeout 是一次上传或下载的总时限。
const webdavTimeout = 40 * time.Second

// WebDAV 是远程备份位置的配置。
//
// URL 可以是目录(以 / 结尾,或最后一段不含扩展名),此时文件名取 Filename;
// 也可以直接是完整的文件地址,包括对象存储的预签名地址。
type WebDAV struct {
	URL      string
	Filename string
	Username string
	Password string
}

// LoadWebDAV 读取已保存的 WebDAV 配置。
func LoadWebDAV(st *store.Store) WebDAV {
	return WebDAV{
		URL:      st.String(keyWebDAVURL, ""),
		Filename: st.String(keyWebDAVFilename, ""),
		Username: st.String(keyWebDAVUsername, ""),
		Password: st.String(keyWebDAVPassword, ""),
	}
}

// SaveWebDAV 保存 WebDAV 配置。
func SaveWebDAV(ctx context.Context, st *store.Store, w WebDAV) error {
	return st.SetMany(ctx, map[string]string{
		keyWebDAVURL:      strings.TrimSpace(w.URL),
		keyWebDAVFilename: strings.TrimSpace(w.Filename),
		keyWebDAVUsername: strings.TrimSpace(w.Username),
		keyWebDAVPassword: w.Password,
	})
}

// Upload 把备份内容上传到 WebDAV。目标是目录且未指定文件名时,自动生成带时间戳的文件名。
func (w WebDAV) Upload(ctx context.Context, data []byte) error {
	target, err := w.target(Filename(time.Now()))
	if err != nil {
		return err
	}
	resp, err := w.do(ctx, http.MethodPut, target, data)
	if err != nil {
		return usererr.Newf("WebDAV 上传失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return usererr.Newf("WebDAV 上传失败：%s", resp.Status)
	}
	return nil
}

// Download 从 WebDAV 下载备份内容。
func (w WebDAV) Download(ctx context.Context) ([]byte, error) {
	target, err := w.target("")
	if err != nil {
		return nil, err
	}
	resp, err := w.do(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, usererr.Newf("WebDAV 下载失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, usererr.Newf("WebDAV 下载失败：%s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, usererr.Newf("WebDAV 下载失败：%v", err)
	}
	if int64(len(data)) > MaxBytes {
		return nil, usererr.New("WebDAV 上的备份文件过大")
	}
	return data, nil
}

// target 计算实际请求的文件地址。defaultName 为空表示必须明确指定文件(下载时)。
func (w WebDAV) target(defaultName string) (*url.URL, error) {
	raw := strings.TrimSpace(w.URL)
	if raw == "" {
		return nil, usererr.New("请填写 WebDAV 地址")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, usererr.New("WebDAV 地址应为 http:// 或 https:// 开头的完整地址")
	}

	filename := strings.TrimSpace(w.Filename)
	if strings.ContainsAny(filename, `/\`) {
		return nil, usererr.New("文件名不能包含路径分隔符")
	}

	if isSigned(u) {
		// 预签名地址对路径签了名,不能再拼接文件名。
		if filename != "" || strings.HasSuffix(u.Path, "/") {
			return nil, usererr.New("检测到签名地址：请填写完整的文件地址，并清空文件名")
		}
		return u, nil
	}

	endsWithSlash := u.Path == "" || strings.HasSuffix(u.Path, "/")
	switch {
	case filename != "" && (endsWithSlash || path.Ext(path.Base(u.Path)) == ""):
		// 地址是目录(以 / 结尾,或最后一段没有扩展名):拼上文件名。
		return u.JoinPath(filename), nil
	case !endsWithSlash:
		return u, nil // 地址本身就是完整的文件地址
	case defaultName != "":
		return u.JoinPath(defaultName), nil
	}
	return nil, usererr.New("WebDAV 地址是目录，恢复时请填写文件名")
}

// isSigned 判断是否为对象存储的预签名地址;这类地址自带鉴权,不需要也不应附加账号密码。
func isSigned(u *url.URL) bool {
	for key := range u.Query() {
		key = strings.ToLower(key)
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

// do 发送请求。配置了账号时先用 Basic 认证;服务器要求 Digest 时改用 Digest 重试一次。
func (w WebDAV) do(ctx context.Context, method string, target *url.URL, body []byte) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, webdavTimeout)
	client := &http.Client{}

	username := strings.TrimSpace(w.Username)
	useAuth := (username != "" || w.Password != "") && !isSigned(target)

	send := func(authorization string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "mb-asset-board")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		} else if useAuth {
			req.SetBasicAuth(username, w.Password)
		}
		return client.Do(req)
	}

	resp, err := send("")
	if err == nil && useAuth && resp.StatusCode == http.StatusUnauthorized {
		if challenge, ok := digestChallenge(resp.Header.Values("WWW-Authenticate")); ok {
			resp.Body.Close()
			resp, err = send(challenge.authorization(method, target.RequestURI(), username, w.Password))
		}
	}
	if err != nil {
		cancel()
		return nil, err
	}
	// 响应体读完之前不能取消 context,把 cancel 绑到 Body.Close 上。
	resp.Body = cancelOnClose{resp.Body, cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}

// digest 是服务器在 WWW-Authenticate 中给出的 Digest 认证参数(RFC 7616,仅实现 MD5)。
type digest struct {
	realm, nonce, opaque, algorithm string
	qopAuth                         bool
}

func digestChallenge(headers []string) (digest, bool) {
	for _, h := range headers {
		scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
		if !strings.EqualFold(scheme, "Digest") {
			continue
		}
		params := parseAuthParams(rest)
		d := digest{
			realm:     params["realm"],
			nonce:     params["nonce"],
			opaque:    params["opaque"],
			algorithm: params["algorithm"],
		}
		for _, qop := range strings.Split(params["qop"], ",") {
			if strings.EqualFold(strings.TrimSpace(qop), "auth") {
				d.qopAuth = true
			}
		}
		algorithm := strings.ToUpper(d.algorithm)
		supported := algorithm == "" || algorithm == "MD5" || algorithm == "MD5-SESS"
		if d.realm != "" && d.nonce != "" && supported {
			return d, true
		}
	}
	return digest{}, false
}

// parseAuthParams 解析 key=value, key="quoted value" 形式的参数列表。
func parseAuthParams(s string) map[string]string {
	params := make(map[string]string)
	for {
		s = strings.TrimLeft(s, ", \t")
		key, rest, found := strings.Cut(s, "=")
		if !found {
			return params
		}
		var value string
		if strings.HasPrefix(rest, `"`) {
			value, s, _ = strings.Cut(rest[1:], `"`)
		} else {
			value, s, _ = strings.Cut(rest, ",")
		}
		params[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
}

func (d digest) authorization(method, uri, username, password string) string {
	cnonceBytes := make([]byte, 8)
	_, _ = rand.Read(cnonceBytes)
	cnonce := hex.EncodeToString(cnonceBytes)
	const nc = "00000001"

	ha1 := md5Hex(username + ":" + d.realm + ":" + password)
	if strings.EqualFold(d.algorithm, "MD5-SESS") {
		ha1 = md5Hex(ha1 + ":" + d.nonce + ":" + cnonce)
	}
	ha2 := md5Hex(method + ":" + uri)

	var response string
	if d.qopAuth {
		response = md5Hex(strings.Join([]string{ha1, d.nonce, nc, cnonce, "auth", ha2}, ":"))
	} else {
		response = md5Hex(ha1 + ":" + d.nonce + ":" + ha2)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `Digest username=%q, realm=%q, nonce=%q, uri=%q, response=%q`,
		username, d.realm, d.nonce, uri, response)
	if d.opaque != "" {
		fmt.Fprintf(&b, `, opaque=%q`, d.opaque)
	}
	if d.qopAuth {
		fmt.Fprintf(&b, `, qop=auth, nc=%s, cnonce=%q`, nc, cnonce)
	}
	if d.algorithm != "" {
		fmt.Fprintf(&b, `, algorithm=%s`, d.algorithm)
	}
	return b.String()
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
