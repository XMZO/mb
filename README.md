# MB 米表 / MB Asset Board

一个轻量、自托管的域名与 VPS 资产面板。它可以管理域名、VPS、续费价格、到期时间、公开展示字段、汇率换算、邮件提醒和 WebDAV 备份。

A lightweight self-hosted asset board for domains and VPS instances. It tracks renewal dates, prices, visibility, exchange rates, email reminders, and WebDAV backups.

---

## 功能 / Features

### 中文

- 域名资产管理:域名、注册商、到期日、续费价格、备注、置顶、隐藏、成本排除。
- VPS 资产管理:厂商、IP、地区、CPU/内存/磁盘、流量、带宽、账期、到期日、价格、备注。
- 公开/管理双模式:可配置公开页面展示哪些字段。
- 管理员登录:BCrypt 密码哈希、HMAC 签名 Cookie、登录限流。
- 汇率换算:支持主流与部分冷门法币（含 TRY/TND 等）换算,可从 ECB / open.er-api.com 更新汇率，也可手动补充。
- 到期提醒:按配置天数通过邮件提醒即将到期的域名和 VPS，SMTP 支持 PLAIN / LOGIN 认证以兼容 O365、Outlook、163 等服务商。
- 备份恢复:JSON 备份导出/导入,支持 WebDAV 推送/拉取。
- 模块化结构:业务功能拆分到 `internal/mb` 下的多个小文件,方便增删功能。
- 前端可替换:HTML 模板已外置到 `internal/mb/web/templates`,不再硬编码在 Go 字符串中。

### English

- Domain management: registrar, expiry date, renewal price, notes, pin/hide, and cost exclusion.
- VPS management: vendor, IP, region, CPU/memory/disk, traffic, bandwidth, billing cycle, expiry date, price, and notes.
- Public/admin modes: choose which fields are visible on the public page.
- Admin login: BCrypt password hashing, HMAC-signed cookies, and login rate limiting.
- FX conversion: supports common and less-common fiat currencies including TRY/TND, with optional ECB / open.er-api.com updates and manual overrides.
- Expiry reminders: send email reminders for upcoming domain/VPS expirations, with PLAIN / LOGIN SMTP auth for providers such as O365, Outlook, and 163.
- Backup and restore: JSON export/import plus WebDAV push/pull.
- Modular code layout: feature files live under `internal/mb`, making features easier to add or remove.
- Replaceable frontend: templates live under `internal/mb/web/templates` instead of being hard-coded Go strings.

---

## 技术栈 / Tech Stack

- Go 1.24+
- SQLite via [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), no CGO required
- Standard-library HTTP server and `html/template`
- `golang.org/x/crypto/bcrypt` for password hashing

---

## 快速开始 / Quick Start

### 1. 准备配置 / Prepare config

```bash
cp .env.example .env
```

按需编辑 `.env`。如果 `MB_ADMIN_PASSWORD` 留空,首次启动会生成随机管理员密码并**只在日志中显示一次**。

Edit `.env` as needed. If `MB_ADMIN_PASSWORD` is empty, the app generates a random admin password on first startup and prints it to the log **once**.

### 2. 运行 / Run

```bash
go run .
```

默认监听:

```text
http://127.0.0.1:15000
```

Default listen address:

```text
http://127.0.0.1:15000
```

直接运行二进制或 systemd 部署时,可通过 `.env` 或真实环境变量修改监听地址:

```env
MB_ADDR=:15000
```

### 3. 构建 / Build

```bash
go build -o mb .
```

Windows:

```powershell
go build -o mb.exe .
```

### 4. Docker Compose 部署 / Deploy with Docker Compose

tag workflow 会把镜像推到 GHCR。推一个 `v*` tag 即可触发测试、原生 Linux amd64/arm64 镜像构建、Windows 容器镜像构建和 manifest 发布:

```bash
git tag v1.0.0
git push origin v1.0.0
```

镜像名默认是:

```text
ghcr.io/<owner>/<repo>:<tag>
ghcr.io/<owner>/<repo>:latest
```

VPS 上部署:

```bash
cp .env.example .env
# 编辑 .env: 把 MB_IMAGE 改成 ghcr.io/<owner>/<repo>:latest 或指定 tag
docker compose pull
docker compose up -d
```

Docker 部署的端口只在 `docker-compose.yml` 的 `ports` 里改。默认:

```yaml
ports:
  - "15000:15000"
```

要换宿主机端口,改左边即可,例如 `"8080:15000"`。`.env` 里的 `MB_ADDR` 是给直接运行二进制 / systemd 用的,Docker Compose 默认不需要设置它。

`docker-compose.yml` 默认使用 Docker named volume `mb-data` 持久化 `/data/mb.db`。如果想把数据库放在仓库目录旁边,可在 `.env` 里改成:

