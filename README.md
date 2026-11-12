# MB 米表 / MB Asset Board

一个轻量、自托管的域名与 VPS 资产面板：记录到期时间和续费价格，统计年度成本，临近到期时发邮件提醒，支持公开只读展示和 JSON / WebDAV 备份。单个二进制文件，数据存在一个 SQLite 文件里，不依赖任何外部服务或 CDN。

A lightweight self-hosted board for domains and VPS instances: tracks expiry dates and renewal prices, totals yearly cost across currencies, emails you before things expire, and offers a public read-only view plus JSON / WebDAV backups. One binary, one SQLite file, no CDN or external services required.

---

## 功能 / Features

- **一页看全**：域名和 VPS 排成紧凑的自适应网格（手机 1 列，1080p 屏幕 6 列），不分页、不分标签页；长域名和备注完整换行显示，不截断。
- **到期一目了然**：顶部的「已过期 / 30 天内 / 90 天内」既是统计数字也是筛选按钮；只有需要处理的条目才会上色。
- **搜索、排序、拖拽**：按名称、后缀、到期时间、价格、注册商 / 地区排序；默认顺序可以拖拽调整，也可以把任意排序结果保存为默认顺序。排序、筛选、拖拽和操作之后，小块会平滑地移到新位置。
- **管理**：点击条目弹出面板，一键续费（+1 年 / 一个付费周期）、编辑、置顶、隐藏、删除；批量模式可多选后一起操作；域名可导出为 TXT / CSV。
- **多币种**：130 多种法币，自动拉取汇率（open.er-api.com，失败时退回欧洲央行），可以手动指定个别币种的汇率，手动值不会被自动更新覆盖。
- **公开 / 私有**：公开模式下访客只读访问，可以逐项决定访客能看到哪些字段，也可以把单条记录完全隐藏。
- **到期提醒**：按「提前 N 天」发邮件；停机期间错过的提醒会在恢复后补发。SMTP 支持 STARTTLS / SSL，PLAIN / LOGIN 认证。
- **备份恢复**：JSON 导出 / 导入，WebDAV 推送 / 拉取（Basic、Digest 认证，以及预签名地址）。
- **安全**：bcrypt 密码、HMAC 签名的会话 Cookie、登录限流、CSRF 防护、严格的 CSP（没有内联脚本和样式）。

English summary: one dense, responsive page for all assets (no pagination or tabs); status counts double as filters; click an item for one-click renewal and other actions; multi-currency totals with automatic FX rates and manual overrides; public read-only mode with per-field visibility; email reminders that catch up after downtime; JSON / WebDAV backups.

---

## 快速开始 / Quick Start

```bash
cp .env.example .env   # 按需修改,也可以不改
go run .
```

打开 <http://127.0.0.1:15000>。如果没有设置 `MB_ADMIN_PASSWORD`，首次启动会生成随机管理员密码并**只在日志里显示一次**。

Open <http://127.0.0.1:15000>. Without `MB_ADMIN_PASSWORD`, a random admin password is generated on first start and printed to the log **once**.

构建 / Build:

```bash
go build -o mb .
```

子命令 / Commands: `mb`（启动服务）、`mb healthcheck`（供容器健康检查）、`mb version`。

### Docker Compose

推送 `v*` tag 会触发 GitHub Actions 构建并发布镜像到 GHCR（Linux amd64 / arm64，以及 Windows 容器）：

```bash
git tag v1.0.0
git push origin v1.0.0
```

部署 / Deploy:

```bash
cp .env.example .env    # 把 MB_IMAGE 改成 ghcr.io/<owner>/<repo>:latest 或指定 tag
docker compose pull
docker compose up -d
```

- 端口在 `docker-compose.yml` 的 `ports` 里改（例如 `"8080:15000"`）；`MB_ADDR` 只用于直接运行二进制的场景。
- 数据库在宿主机的 `./data/mb.db`。容器以 UID/GID `10001` 运行，如果无法写入：`sudo chown -R 10001:10001 data`。
- 镜像基于 `scratch`，没有 shell；排查问题请看 `docker compose logs mb`。
- Windows 容器使用 `docker compose -f docker-compose.windows.yml up -d`。

---

## 配置 / Configuration

