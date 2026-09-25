# SSL Admin

与 `renew-service` 平级的独立 Vue 3 + Vite / Go 管理后台，负责管理 AliDNS 解析和 Nginx 站点绑定。

证书申请、续签和证书文件落盘由自动续签项目的 `sslctl` 负责，本项目不复制 ACME 逻辑。

两个项目通过已安装的 `sslctl`、配置文件和证书目录协作，不共享 Go 内部包、SQLite 数据库或 systemd 服务。

## 本地运行

```powershell
cd web
npm install --registry=https://registry.npmmirror.com
npm run build
cd ..
$env:ADMIN_AUTH_CODE="replace-with-local-code"
$env:ADMIN_DB_PATH="./data/admin.db"
$env:ADMIN_WEB_DIR="./web/dist"
go run ./cmd/ssl-admin
```

开发前端使用 Vite dev server，API 代理到 `127.0.0.1:8080`。

## 生产目录

- 程序：`/opt/ssl-admin/ssl-admin`
- 前端：`/opt/ssl-admin/web/dist`
- 配置：`/etc/ssl-admin/admin.env`
- SQLite：`/var/lib/ssl-admin/admin.db`
- 服务：`ssl-admin.service`

后台的“域名管理”维护多个 AliDNS 根域名。DNS 记录会保存所属根域名，并按当前选择的域名单独同步；历史 `ALI_DNS_DOMAIN` 会在首次升级时自动迁移。

“申请证书”会把证书申请记录写入 SQLite，并生成 `/etc/ssl-auto-renew/certificates.yaml`。`renew-service` 读取该清单执行 ACME 申请和自动续签，后台不直接执行 ACME。

授权码只从 `admin.env` 读取，不写入 SQLite。生产环境应使用随机长字符串，并将配置文件权限限制为 `0640`。

## 完整安装

构建前端和 Go 后台：

```bash
cd web
npm install --registry=https://registry.npmmirror.com
npm run build
cd ..
go mod tidy
go test ./...
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/ssl-admin-linux-amd64 ./cmd/ssl-admin
```

安装到 ECS：

```bash
sudo install -d -m 0750 /opt/ssl-admin/web /etc/ssl-admin /var/lib/ssl-admin
sudo install -m 0755 bin/ssl-admin-linux-amd64 /opt/ssl-admin/ssl-admin
sudo cp -a web/dist/. /opt/ssl-admin/web/dist/
sudo install -m 0644 deploy/ssl-admin.service /etc/systemd/system/ssl-admin.service
sudo chmod 0640 /etc/ssl-admin/admin.env
sudo systemctl daemon-reload
sudo systemctl enable --now ssl-admin.service
```

生产环境默认文件：

```text
/opt/ssl-admin/ssl-admin
/opt/ssl-admin/web/dist
/etc/ssl-admin/admin.env
/var/lib/ssl-admin/admin.db
/etc/ssl-auto-renew/certificates.yaml
/etc/nginx/conf.d/
```

## 关键配置

```dotenv
ADMIN_AUTH_CODE=replace-with-a-long-random-code
ADMIN_ADDR=127.0.0.1:8080
ADMIN_DB_PATH=/var/lib/ssl-admin/admin.db
ADMIN_WEB_DIR=/opt/ssl-admin/web/dist
ALI_DNS_DOMAIN=example.com
ALI_DNS_DOMAINS=
ALICLOUD_RAM_ROLE=your-ecs-role
ALICLOUD_REGION_ID=cn-hangzhou
SSL_CERTIFICATE_DIR=/etc/ssl-auto-renew/certs
SSL_CERTIFICATE_MANIFEST=/etc/ssl-auto-renew/certificates.yaml
RENEW_TRIGGER_ENABLED=true
NGINX_SITE_DIR=/etc/nginx/conf.d
```

`ADMIN_AUTH_CODE` 只从环境文件读取。`ALI_DNS_DOMAINS` 是逗号分隔的初始化根域名列表；后续域名可以直接在后台添加。`RENEW_TRIGGER_ENABLED=true` 会在保存证书申请后立即启动续签服务。

## 使用顺序

1. 在“域名管理”添加根域名。
2. 在“DNS 解析”选择根域名并点击同步。
3. 创建或删除 AliDNS 记录。
4. 在“SSL 证书”选择已管理域名并保存申请。
5. 后台写入 SQLite、生成 `certificates.yaml` 并触发 `ssl-auto-renew.service`。
6. 在证书页面刷新，确认签发完成。
7. 在“Nginx 站点”选择域名、上游和证书，保存绑定。

选择 `example.com` 后，后台会为证书自动生成 `example.com` 和 `*.example.com`。

## Nginx 与证书

站点绑定读取：

```text
/etc/ssl-auto-renew/certs/<certificate-name>/current/fullchain.pem
/etc/ssl-auto-renew/certs/<certificate-name>/current/privkey.pem
```

保存站点前执行 `nginx -t`，成功后 reload Nginx。旧的 Certbot 配置如果仍引用 `/etc/letsencrypt/live/...`，需要切换到上述 `current` 路径。

## 健康检查和排障

```bash
curl -fsS http://127.0.0.1:8080/api/session
sudo systemctl status ssl-admin.service
sudo journalctl -u ssl-admin.service -n 100 --no-pager
sudo journalctl -u ssl-auto-renew.service -n 100 --no-pager
sudo nginx -t
```

申请失败时，重点检查：域名是否已在域名管理中维护、ECS RAM 角色权限、共享清单权限、`ssl-auto-renew.service` 是否存在，以及 DNS TXT 是否已传播。修改授权码后执行：

```bash
sudo systemctl restart ssl-admin.service
```

不要提交 `admin.env`、`deploy.env`、SQLite 数据库、证书、私钥或云凭据。
