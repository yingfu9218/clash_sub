# clash-sub

Clash 订阅文件管理系统。Go + Gin 后端，前端静态资源全部编译进二进制 —— 部署就是拷一个可执行文件。

```
./clash-sub
```

首次启动会生成 `config.yaml` 并把登录密码打印到控制台（只打印这一次）。浏览器打开 `http://localhost:8088`。

## 构建

```bash
./scripts/fetch-vendor.sh   # 下载编辑器依赖到 web/assets/vendor（只需一次）
make build                  # -> ./clash-sub
make dist                   # 交叉编译 linux/darwin/windows
```

`CGO_ENABLED=0`，产物是静态二进制，可以直接丢进 `scratch` 镜像。

## 配置

`config.yaml`：

| 字段 | 说明 |
|---|---|
| `listen` | 监听地址，默认 `:8088` |
| `username` | 登录用户名 |
| `password_hash` | bcrypt 哈希。**删掉这一行再重启**即可重置密码 |
| `secret` | cookie 签名密钥。改掉会让所有会话立即失效 |
| `data_file` | 订阅存储路径，默认 `data/subscriptions.json` |
| `login_allow_ips` | 允许登录的 IP / CIDR 列表，如 `["1.2.3.4", "10.0.0.0/8"]`。`[]` 表示不限制。被拒绝的 IP 会打到日志里 |
| `trusted_proxies` | 前置反向代理地址。只采信来自这些地址的 `X-Forwarded-For`；直连公网留空 `[]`，本机 Nginx 填 `["127.0.0.1", "::1"]` |

旧版本生成的 `config.yaml` 缺少新字段时，启动会自动补上（连同注释）。

命令行 `-config` / `-listen` 可覆盖。

## 订阅地址

```
http://<你的地址>:8088/subscribe?token=<32位随机token>
```

token 是唯一凭据，不需要登录即可访问。响应带 `Content-Disposition`（客户端据此命名配置）和 `Profile-Update-Interval: 24`（建议每 24 小时更新）。

订阅被停用或删除后，该地址立即返回 404 —— 停用和不存在返回同样的结果，不泄露 token 是否有效。

## 接口

登录态通过 HttpOnly cookie 维持，签名是 HMAC-SHA256，服务端不存 session，重启不掉线。

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/login` | `{username, password, remember}` |
| `POST` | `/api/logout` | |
| `GET` | `/api/me` | |
| `GET` | `/api/subscriptions` | 列表（不含配置正文） |
| `POST` | `/api/subscriptions` | 新建，返回 token |
| `GET` | `/api/subscriptions/:id` | 含配置正文 |
| `PUT` | `/api/subscriptions/:id` | 改名称 / 备注 / 正文 |
| `PATCH` | `/api/subscriptions/:id` | `{enabled}` 启用或停用 |
| `POST` | `/api/subscriptions/:id/token` | 换 token，旧地址立即失效 |
| `DELETE` | `/api/subscriptions/:id` | |

保存时服务端会解析 YAML，语法不通过直接拒绝，并把解析器给出的行号原样返回。节点数、策略组数、规则数在保存时算好存下来，列表页不用再解析。

## 安全边界

- 密码 bcrypt 存储；登录失败按 IP 计数，15 分钟内 5 次锁定
- `login_allow_ips` 限制可登录的来源 IP，不在名单内直接 403，不消耗 bcrypt。只限制登录，不影响 `/subscribe`
- 客户端 IP 只在请求来自 `trusted_proxies` 时才看 `X-Forwarded-For`，否则用 TCP 对端地址，无法伪造
- `config.yaml` 与数据文件权限 `0600`
- cookie `HttpOnly` + `SameSite=Lax`，HTTPS 下自动加 `Secure`
- 数据文件先写临时文件再 rename，写一半崩溃不会毁掉配置

没有内置 TLS。放公网建议前面挂一层 Nginx / Caddy 做 HTTPS。

## 前端

`web/` 下三个页面，无框架无构建步骤：

- `login.html` — 登录
- `index.html` — 订阅列表、删除、扫码导入
- `editor.html` — YAML 编辑器（CodeMirror + js-yaml 实时校验），右栏随输入重算节点 / 策略组 / 规则数

PC 和 H5 同一套页面。编辑器在窄屏切成「编辑 / 概览」两个标签，底部固定保存条。