```env
MB_DATA_VOLUME=./data
```

绑定宿主机目录时,容器内默认 UID/GID 是 `10001:10001`;如果遇到数据库无法写入:

```bash
mkdir -p data
sudo chown -R 10001:10001 data
```

The compose file uses the published GHCR image and persists SQLite at `/data/mb.db`. By default it uses a named Docker volume; set `MB_DATA_VOLUME=./data` if you prefer a host bind mount.

Windows 容器模式可使用同一个 GHCR tag,但要用 Windows compose 文件,因为数据库路径和 healthcheck 与 Linux 容器不同:

```powershell
Copy-Item .env.example .env
# 编辑 .env: 把 MB_IMAGE 改成 ghcr.io/<owner>/<repo>:latest 或指定 tag
docker compose -f docker-compose.windows.yml pull
docker compose -f docker-compose.windows.yml up -d
```

CI 会发布 Linux `amd64` / `arm64` 镜像,并尝试发布 Windows `amd64` / `arm64` 容器镜像。Windows `arm64` runner 仍是 GitHub public preview,所以该平台在 workflow 中设为 optional;如果 GitHub 当次没有可用原生 runner 或 Windows ARM 容器基底不可用,不会阻断 Linux/VPS 镜像发布。Windows `amd64` 不是 optional。

---

## 配置 / Configuration

配置通过环境变量读取。应用启动时会自动读取当前目录下的 `.env`;真实环境变量优先于 `.env`,不会被覆盖。

Configuration is read from environment variables. The app automatically loads `.env` from the working directory. Real environment variables take precedence over `.env` values.

| Variable | Default | Description |
| --- | --- | --- |
| `MB_ADDR` | `:15000` | HTTP listen address for direct binary/systemd runs. Docker Compose normally leaves this unset and uses `ports` instead. |
| `MB_DB` | `./mb.db` | SQLite database path |
| `MB_COOKIE_NAME` | `mb_admin` | Admin session cookie name |
| `MB_ADMIN_PASSWORD` | empty | Admin password. If set, it is authoritative and takes effect after restart. If empty, a random password is generated on first initialization. |
| `MB_LOGIN_MAX_FAILURES` | `5` | Max failed login attempts in the window. `0` disables rate limiting. |
| `MB_LOGIN_WINDOW` | `15m` | Failed-login counting window |
| `MB_LOGIN_LOCKOUT` | `15m` | Lockout duration after too many failures |
| `MB_LOGIN_MAX_ENTRIES` | `4096` | Max in-memory login limiter entries |
| `MB_TRUST_PROXY_HEADERS` | `false` | Whether to trust `X-Forwarded-For` / `X-Real-IP` |
| `MB_TRUSTED_PROXY_CIDRS` | empty | Optional trusted proxy IP/CIDR allowlist |

> 如果部署在 Nginx / Caddy / Cloudflare 等可信反向代理后面,才建议开启 `MB_TRUST_PROXY_HEADERS=true`。否则攻击者可能伪造转发头绕过登录限流。
>
> Enable `MB_TRUST_PROXY_HEADERS=true` only when the app is behind a trusted reverse proxy. Otherwise attackers may spoof forwarded headers to bypass login rate limiting.

---

## 安全说明 / Security Notes

### 中文

- 密码使用 BCrypt 存储,不保存明文。
- 登录会话使用 HMAC-SHA256 签名 Cookie,并设置 `HttpOnly` / `SameSite=Lax` / HTTPS 下 `Secure`。
- 登录接口默认启用限流,防止持续爆破。
- 所有非安全方法请求会校验 CSRF token;表单使用隐藏字段,fetch/API 使用 `X-CSRF-Token`。
- CSP 已移除 `unsafe-inline`;页面脚本/样式通过每请求 nonce 授权。
- 内部错误不会直接返回原始错误信息,完整错误仅写入服务端日志。
- `.env`、数据库和构建产物已通过 `.gitignore` 忽略;不要提交真实 `.env` 或 `mb.db`。

### English

- Passwords are stored with BCrypt; plaintext passwords are not stored.
- Sessions use HMAC-SHA256-signed cookies with `HttpOnly`, `SameSite=Lax`, and `Secure` under HTTPS.
- Login rate limiting is enabled by default.
- Unsafe HTTP methods are protected by CSRF tokens; forms use hidden fields and fetch/API calls use `X-CSRF-Token`.
- CSP no longer uses `unsafe-inline`; inline scripts/styles are authorized with per-request nonces.
- Internal errors are redacted from HTTP responses; details are logged server-side.
- `.env`, database files, and build artifacts are ignored by `.gitignore`; do not commit real `.env` or `mb.db`.

---

## 目录结构 / Project Layout

