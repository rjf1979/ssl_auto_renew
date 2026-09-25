# SSL Auto Renew Workspace

本仓库包含两个平级子项目，分别负责证书生命周期和站点管理。

```text
ssl_auto_renew/
├── renew-service/   # Go：申请、续签和部署 SSL 证书
├── web-admin/       # Go + Vue 3/Vite：管理 DNS 和 Nginx 证书绑定
├── deploy.env       # 共享 ECS 部署连接配置，不提交 Git
├── .tools/          # 本地构建工具
└── .gitignore
```

`renew-service` 和 `web-admin` 使用独立的 Go 模块、数据库、systemd 服务和运行目录，不共享 Go 内部包。两者通过已安装的 `sslctl`、证书目录和 Nginx 配置协作。

## 子项目

- [renew-service](renew-service/README.md)：ACME 申请、DNS-01 验证、证书续签和证书部署。
- [web-admin](web-admin/README.md)：单授权码登录、AliDNS 记录管理和 Nginx 站点绑定。

## 生产部署指南

### 项目边界

`renew-service` 只负责 ACME 申请、DNS-01 验证、续签、证书版本化存储和 Nginx reload。`web-admin` 负责多根域名、AliDNS 解析、证书申请记录和 Nginx 站点绑定，不实现 ACME。

两个项目不共享 Go 内部包、SQLite 数据库或 systemd 服务，通过以下本地文件协作：

```text
/etc/ssl-auto-renew/config.yaml
/etc/ssl-auto-renew/certificates.yaml
/etc/ssl-auto-renew/certs/<certificate-name>/current/
/etc/nginx/conf.d/
```

### ECS 要求

- Linux ECS、Nginx、Go 1.25+、Node.js/npm。
- 目标域名已由阿里云 DNS 托管。
- ECS 已绑定 RAM 角色，并允许 AliDNS 查询、创建和删除 TXT 记录。
- 管理后台只监听 `127.0.0.1`，公网访问由 Nginx HTTPS 反向代理。

ECS RAM 角色由 SDK 和 lego 获取临时凭据，不要在仓库中保存 AccessKey、Secret 或 STS Token。

### 构建

```bash
cd renew-service
go mod tidy
go test ./...
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/sslctl-linux-amd64 ./cmd/sslctl

cd ../web-admin/web
npm install --registry=https://registry.npmmirror.com
npm run build
cd ..
go mod tidy
go test ./...
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/ssl-admin-linux-amd64 ./cmd/ssl-admin
```

### 安装续签服务

```bash
sudo install -d -m 0750 /etc/ssl-auto-renew/secrets
sudo install -d -m 0750 /etc/ssl-auto-renew/certs /var/lib/ssl-auto-renew
sudo install -m 0755 sslctl-linux-amd64 /usr/local/bin/sslctl
sudo install -m 0644 config.yaml /etc/ssl-auto-renew/config.yaml
sudo install -m 0644 ssl-auto-renew.service /etc/systemd/system/ssl-auto-renew.service
sudo install -m 0644 ssl-auto-renew.timer /etc/systemd/system/ssl-auto-renew.timer
sudo systemctl daemon-reload
sudo /usr/local/bin/sslctl config validate --config /etc/ssl-auto-renew/config.yaml
```

生产配置使用 `https://acme-v02.api.letsencrypt.org/directory`，并设置：

```yaml
certificate_manifest: /etc/ssl-auto-renew/certificates.yaml
```

### 安装管理后台

```bash
sudo install -d -m 0750 /opt/ssl-admin/web /etc/ssl-admin /var/lib/ssl-admin
sudo install -m 0755 ssl-admin-linux-amd64 /opt/ssl-admin/ssl-admin
sudo cp -a web/dist/. /opt/ssl-admin/web/dist/
sudo install -m 0644 deploy/ssl-admin.service /etc/systemd/system/ssl-admin.service
sudo chmod 0640 /etc/ssl-admin/admin.env
sudo systemctl daemon-reload
sudo systemctl enable --now ssl-admin.service
curl -fsS http://127.0.0.1:8080/api/session
```

`/etc/ssl-admin/admin.env` 至少需要配置 `ADMIN_AUTH_CODE`、`ADMIN_DB_PATH`、`ADMIN_WEB_DIR`、`ALICLOUD_RAM_ROLE`、`SSL_CERTIFICATE_DIR`、`SSL_CERTIFICATE_MANIFEST` 和 `NGINX_SITE_DIR`。授权码只从该文件读取，不写入 SQLite。

### 首次使用

1. 在“域名管理”添加根域名。
2. 在“DNS 解析”选择根域名并同步 AliDNS。
3. 在“SSL 证书”选择已管理域名并保存申请。
4. 后台生成 `certificates.yaml` 并尝试立即启动续签服务。
5. 确认证书有效后，在“Nginx 站点”选择域名、上游和证书进行绑定。

