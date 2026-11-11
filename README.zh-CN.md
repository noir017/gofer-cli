# gofer-cli

[![CI](https://github.com/noir017/gofer-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/noir017/gofer-cli/actions/workflows/ci.yml)

[Gofer](https://github.com/cristianadrielbraun/gofer)（自托管 webmail）的非官方命令行客户端。它按 Gofer 网页的方式访问服务器，面向 agent 和脚本：所有输入来自参数、环境变量、配置文件或 stdin，结果只写 stdout，诊断只写 stderr，从不交互。

与 Gofer 项目无隶属关系。

## 安装

```
go install github.com/noir017/gofer-cli/cmd/gofer-cli@latest
```

或者从 [Releases](https://github.com/noir017/gofer-cli/releases) 下载 linux、darwin、windows（amd64、arm64）的二进制，放进 `PATH`。

## 快速开始

```
export GOFER_URL=https://gofer.example.com
export GOFER_USER=me@example.com
export GOFER_PASSWORD_CMD='pass show mail/gofer'

gofer-cli login
gofer-cli list --unread --limit 5
gofer-cli read 1234
```

`login` 把会话 cookie 以 0600 权限存进缓存目录。后续命令复用它，过期后自动重新登录。

## 配置

取值顺序：参数 > 环境变量 > 配置文件。

| 参数 | 环境变量 | 配置键 | 含义 |
| --- | --- | --- | --- |
| `--url` | `GOFER_URL` | `url` | 服务器地址，如 `https://gofer.example.com` |
| `--user` | `GOFER_USER` | `user` | 登录名（邮箱地址） |
| | `GOFER_PASSWORD` | `password` | 密码 |
| | `GOFER_PASSWORD_CMD` | `password_cmd` | 输出密码的命令，用 `sh -c` 执行，stdout 即密码 |
| | `GOFER_TOTP_SECRET` | `totp_secret` | 开启两步验证的账号所用的 base32 TOTP 密钥 |
| `--config` | `GOFER_CONFIG` | | 配置文件，默认在系统配置目录下的 `gofer-cli/config.toml`（Linux 下是 `~/.config` 或 `$XDG_CONFIG_HOME`） |
| | `GOFER_CACHE_DIR` | | 会话 cookie 目录，默认在系统缓存目录下的 `gofer-cli`（Linux 下是 `~/.cache` 或 `$XDG_CACHE_HOME`） |

配置文件是 TOML：

```toml
url = "https://gofer.example.com"
user = "me@example.com"
password_cmd = "pass show mail/gofer"
totp_secret = "JBSWY3DPEHPK3PXP"
```

`password_cmd` 让密码不落在文件里，命令可以是任意工具：

```toml
password_cmd = "pass show mail/gofer"                 # pass
password_cmd = "op read op://private/gofer/password"  # 1Password CLI
```

优先用 `password_cmd` 而不是 `password`：命令只在需要密码时执行，输出也不会写进配置文件。

`password_cmd` 用 `sh -c` 执行。Windows 上没有 `sh`，请改用 `password`，或自备一个 `sh`。

## 命令

全局参数：`--url`、`--user`、`--config`、`--json`。所有命令都支持 `--json`（含 `doctor`），结果按 JSON 输出；`--raw`、`--html` 与 `--json` 同时给会报错。

| 命令 | 作用 |
| --- | --- |
| `list [-q 搜索语法] [--to A] [--from A] [--subject S] [--account EMAIL] [--unread] [--limit 20]` | 搜索或列出邮件 |
| `read <id> [--raw] [--html]` | 输出一封邮件：正文文本（默认）、原始源码或 HTML |
| `latest [过滤参数] [--raw] [--html]` | 符合条件的最新一封 |
| `otp [过滤参数] [--since 10m] [--wait 秒] [--pattern 正则]` | 只输出验证码 |
| `link [过滤参数] [--since 10m] [--wait 秒] [--match 正则]` | 只输出链接 |
| `wait [过滤参数] [--timeout 120]` | 等一封新邮件，然后按 `read` 的文本或 JSON 格式输出 |
| `attachments <id>` | 列出附件 |
| `save <id> [--index N] [-o 目录]` | 下载附件，输出写入的路径 |
| `mark read\|unread\|star\|unstar <id>... \| -` | 改邮件标记；`-` 从 stdin 读 id |
| `archive \| spam \| delete <id>... \| -` | 移到归档、垃圾邮件或回收站 |
| `move <id>... --folder 名称或ID [--account 邮箱]` | 移到指定文件夹 |
| `send --to A[,A] [--from 账号] [--subject S] [--cc A[,A]] [--bcc A[,A]] [--body 文本 \| --body-file 文件 \| 标准输入] [--attach 文件]...` | 发信；只有 `--to` 必填 |
| `reply <id> [--all] [--body 文本 \| --body-file 文件 \| 标准输入] [--attach 文件]...` | 回复；不给 `--from` 时用原邮件所在的账号 |
| `accounts` | 列出账号 |
| `folders [--account EMAIL]` | 列出文件夹 |
| `sync [--account EMAIL]` | 让服务器拉取新邮件 |
| `login` / `logout` | 建立或清除保存的会话 |
| `doctor` | 检查配置和连通性 |
| `version` | 输出版本 |

`list`、`latest`、`otp`、`link`、`wait` 共用一套过滤参数。`--to`、`--from`、`--subject` 是各自独立的筛选条件；`-q` 里可以用和 Gofer 搜索框一样的写法：`from:`、`to:`、`subject:`、`body:`、`after:`、`before:`、`is:unread` 等。本工具翻译不了的关键字（如 `tag:`、`attachment:`、`larger:`）会直接报错，不会当成普通文本去搜。

`delete` 只把邮件移到回收站，不永久删除。

## 在 agent 里使用

从新邮件里取验证码，最多等一分钟：

```
gofer-cli otp --from no-reply@example.com --subject "Sign in" --since 5m --wait 60
```

邮件里有多个数字时，用 `--pattern` 限定什么算验证码。

从新邮件里取链接：

```
gofer-cli link --from no-reply@example.com --wait 60 --match 'https://example.com/reset'
```

等一封邮件并以 JSON 输出整封内容：

```
gofer-cli wait --from alerts@example.com --timeout 120 --json
```

把所有未读标为已读：

```
gofer-cli list --unread --limit 100 | gofer-cli mark read -
```

`list` 写到 stdout 的邮件 id 正是 `mark` 从 stdin 读取的格式。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 出错 |
| 2 | 没有匹配或等待超时（`latest`、`otp`、`link`、`wait`）；id 不存在（`read`、`attachments`、`save`、`mark`、`archive`、`delete`、`move`）；`--account` 或 `--folder` 指定的对象不存在 |
| 3 | 登录或配置问题 |

`list`、`accounts`、`folders` 结果为空时退出码是 0。

## 兼容性

已在 Gofer v0.23.0 上实测。Gofer 尚未到 1.0，版本之间会变；其他版本未必不能用，但升级服务器后先跑 `gofer-cli doctor`。

## 已知限制

Gofer 开着「按会话分组」时，一个会话只列出其中最新的一封。这类条目在 JSON 输出里带 `"thread": true` 标记。

## 工作原理

Gofer 没有公开 API。本工具只调 Gofer 网页自己用的几个接口：返回 JSON 的搜索接口取邮件 id，原始邮件接口取完整内容，写操作与网页提交同样的表单，并带上 `X-Gofer-Request: 1` 头，让 Gofer 接受非浏览器客户端。Gofer 升级后这些接口可能变化，届时 gofer-cli 需要跟着改。

## 许可

MIT，见 [LICENSE](LICENSE)。