通过环境变量配置。启动时会读取工作目录下的 `.env`，真实环境变量优先。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MB_ADDR` | `:15000` | 监听地址，也可以只写端口号 / Listen address |
| `MB_DB` | `./mb.db` | SQLite 数据库文件路径 / Database path |
| `MB_ADMIN_PASSWORD` | 空 | 管理员密码。设置后以它为准（每次启动同步，页面上不能再修改）；留空则首次启动随机生成 |
| `MB_COOKIE_NAME` | `mb_admin` | 会话 Cookie 名称 |
| `MB_LOGIN_MAX_FAILURES` | `5` | 时间窗口内允许的登录失败次数，`0` 关闭限流 |
| `MB_LOGIN_WINDOW` | `15m` | 统计失败次数的时间窗口 |
| `MB_LOGIN_LOCKOUT` | `15m` | 触发限流后的锁定时长 |
| `MB_LOGIN_MAX_ENTRIES` | `4096` | 限流器在内存中记录的客户端数量上限 |
| `MB_TRUST_PROXY_HEADERS` | `false` | 是否信任 `X-Forwarded-For` / `X-Real-IP`（仅用于登录限流） |
| `MB_TRUSTED_PROXY_CIDRS` | 空 | 可信代理的 IP / CIDR 列表，逗号分隔 |
| `TZ` | 系统时区 | 计算「剩余天数」使用的时区，例如 `Asia/Shanghai`。时区数据已内嵌，容器里也可用 |

> 只有部署在可信的反向代理（Nginx / Caddy / Cloudflare 等）后面时才开启 `MB_TRUST_PROXY_HEADERS`，并尽量同时配置 `MB_TRUSTED_PROXY_CIDRS`。客户端地址取 `X-Forwarded-For` 中最靠右的非代理地址，无法被客户端伪造。
>
> Enable `MB_TRUST_PROXY_HEADERS` only behind a trusted reverse proxy. The client address is the right-most non-proxy entry of `X-Forwarded-For`, which clients cannot spoof.

其余设置（公开模式、访客可见字段、汇率、邮件、WebDAV、站点图标）都在登录后的「设置」页面里。

---

## 从旧版本升级 / Upgrading

直接替换二进制或镜像即可，数据库会在首次启动时原地升级，旧的备份文件也可以照常导入。需要留意的变化：

- **登录状态会失效一次**：会话 Cookie 的格式变了，升级后需要重新登录。
- **列表不再分页**；访客看不到被「隐藏」的条目（之前显示为一行「已隐藏」），只在末尾提示「另有 N 个未公开」。
- **提醒改为按档位补发**：升级后第一次检查时，处于提醒窗口内、但这一档还没提醒过的条目会合并成一封邮件发出。
- **备份文件不再包含 Cookie 签名密钥**；恢复设置时始终保留当前实例的密钥。
- 数据库保留了旧版的全部列，升级后仍然可以用旧版程序打开（回滚安全）。

Upgrading is in place: replace the binary or image. Sessions are invalidated once; the list is no longer paginated; hidden items are omitted from the public view; reminders catch up per threshold; backups no longer contain the cookie signing secret. Old columns are kept, so rolling back remains possible.

---

## 目录结构 / Project Layout

```text
main.go                      入口:解析子命令
internal/
  app/                       装配:打开数据库、启动 HTTP 服务与后台任务、优雅退出
  config/                    环境变量与 .env
  asset/                     域名 / VPS 的数据模型与纯计算(到期天数、顺延月份、年化价格)
  money/                     币种表、汇率换算、金额格式化
  store/                     SQLite:建表与迁移、设置项、资产的增删改查、提醒日志
  fx/                        汇率拉取与手动汇率
  mailer/                    SMTP 发信
  reminder/                  到期提醒
  backup/                    JSON 备份、WebDAV
  usererr/                   可以直接展示给用户的错误
  buildinfo/                 构建时注入的版本号
  web/                       HTTP 层
    server.go                路由表(每个地址需要什么权限一目了然)
    middleware.go            安全响应头、鉴权、表单 / JSON 接口的 CSRF 校验
    auth.go  limiter.go      密码、会话、登录、限流、客户端地址
    render.go                模板渲染、静态资源、一次性提示
    dashboard.go  views.go   面板首页;访客可见范围在 views.go 里统一处理
    forms.go                 域名 / VPS 的新增与编辑表单
    api.go                   批量操作、排序、域名导出
    settings.go  backups.go  设置页与备份恢复
    templates/               页面模板(Go html/template)
    static/                  app.css、app.js、boot.js(内嵌进二进制)
```

依赖方向是单向的：`web` → 业务包（`fx`、`mailer`、`reminder`、`backup`）→ `store` → `asset` / `money`。

几条贯穿全局的约定：

- **错误**：只有 `usererr` 的文案会返回给浏览器；其它错误一律只写服务端日志，页面显示统一提示。
- **访客权限**：访客不该看到的字段在生成视图数据时就被清空（`web/views.go`），模板不做权限判断。
- **前端**：页面由服务端渲染，`app.js` 只负责交互，通过 `data-*` 属性绑定行为；没有内联脚本和样式，也没有第三方库。动画只改 transform / opacity，不影响排版；系统开启「减少动态效果」时全部关闭。
- **写操作**：表单走「提交 → 重定向 → 显示结果」；面板上的操作走 `POST /api/{kind}/batch`，单条操作就是只含一个 ID 的批量操作。操作成功后不整页刷新：脚本在后台重新取一次面板，只替换概览和小块（剩余天数、总额等仍由服务端计算），搜索、筛选、批量选择和滚动位置保持不变。

---

## 开发 / Development

```bash
go test ./...
go vet ./...
```

测试覆盖：旧版数据库升级、资产增删改查与排序、汇率拉取与回退、SMTP 发信（内置的假服务器）、提醒的补发与去重、备份的往返与旧格式兼容、WebDAV 的 Basic / Digest 认证，以及 Web 层的登录、限流、CSRF、访客可见范围、各表单与接口。

构建时可以注入版本号 / Inject version info at build time:

```bash
go build -ldflags "-X mb/internal/buildinfo.Version=v1.2.3 -X mb/internal/buildinfo.Commit=$(git rev-parse HEAD)" -o mb .
```

---

## 部署建议 / Deployment Notes

systemd:

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

建议放在 Nginx / Caddy 后面并启用 HTTPS，同时配置：

```env
MB_TRUST_PROXY_HEADERS=true
MB_TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128
```

备份文件里包含管理员密码的哈希，以及邮箱、WebDAV 的密码，请妥善保管。

---

## License

No license file is included yet. Add one before publishing if you want to define reuse/distribution terms.
