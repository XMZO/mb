# mb 米表 · 改进计划

> 基于对 `main.go`(5544 行)与 `templates.go`(2328 行)的代码审查。
> 结论:安全与工程基本功已高于同类自托管项目平均水平,本计划聚焦少量真实风险点与可维护性改进。
> 创建日期:2026-07-01

## 优先级总览

| 编号 | 事项 | 优先级 | 类型 | 预计工作量 |
|------|------|--------|------|-----------|
| ~~P0-0~~ | ✅ 备份导入 VPS 的 SQL 占位符缺 1 个,导入必报错 | **已完成** | 功能 bug | 极小(一行) |
| ~~P0-1~~ | ✅ 默认密码硬编码 → `.env` + 随机/环境变量双模式 | **已完成** | 安全 | 小 |
| ~~P0-2~~ | ✅ 登录限流 / 防爆破(可配置 + 反代安全模式) | **已完成** | 安全 | 小 |
| ~~P1-1~~ | ✅ 原始错误信息脱敏(5xx/设置页内部错误) | **已完成** | 安全/健壮性 | 中 |
| ~~P1-2~~ | ✅ 仓库卫生:新仓库已初始化 + 完整 `.gitignore` | **已完成** | 工程 | 极小 |
| ~~P1-3~~ | ✅ main/templates 拆成模块化内部包 + 外置模板目录 | **已完成** | 可维护性/前端可替换 | 中 |
| P2-1 | CSP 去除 `unsafe-inline` | 🟢 可选 | 安全 | 中 |
| P2-2 | 增加 CSRF token(纵深防御) | 🟢 可选 | 安全 | 中 |
| P2-3 | settings 表重复读取,批量/缓存 | 🟢 可选 | 性能 | 中 |

---

## P0 · 必改

### P0-0 备份导入 VPS 的 SQL 占位符缺 1 个(功能性硬 bug) — ✅ 已完成

> ✅ 2026-07-01 已修复:`main.go:4474` 占位符 27 → **28**(= 列 28 = 参数 28),`go build ./...` 通过。导入含 VPS 的备份不再报错。
>
> ✅ 通用自检已补(防止再犯,`go test` 通过):
> - 新增 / 导入的 domains、vps 插入合并为共用常量 `insertDomainSQL` / `insertVPSSQL`,从结构上根除「两份 SQL 抄写不一致」这一根因。
> - 新增启动期 `validateWriteStatements`:对上述语句做 `Prepare` 自检,列数 ≠ 占位符数则**启动即失败**(fail-fast)。
> - 新增 `main_test.go` 冒烟测试:`TestWriteStatementsSelfCheck`(列 vs 占位符)+ `TestApplyBackupRoundTrip`(临时库真实执行导入路径,占位符 / 列 / 参数任一错位即失败,并校验导出 round-trip)。

**现状(已用工具核实)**
- 导入路径 `INSERT INTO vps(...)`(`main.go:4473-4475`):**28 列 / 27 个 `?` / 28 个参数**,占位符少 1 个。
- 对照新增路径(`main.go:1941-1943`):28 列 / **28** 个 `?` / 28 参数,正确。二者列名与参数顺序完全一致,仅导入路径的 `VALUES` 少写一个 `?`。

**影响**
- 导入**任何含 VPS 记录**的备份都会报错(列数 / 参数数与占位符不匹配)。
- 导入在事务中执行(`main.go:4473` 用 `tx`,失败即 `return err` 不 commit),故**同一备份中的域名数据也会一并回滚**,整个恢复失败。
- 原始错误经 `http.Error(w, err.Error())` 直接返回前端(与 P1-1 关联)。
- 新增 VPS 不受影响(走 `main.go:1941` 的正确语句)。

**方案(一行修复)**
- 将 `main.go:4474` 的 `VALUES(?,…,?)` 从 27 个 `?` 补齐为 **28** 个 `?`(列与参数顺序均正确,无需改动其余部分)。

**验收标准**
- 构造一份含 ≥1 条 VPS 的备份 JSON,导入成功、数据完整、无报错。
- 建议补一个最小回归测试或断言:同一 payload 走新增与导入两条路径应等价。
- 可选:加一个启动期/测试期的自检,校验两条 INSERT 的占位符数与列数一致,防止再次跑偏。

