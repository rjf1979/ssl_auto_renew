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
