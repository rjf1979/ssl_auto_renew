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