---

### P0-1 默认管理员密码 — ✅ 已完成

> ✅ 2026-07-01 已完成(`go build` + `go test` 通过):
> - 删除硬编码 `defaultAdminPassword`;`loadConfig` 的 `MB_ADMIN_PASSWORD` 默认值改为空。
> - 新增 `ensureAdminPassword` **双模式**:设了环境变量 → 以其为准、**重启即同步**(消除"改环境变量不生效"的困惑);未设 + 库内无密码 → **随机生成强密码并打印一次**;未设 + 已有 → 保留(交设置页管理)。
> - 新增零依赖 `.env` 自动加载(`loadDotEnv`,真实环境变量优先),并提供 `.env.example` 模板。
> - 新增 `TestEnsureAdminPassword` 覆盖三种分支。
> 实际采用方案 A 的增强版(叠加环境变量声明式同步 + `.env` 支持)。

**现状**
- `defaultAdminPassword = "wkty_mtz_O88"` 硬编码在源码(`main.go:42`),已公开在仓库中。
- `ensureDefaults` 仅在密码 hash 不存在时生成(`main.go:680-690`)。因此**首次启动后再设置 `MB_ADMIN_PASSWORD` 环境变量不会生效**,只能进设置页手动改。
- 若部署者首次启动前未设环境变量,初始密码即为源码里的公开值。

**方案(二选一,推荐 A)**
- **方案 A(推荐):去掉硬编码默认值。** 首次初始化时:
  - 若设置了 `MB_ADMIN_PASSWORD` → 用它。
  - 否则 → 用 `crypto/rand` 生成一个随机强密码,存 hash,并将明文**仅打印到启动日志一次**,提示"请登录后立即修改"。
- **方案 B(最小改动):保留常量,但启动时检测。** 若最终使用的是内置默认密码,则 `log.Printf` 打印醒目告警,提醒尽快修改。

**涉及位置**:`main.go:42`(常量)、`main.go:438-445`(loadConfig)、`main.go:680-690`(ensureDefaults)

**验收标准**
- 未设环境变量时,日志出现随机密码(方案 A)或安全告警(方案 B)。
- 源码中不再存在可直接登录的固定明文密码(方案 A)。
- README / 文档补充首次登录说明。

---

### P0-2 登录速率限制 — ✅ 已完成

> ✅ 2026-07-01 已完成(`go build` + `go test -v` 通过):
> - 新增内存登录限流器 `loginLimiter`:默认同一客户端 **15 分钟内失败 5 次后锁定 15 分钟**,返回 `429 Too Many Requests` 并带 `Retry-After`。
> - 登录成功后清理该客户端失败状态;限流状态惰性清理,并有 `MB_LOGIN_MAX_ENTRIES` 上限,避免长期运行 map 增长。
> - 限流参数可配置:`MB_LOGIN_MAX_FAILURES`、`MB_LOGIN_WINDOW`、`MB_LOGIN_LOCKOUT`、`MB_LOGIN_MAX_ENTRIES`。
> - 客户端识别默认使用 `RemoteAddr`,**默认不信任** `X-Forwarded-For` / `X-Real-IP`,避免攻击者伪造头绕过限流。
> - 可信反代部署可显式开启:`MB_TRUST_PROXY_HEADERS=true`;并可用 `MB_TRUSTED_PROXY_CIDRS` 限定只有指定代理 CIDR 的转发头才被采信。
> - `.env.example` 已补充全部配置说明。
> - 新增测试:`TestLoginLimiterLockoutAndClear`、`TestClientIPTrustProxyHeaders`、`TestHandleLoginRateLimit`。

**现状**
- `handleLogin`(`main.go:1073`)对密码错误无任何计数、延迟或锁定。
- `bcrypt` 慢哈希提供部分保护,但无法阻止持续爆破 / 撞库。

**方案**
- 新增一个轻量内存限流器(应用为单实例,内存方案足够):
  ```go
  type loginLimiter struct {
      mu       sync.Mutex
      attempts map[string]*attemptState // key = 客户端 IP
  }
  ```
  - 记录每个 IP 的失败次数与首次失败时间。
  - 策略示例:同一 IP 在 15 分钟内失败 ≥ 5 次,后续请求返回 `429 Too Many Requests`(或递增延迟),登录成功后清零。
  - IP 取值需正确处理反向代理:优先解析 `X-Forwarded-For`(与 `requestIsSecure` 的信任模型一致),否则用 `RemoteAddr`。
