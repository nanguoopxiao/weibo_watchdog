# Weibo Visibility Monitor

一个用 Go 写的微博公开可见性监控工具。现在支持两种模式：

- `bot`：Telegram Bot 多用户订阅模式。用户 `/start` 后发送微博 UID，工具自动记录 `chat_id -> uid` 并开始监控。
- `check/run`：本地配置账号的单机监控模式，适合只给自己用。

## Telegram Bot 模式

用户流程：

```text
/start
机器人：请发送要监控的微博用户 UID
用户：<微博用户 UID>
机器人：初始化完成，已记录最近 20 条微博，并列出内容梗概
```

之后服务会：

```text
订阅库 chat_id -> uid
  -> 每 10 分钟按 UID 扫描最近 20 条微博
  -> 新微博自动进入追踪器
  -> 检测到新发布微博时推送“已录入”提示和内容梗概
  -> 详情页变成公开不可见时推送给对应 chat_id
```

### 配置

复制配置模板：

```powershell
Copy-Item config.example.json config.json
```

`config.json` 的核心配置：

```json
{
  "interval": "10m",
  "database": {
    "type": "json",
    "dsn_env": "WEIBO_WATCHDOG_DATABASE_URL"
  },
  "telegram_bot": {
    "enabled": true,
    "bot_token_env": "TELEGRAM_BOT_TOKEN",
    "poll_timeout": "25s",
    "default_recent_limit": 20,
    "default_lookback_days": 0,
    "default_max_pages": 3,
    "include_reposts": true
  },
  "accounts": []
}
```

默认 `database.type=json`，会继续使用本地 `data/*.json`。如果要用 PostgreSQL：

```json
"database": {
  "type": "postgres",
  "dsn_env": "WEIBO_WATCHDOG_DATABASE_URL"
}
```

服务器上可以这样加载连接串：

```bash
source /root/weibo_watchdog_db.env
./weibo-monitor-linux-arm64 db-check -config config.json
```

不要把 Telegram token 写进配置文件。用环境变量：

```powershell
$env:TELEGRAM_BOT_TOKEN="你的 Bot token"
```

用户级持久设置：

```powershell
[Environment]::SetEnvironmentVariable("TELEGRAM_BOT_TOKEN", "你的 Bot token", "User")
```

启动 Bot 服务：

```powershell
.\weibo-monitor.exe bot -config config.json
```

Bot 支持：

- `/start`：开始订阅流程
- 直接发送数字 UID：设置或更新当前聊天监控的微博账号
- `/status`：查看当前聊天订阅的 UID
- `/stop`：停止当前聊天订阅
- `/help`：查看帮助

## 是否需要数据库

需要，但 MVP 不必上独立数据库服务。

当前使用本地 JSON 文件作为轻量数据库：

- `data/bot.json`：Telegram `update_offset`、`chat_id -> uid` 订阅关系
- `data/state.json`：微博追踪器状态，包含 UID、微博链接、正文片段、发布时间、最新状态
- `data/checks.jsonl`：每次检测历史，一行一个 JSON

这个结构足够支撑个人和小范围多人使用。之后如果用户量变大，可以切到 PostgreSQL。

也可以切到 PostgreSQL。程序会自动建表和迁移 schema：

- `app_state`：Telegram update offset 等键值状态
- `subscriptions`：`chat_id -> weibo_uid` 订阅关系
- `weibo_posts`：微博追踪器最新状态
- `post_checks`：每次检测历史

联调命令：

```bash
source /root/weibo_watchdog_db.env
./weibo-monitor-linux-arm64 db-check -config config.json
```

## 本地账号模式

如果不想用 Telegram Bot，也可以在 `accounts` 里手工配置账号，然后跑：

```powershell
.\weibo-monitor.exe check -config config.json
.\weibo-monitor.exe run -config config.json
```

账号配置示例：

```json
"accounts": [
  {
    "name": "示例账号",
    "uid": "替换为微博用户 UID",
    "recent_limit": 20,
    "lookback_days": 0,
    "max_pages": 3,
    "include_reposts": true
  }
]
```

## IPv6

默认自动双栈：

```json
"network": {
  "timeout": "20s",
  "ip_family": "auto",
  "proxy": ""
}
```

强制 IPv6：

```json
"ip_family": "ipv6"
```

强制 IPv4：

```json
"ip_family": "ipv4"
```

如果需要代理：

```json
"proxy": "http://127.0.0.1:7890"
```

禁用系统代理环境变量：

```json
"proxy": "none"
```

## 状态含义

| 状态 | 含义 |
| --- | --- |
| `PUBLIC_VISIBLE` | 公开页面里找到了自动发现的正文片段 |
| `LIKELY_VISIBLE` | 没有正文片段可比对，但没有明显异常标记 |
| `PUBLIC_INVISIBLE` | 跳转到 sorry/pagenotfound，或出现不存在/无权限标记 |
| `LOGIN_WALL` | 被登录墙拦住，不能断言是否被隐藏 |
| `RATE_LIMITED` | 触发验证码、访问频繁、安全验证等风控 |
| `UNKNOWN` | 没找到决定性证据 |
| `NETWORK_ERROR` | 网络连接失败、超时、DNS 或 TLS 错误 |

## 编译部署

当前平台：

```powershell
go build -o weibo-monitor.exe .
```

Linux amd64：

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o weibo-monitor-linux-amd64 .
```

macOS arm64：

```powershell
$env:GOOS="darwin"; $env:GOARCH="arm64"; go build -o weibo-monitor-darwin-arm64 .
```

Windows amd64：

```powershell
$env:GOOS="windows"; $env:GOARCH="amd64"; go build -o weibo-monitor-windows-amd64.exe .
```

## 常驻运行

Windows 可以用任务计划程序或 NSSM 把下面命令做成服务：

```powershell
C:\tools\weibo-monitor\weibo-monitor.exe bot -config C:\tools\weibo-monitor\config.json
```

Linux 可以用 systemd 跑：

```bash
/opt/weibo-monitor/weibo-monitor bot -config /opt/weibo-monitor/config.json
```

## License

MIT License. See [LICENSE](LICENSE).
