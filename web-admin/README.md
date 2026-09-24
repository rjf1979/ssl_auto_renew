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

授权码只从 `admin.env` 读取，不写入 SQLite。生产环境应使用随机长字符串，并将配置文件权限限制为 `0640`。