- 需要定期清理过期条目,避免 map 无限增长(可在每次访问时惰性清理)。

**涉及位置**:`main.go:1073-1113`(handleLogin)、`app` 结构(`main.go:108-114`)挂载 limiter。

**验收标准**
- 连续多次错误密码后被限流(429 或明显延迟)。
- 正确密码登录成功后计数重置。
- 长时间运行不会因 limiter map 泄漏内存。

---

## P1 · 建议

### P1-1 错误信息脱敏 — ✅ 已完成

> ✅ 2026-07-01 已完成(`go build` + `go test -v` 通过):
> - 新增 `serverError(w, err)`:服务端日志记录完整错误,客户端统一返回 `服务器内部错误`。
> - 全文替换所有 `http.Error(w, err.Error(), http.StatusInternalServerError)`;复扫确认 500 响应只剩统一 helper。
> - `server not initialized`、模板缺失也改为统一脱敏。
> - 设置页内部/外部服务失败(邮件测试、备份生成/导入、WebDAV 上传/下载、汇率拉取等)改为安全提示 + 服务端日志;保留用户输入校验类提示(如 WebDAV 地址格式、备份 JSON 格式错误)。
> - 新增 `TestServerErrorRedactsDetails`,确保响应体不会泄漏 `sqlite` / 内部路径等原始错误。

**现状**
- 大量 `http.Error(w, err.Error(), http.StatusInternalServerError)`(如 `main.go:994、999、1711`)将原始错误(可能含 SQL 语句、内部路径)直接返回给客户端。

**方案**
- 新增统一助手:
  ```go
  func (a *app) serverError(w http.ResponseWriter, err error) {
      log.Printf("internal error: %v", err) // 记录完整错误
      http.Error(w, "服务器内部错误", http.StatusInternalServerError)
  }
  ```
- 全局把 `http.Error(w, err.Error(), 500)` 替换为 `a.serverError(w, err)`。
- 400/校验类错误可保留面向用户的中文提示(那些是有意暴露的),仅对 5xx 脱敏。

**涉及位置**:全文搜索 `http.Error(w, err.Error()` 逐处替换。

**验收标准**:客户端不再收到含 SQL / 路径的原始错误;完整错误仍可在服务端日志查到。

---

### P1-2 仓库卫生 — ✅ 已完成

> ✅ 2026-07-01:用户已新建远端仓库 `https://github.com/XMZO/mb`。
> 已重新 `git init`,设置 `origin=https://github.com/XMZO/mb`,并生成完整 `.gitignore`:
> - 忽略构建产物:`/mb`、`/mb.exe`、`*.exe`、`*.test`、`*.out` 等;
> - 忽略运行数据:`*.db`、`*.db-*`、`mb.db*`;
> - 忽略真实环境文件:`.env`、`.env.*`,但保留 `.env.example`;
> - 忽略备份/临时/本地工具文件:`*.bak`、`*.tmp`、`*.log`、`__scan*.ps1`、`.claude/` 等。
> 当前未提交、未 push;待用户确认后再执行对外动作。

**现状**
- `.gitignore` 已忽略 `mb.db`,但未忽略:
  - `mb` —— Linux 二进制,约 13MB(`*.exe` 只挡了 Windows);
  - `run.bak` —— 备份文件。
- 二者当前处于待提交(untracked)状态。

**方案**
- 向 `.gitignore` 追加:
  ```gitignore
  # 编译产物(无扩展名的二进制)
  /mb
  # 备份 / 临时
  *.bak
  ```
- 若历史提交中已包含二进制/数据库,后续可考虑 `git rm --cached` 清理(不影响本地文件)。

**验收标准**:`git status` 不再出现 `mb`、`run.bak`。

---

### P1-3 单文件拆分 — ✅ 已完成