选择 `example.com` 申请通配符证书时，会生成 `example.com` 和 `*.example.com` 两个 SAN。

### 每日续签

```bash
sudo systemctl enable --now ssl-auto-renew.timer
systemctl list-timers ssl-auto-renew.timer
sudo systemctl start ssl-auto-renew.service
sudo journalctl -u ssl-auto-renew.service -n 100 --no-pager
```

默认每天 `03:17` 检查，随机延迟最多 30 分钟；`Persistent=true` 会在停机错过后补执行。

### 证书路径和故障排查

Nginx 应引用稳定的 `current` 路径：

```text
/etc/ssl-auto-renew/certs/<name>/current/fullchain.pem
/etc/ssl-auto-renew/certs/<name>/current/privkey.pem
```

续签后 `current` 会切换到新版本，然后执行 `nginx -t` 和 reload。旧的 Certbot 配置如果仍引用 `/etc/letsencrypt/live/...`，需要切换到上述路径。

```bash
sudo systemctl status ssl-admin.service
sudo journalctl -u ssl-admin.service -n 100 --no-pager
sudo systemctl status ssl-auto-renew.service
sudo journalctl -u ssl-auto-renew.service -n 100 --no-pager
sudo nginx -t
sudo namei -l /etc/ssl-auto-renew/certs/example-com/current/fullchain.pem
dig TXT _acme-challenge.example.com
```

### Git 安全检查

提交前确认 `deploy.env`、`*.env`、数据库、`account.key`、私钥、证书、`web/dist` 和 `node_modules` 均被忽略：

```bash
git status --short --ignored
git diff --check
```

## 使用 AI 辅助安装

可以使用 Codex、ChatGPT 或其他代码助手协助完成构建和 ECS 部署。推荐让 AI 在项目目录中工作，并让它读取本地的忽略配置，而不是把云凭据复制到聊天窗口。

### 提供给 AI 的信息

可以提供以下非敏感信息：

- 项目所在目录。
- ECS 公网 IP 或域名、SSH 端口和登录用户名。
- 远端安装目录。
- ECS 操作系统和 CPU 架构，例如 Linux amd64。
- 管理后台公网域名。
- 已绑定的 ECS RAM 角色名称。
- 是否使用 Let's Encrypt production 环境。

密码、SSH 私钥、AccessKey Secret、STS Token、后台授权码、ACME 账号私钥和 SQLite 数据库不应粘贴到对话中。将这些内容放在本地 `deploy.env`、`admin.env` 或 ECS 上的受限环境文件中，并确保它们被 `.gitignore` 忽略。

### 推荐提示词

可以把下面的提示词交给 AI，并根据实际情况替换占位符：

```text
请在当前项目目录部署 ssl_auto_renew：

1. 先阅读 README.md、renew-service/README.md 和 web-admin/README.md。
2. 检查两个子项目的职责和目录隔离，不要合并它们的数据库或代码包。
3. 检查 .gitignore，确认 deploy.env、*.env、数据库、证书、私钥和构建产物不会提交。
4. 在本地分别执行 Go 测试、Vue 构建和 Linux amd64 构建。
5. 将 renew-service 安装为 ssl-auto-renew.service，并启用 ssl-auto-renew.timer。
6. 将 web-admin 安装为 ssl-admin.service，后台只监听 127.0.0.1。
7. 使用 ECS RAM 角色访问 AliDNS，不要写入或回显 AccessKey、Secret、STS Token 或授权码。
8. 发布后验证服务状态、日志、内部健康接口、证书状态、Nginx 配置和 timer。
9. 失败时保留旧版本，先说明原因，不要删除生产数据库、证书或环境文件。

部署连接信息在当前目录的 deploy.env 中，请读取变量但不要在输出中显示变量值。
ECS 地址：<ECS 地址>
SSH 用户：<SSH 用户>
SSH 端口：<SSH 端口>
RAM 角色：<RAM 角色名>
公网域名：<管理后台域名>
```

### AI 执行后的核验

让 AI 返回以下检查结果，但只返回状态、路径和错误摘要，不返回任何凭据：

```bash
systemctl is-active ssl-admin.service
systemctl is-enabled ssl-auto-renew.timer
systemctl is-active ssl-auto-renew.timer
systemctl list-timers ssl-auto-renew.timer
curl -fsS http://127.0.0.1:8080/api/session
sslctl status --config /etc/ssl-auto-renew/config.yaml
nginx -t
```

最后人工确认：管理后台可以登录、DNS 同步正常、证书状态有效、Nginx HTTPS 可访问，并且 `git status --short --ignored` 中没有出现敏感文件未被忽略的情况。
