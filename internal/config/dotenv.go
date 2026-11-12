package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv 读取 KEY=VALUE 形式的 .env 文件并注入进程环境;文件不存在时静默跳过。
// 已存在的真实环境变量优先,不会被覆盖(与容器 / systemd 的注入共存)。
func LoadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

// parseEnvLine 解析一行 .env;支持 # 注释、可选的 export 前缀与成对引号。
func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	key, value, found := strings.Cut(line, "=")
	key = strings.TrimSpace(key)
	if !found || key == "" {
		return "", "", false
	}
	return key, unquote(strings.TrimSpace(value)), true
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	// 未加引号的值允许行尾注释(与 docker compose 的 env_file 行为一致):KEY=value # comment
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}