> ✅ 2026-07-01 已完成(`go build ./...` + `go test ./... -v` 通过):
> - 根目录 `main.go` 变成极薄入口,只调用 `mb/internal/mb.Run()`。
> - 原大 `main.go` 拆入 `internal/mb/` 多个功能文件,每个文件有明确用途:`auth.go`、`domain.go`、`vps.go`、`settings.go`、`backup.go`、`webdav.go`、`email.go`、`fx.go`、`sort.go`、`db.go`、`config.go`、`http_helpers.go`、`money.go`、`dates.go`、`util.go`、`types.go`、`run.go`、`routes.go`。
> - 路由集中到 `routes.go` 的 `featureModule` / `defaultModules`,新增/删除功能只需增删模块注册点,不再把所有路由写死在启动函数里。
> - 原 `templates.go` 的 HTML 字符串常量全部外置为 `internal/mb/web/templates/*.html`,Go 代码只保留模板加载器;后续换前端可以替换模板目录或替换渲染层。
> - 测试迁移到 `internal/mb/main_test.go`,继续覆盖 SQL 自检、备份 round-trip、默认密码、登录限流、错误脱敏。
> - `templates.go` 和根目录 `main_test.go` 已移除(内容迁移完成),不影响单二进制构建。

**现状**
- `main.go` 达 5544 行。git 历史显示原本按职责拆分(`dashboard_handler.go` / `domain_actions.go` / `vps_actions.go` / `actions_common.go` / `templates_dashboard.go`),现已合并。
- 单文件便于分发,但后续加功能、定位代码成本高。

**方案(建议拆分,仍在同一 `package main`,不影响单二进制构建)**

| 文件 | 内容 |
|------|------|
| `main.go` | `main()`、`config`、`app`、路由注册 |
| `db.go` | `openDB`、`migrate`、`ensureDefaults`、settings 读写 helper |
| `auth.go` | cookie secret、session、中间件、login/logout、(P0-2)limiter |
| `domains.go` | domain 相关 handler + `loadDomains` / `loadDomainItem` |
| `vps.go` | vps 相关 handler + `loadVPS` / `loadVPSItem` |
| `fx.go` | 汇率抓取/存储/换算 |
| `email.go` | SMTP、提醒循环、`startReminderLoop` |
| `backup.go` | 备份导入导出、WebDAV |
| `settings.go` | 设置页 handler + 表单解析 |
| `util.go` | 日期/金额/磁盘/字符串等纯函数 |
| `templates.go` | 保持不变 |

- 纯机械移动,不改逻辑;每移一组用 `go build ./...` 验证。

**验收标准**:`go build` 通过,`go vet` 无新增告警,单个文件控制在合理体量。

---

## P2 · 可选(纵深防御 / 优化)

### P2-1 CSP 收紧
- 现 `script-src` / `style-src` 含 `'unsafe-inline'`(`main.go:853`),削弱 XSS 纵深防御(模板自动转义已兜底)。
- 方案:内联脚本/样式改为外部文件或加 `nonce`,随后移除 `'unsafe-inline'`。工作量取决于模板里内联代码量。

### P2-2 CSRF token
- 现仅靠 `SameSite=Lax` 防护(对 POST 表单已基本够用)。
- 如需纵深防御:采用 synchronizer token 或 double-submit cookie,在所有状态变更表单注入隐藏 token 并在服务端校验。

### P2-3 settings 读取优化
- 单次请求会多次查询 settings 表(中间件查 `public_mode`,handler 再查一次,各 `show_*` 逐条查)。
- 方案:一次性 `SELECT key,value FROM settings` 载入 map;或加带失效的进程内缓存。SQLite 本地读很快,收益有限,低优先级。

---

## 建议执行顺序

1. **P0-0**(功能性硬 bug,一行修复)—— 最先做。
2. ~~**P0-1、P0-2**(安全,改动小、收益高)。~~ ✅ 已完成
3. ~~**P1-2**(仓库卫生,顺手)。~~ ✅ 已完成
4. ~~**P1-1**(错误脱敏,可与 P1-3 结合)。~~ ✅ 已完成
5. ~~**P1-3**(拆分)—— 放在功能稳定后做,纯重构,单独一个提交/PR。~~ ✅ 已完成
6. **P2** 视需要而定。

> 每一项建议独立提交,便于回滚与 review。重构类改动(P1-3)与逻辑改动分开提交。
