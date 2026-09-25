# SSL Auto Renew

面向阿里云 ECS 的免费 ACME SSL 证书自动续签工具。

当前实现以 MVP 核心为目标：Go CLI、Let's Encrypt、阿里云 DNS-01、版本化证书目录和 systemd timer。

需要 Go 1.23+。复制 `config.example.yaml` 后修改域名和路径：

```bash
go mod tidy
go test ./...
go run ./cmd/sslctl config validate -config ./config.example.yaml
```

将编译后的 `sslctl` 安装到 `/usr/local/bin/`，将配置放到 `/etc/ssl-auto-renew/config.yaml`，再安装 `deploy/systemd/` 中的 service 和 timer。首次使用 Let's Encrypt staging 验证阿里云 DNS RAM 权限，确认通过后切换 production。

配置中的 `certificate_manifest` 可指向 Web 管理后台生成的证书清单。`sslctl` 每次执行 `validate`、`status` 或 `renew` 都会重新读取该清单，因此后台新增的证书会自动进入后续续签任务。

阿里云凭据由 lego 使用环境变量读取，不要写进配置文件、代码或 Git。生产环境应使用最小权限 RAM 子账号或 ECS RAM 角色。

## 生产安装

需要 Go 1.25+。构建 Linux ECS 版本：

```bash
go mod tidy
go test ./...
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/sslctl-linux-amd64 ./cmd/sslctl
```

上传后安装：

```bash
sudo install -d -m 0750 /etc/ssl-auto-renew/secrets
sudo install -d -m 0750 /etc/ssl-auto-renew/certs /var/lib/ssl-auto-renew
sudo install -m 0755 sslctl-linux-amd64 /usr/local/bin/sslctl
sudo install -m 0644 config.yaml /etc/ssl-auto-renew/config.yaml
sudo install -m 0644 deploy/systemd/ssl-auto-renew.service /etc/systemd/system/ssl-auto-renew.service
sudo install -m 0644 deploy/systemd/ssl-auto-renew.timer /etc/systemd/system/ssl-auto-renew.timer
sudo systemctl daemon-reload
```

生产 `config.yaml` 应使用 `https://acme-v02.api.letsencrypt.org/directory`，并设置：

```yaml
certificate_manifest: /etc/ssl-auto-renew/certificates.yaml
```

`certificates.yaml` 由 web-admin 生成。启用 manifest 后，每次 `validate`、`status` 或 `renew` 都会重新读取它。

## ECS RAM 角色

systemd 服务通过 `ALICLOUD_RAM_ROLE` 使用 ECS 绑定的 RAM 角色：

```ini
Environment=ALICLOUD_RAM_ROLE=your-ecs-role
```

角色需要允许 AliDNS 查询托管域名、创建 TXT 记录和删除 TXT 记录。不要把长期 AccessKey、Secret 或安全令牌写入配置文件。

## 手工运行和定时

```bash
sudo /usr/local/bin/sslctl config validate --config /etc/ssl-auto-renew/config.yaml
sudo /usr/local/bin/sslctl status --config /etc/ssl-auto-renew/config.yaml
sudo systemctl start ssl-auto-renew.service
sudo systemctl enable --now ssl-auto-renew.timer
systemctl list-timers ssl-auto-renew.timer
sudo journalctl -u ssl-auto-renew.service -n 100 --no-pager
```

默认每天 `03:17` 检查，随机延迟最多 30 分钟；`Persistent=true` 会在停机错过后补执行。续签默认只处理剩余有效期小于 `renew_before_days` 的证书。

## 证书部署

证书按版本保存：

```text
/etc/ssl-auto-renew/certs/<name>/versions/<timestamp>/
/etc/ssl-auto-renew/certs/<name>/current -> versions/<timestamp>
```

Nginx 应引用 `current/fullchain.pem` 和 `current/privkey.pem`。部署失败会尝试恢复旧链接和旧证书文件，并将错误写入 `state.json`。

## 排障

```bash
sudo systemctl status ssl-auto-renew.service
sudo journalctl -u ssl-auto-renew.service -n 100 --no-pager
sudo nginx -t
sudo namei -l /etc/ssl-auto-renew/certs/example-com/current/fullchain.pem
dig TXT _acme-challenge.example.com
```

常见原因包括 RAM 角色未绑定、AliDNS 权限不足、TXT 尚未传播、清单不可读、Nginx 仍引用旧的 `/etc/letsencrypt/live/` 路径或私钥权限不正确。