```text
.
├── main.go                         # thin entrypoint / 极薄启动入口
├── go.mod
├── go.sum
├── Dockerfile                      # Linux container image
├── Dockerfile.windows              # Windows container image
├── docker-compose.yml              # Linux/VPS compose
├── docker-compose.windows.yml      # Windows container compose
├── .env.example                    # config template / 配置模板
├── internal/mb/
│   ├── run.go                      # app bootstrap / 应用启动流程
│   ├── routes.go                   # feature route registration / 功能模块路由注册
│   ├── types.go                    # shared types and constants / 共享类型与常量
│   ├── config.go                   # env and .env loading / 配置加载
│   ├── db.go                       # SQLite, migrations, settings / 数据库与迁移
│   ├── auth.go                     # auth, sessions, login limiter / 鉴权与登录限流
│   ├── dashboard.go                # dashboard / 首页仪表盘
│   ├── domain.go                   # domain feature / 域名功能
│   ├── vps.go                      # VPS feature / VPS 功能
│   ├── settings.go                 # settings page / 设置页
│   ├── sort.go                     # sorting endpoints / 排序
│   ├── fx.go                       # exchange rates / 汇率
│   ├── email.go                    # SMTP and reminders / 邮件与到期提醒
│   ├── backup.go                   # JSON backup/restore / 备份恢复
│   ├── webdav.go                   # WebDAV client/auth / WebDAV
│   ├── http_helpers.go             # render and HTTP helpers / HTTP 辅助
│   ├── money.go                    # money/currency helpers / 金额与币种
│   ├── dates.go                    # date helpers / 日期工具
│   ├── util.go                     # small helpers / 通用工具
│   ├── templates.go                # template loader / 模板加载器
│   ├── main_test.go                # tests / 测试
│   └── web/templates/
│       ├── layout.html
│       ├── dashboard.html
│       ├── login.html
│       ├── settings.html
│       └── asset_form.html
└── plan.md                         # maintenance plan / 整改计划
```

---

## 模块化开发 / Modular Development

### 新增一个功能模块 / Add a feature module

1. 在 `internal/mb/` 下创建一个有明确用途的文件,例如:

   ```text
   reports.go
   ```

2. 写 handler 和相关业务逻辑。
3. 在 `internal/mb/routes.go` 中新增一个注册函数:

   ```go
   func registerReportRoutes(mux *http.ServeMux, a *app) {
       mux.HandleFunc("/admin/reports", a.withAdmin(a.handleReports))
   }
   ```

4. 把模块加进 `defaultModules()`:

   ```go
   {name: "reports", register: registerReportRoutes},
   ```

This keeps feature routes centralized and makes feature removal straightforward.

### 替换前端 / Replace the frontend

当前渲染层集中在:

```text
internal/mb/templates.go
internal/mb/web/templates/*.html
```

如果只是改页面样式或结构,修改 `web/templates/*.html` 即可。  
如果要换成独立 SPA / JSON API,可以逐步替换 `templates.go` 和 handler 的渲染调用,业务层文件可以继续复用。

For small UI changes, edit `web/templates/*.html`.  
For a full SPA / JSON API rewrite, replace the template loader/rendering layer gradually while keeping the business modules.

---

## 测试 / Tests

```bash
go test ./... -v
```

当前测试覆盖:

- SQL 写语句自检
- 备份导入/导出 round-trip
- 默认管理员密码逻辑
- 登录限流与反向代理 IP 识别
- CSP nonce 与 CSRF token
- settings 缓存失效
- 内部错误脱敏

Current tests cover:

- SQL write-statement self-check
- Backup import/export round-trip
- Admin password initialization/sync
- Login limiter and reverse-proxy IP handling
- CSP nonces and CSRF tokens
- settings cache invalidation
- Internal error redaction

---

## 备份 / Backup

可以在后台导出 JSON 备份,也可以配置 WebDAV 推送/拉取。恢复备份会替换域名/VPS 数据;如果勾选恢复设置,也会恢复设置项。

You can export JSON backups from the admin UI or configure WebDAV push/pull. Restoring a backup replaces domain/VPS data; restoring settings is optional.

---

## 部署建议 / Deployment Notes

### systemd 示例 / Example systemd unit

```ini
[Unit]
Description=MB Asset Board
After=network.target

[Service]
WorkingDirectory=/opt/mb
ExecStart=/opt/mb/mb
Restart=always
EnvironmentFile=/opt/mb/.env

[Install]
WantedBy=multi-user.target
```

### 反向代理 / Reverse proxy

建议在 Nginx / Caddy 后面启用 HTTPS。若启用代理转发头,请同时限制可信代理来源:

Use HTTPS behind Nginx / Caddy. If forwarded headers are enabled, restrict trusted proxy sources:

```env
MB_TRUST_PROXY_HEADERS=true
MB_TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128,10.0.0.0/8
```

---

## License

No license file is included yet. Add one before publishing if you want to define reuse/distribution terms.
