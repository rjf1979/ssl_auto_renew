# 域名 SSL 自动续签工具规划

文档版本：v0.1  
编写日期：2026-09-24  
目标运行环境：阿里云 ECS（Linux，systemd）

## 1. 项目目标

构建一个部署在阿里云 ECS 上的域名 SSL 证书管理工具，默认使用免费的 ACME 证书，完成以下闭环：

1. 为一个或多个域名申请 ACME 证书。
2. 在证书到期前自动续签，避免依赖人工登录服务器。
3. 将新证书以原子方式写入指定目录，并按配置自动 reload Nginx、Apache 或其他服务。
4. 支持阿里云 DNS API 完成 DNS-01 验证，覆盖通配符证书和不方便开放 80 端口的场景。
5. 记录每次申请、续签、部署和失败原因，支持命令行查看和健康检查。
6. 在失败时保留上一份可用证书，并提供告警出口。

本项目第一阶段定位为单台 ECS 上的可靠运维工具，不把它设计成多租户 SaaS，也不默认接管 ECS 上已有的 Nginx/Apache 配置。MVP 默认使用 Let's Encrypt 免费证书，不购买或依赖商业 CA 证书；证书通常有效期为 90 天，由工具在到期前自动续签。

## 2. 开源项目调研结论

本次调研对象为 Certbot、acme.sh、lego 和 dehydrated。GitHub 仓库元数据采集时间为 2026-09-24，数据会随仓库变化，仅用于当前选型参考。

| 项目 | 主要特点 | 可复用思路 | 对本项目的启示 |
|---|---|---|---|
| Certbot | Python 实现，支持 HTTP-01、webroot、Apache、Nginx 及大量插件 | 证书生命周期、插件化验证和部署器 | 功能完整，但完整复刻成本较高；不直接嵌入 |
| acme.sh | Shell 实现，安装后创建每日 cron，默认约每 30 天检查续签，支持 DNS API 与证书部署 | 轻量安装、续签检查、deploy hook、服务 reload | 目标流程成熟；需要吸收其权限、日志、reload 失败处理经验 |
| lego | Go ACME 客户端和库，支持 HTTP-01、DNS-01、TLS-ALPN-01、ARI 及大量 DNS 提供商，包含 Alibaba Cloud DNS | 作为 Go 库嵌入业务，复用 ACME 和 DNS provider 实现 | 推荐作为核心依赖，避免自行实现 ACME 协议 |
| dehydrated | 简单 Shell ACME 客户端，通过 hook 扩展验证和部署 | hook 生命周期、配置简单、可审计 | 适合参考接口设计，不作为核心依赖 |

推荐方案：使用 Go 编写业务层，使用 `go-acme/lego` 作为 ACME 引擎和阿里云 DNS provider。业务层只负责域名配置、调度、证书文件生命周期、服务 reload、日志和告警。

### 2.1 参考来源

- Certbot：https://github.com/certbot/certbot
- acme.sh：https://github.com/acmesh-official/acme.sh
- lego：https://github.com/go-acme/lego
- dehydrated：https://github.com/dehydrated-io/dehydrated
- lego DNS provider 文档：https://go-acme.github.io/lego/dns/alidns/
- Let's Encrypt 文档：https://letsencrypt.org/docs/

## 3. 用户与使用场景

### 3.1 目标用户

- 在阿里云 ECS 上运行 Nginx、Apache、Caddy 或自定义 HTTPS 服务的个人开发者。
- 需要管理多个域名或通配符域名的小型团队。
- 希望通过 DNS API 自动完成续签，同时不把阿里云主账号密钥放进脚本或仓库的运维人员。

### 3.2 典型流程

```text
安装程序
  -> 写入本地配置和受限权限的凭据文件
  -> 添加域名、验证方式和证书部署目标
  -> 首次申请并验证
  -> 写入版本化证书目录
  -> 执行部署动作和服务 reload
  -> systemd timer 定期检查
  -> 到期窗口内自动续签
  -> 记录结果并发送告警
```

## 4. MVP 范围

### 4.1 必须支持

- ACME CA：Let's Encrypt production 和 staging 两套目录。Let's Encrypt 作为 MVP 的免费证书来源，可配置其他兼容 ACME CA，但不纳入付费证书采购流程。
- 证书类型：单域名、多域名 SAN、通配符域名。
- 验证方式：
  - DNS-01：首选，支持阿里云 DNS API。
  - HTTP-01：支持 standalone 或 webroot，作为非通配符域名的备用方式。
- 证书密钥算法：ECDSA P-256 默认，可选 RSA 2048。
- 自动续签：每天检查一次，在证书剩余天数低于可配置阈值时续签，默认 30 天。
- 证书部署：写入独立目录，生成 `privkey.pem`、`cert.pem`、`fullchain.pem` 和元数据文件。
- 部署后动作：可配置 reload 命令，例如 `nginx -t && systemctl reload nginx`。
- 原子更新：新证书先写临时目录并校验，再切换 `current` 软链接；失败时不覆盖当前可用证书。
- CLI：添加、查看、测试、立即续签、撤销、删除和查看日志。
- systemd service/timer：开机可运行、定时执行、失败可通过 `systemctl` 查询。
- 日志：结构化日志写入 stdout/journald，敏感字段脱敏。
- 健康检查：返回服务运行状态、最近一次任务结果和证书剩余天数。

### 4.2 MVP 暂不支持

- 多 ECS 集群统一管理。
- 公网 SaaS 控制台和多用户权限系统。
- 自动修改 Nginx/Apache 主配置。
- 自动申请阿里云公网证书或调用阿里云负载均衡证书部署 API。
- 邮件、短信、钉钉、企业微信等所有告警渠道同时实现。
- 自动开放安全组端口或自动修改 DNS 委派。

## 5. 技术方案

### 5.1 技术栈

| 层次 | 选择 | 原因 |
|---|---|---|
| 语言 | Go 1.23+ | 适合 ECS 常驻服务，单二进制、低资源、部署简单 |
| ACME | `github.com/go-acme/lego/v5` | 复用成熟的 ACME、DNS-01、HTTP-01 和阿里云 DNS 支持 |
| 配置 | YAML 或 TOML | 便于人工审阅；密钥使用环境变量或独立 secrets 文件 |
| 状态 | SQLite | 单 ECS 场景足够，记录任务、证书和事件，不引入数据库服务 |
| 调度 | systemd timer | 利用 Linux 原生进程管理和日志能力，避免常驻调度器复杂化 |
| 日志 | Go `slog` | 标准库结构化日志，方便接入 journald |
| 测试 | Go test、Docker/临时 CA 集成测试 | 覆盖纯逻辑和真实 ACME 流程 |
| 打包 | 静态二进制 + install.sh + systemd 单元 | 适合 ECS 离线或低依赖部署 |

### 5.2 组件划分

```text
cmd/sslctl                  CLI 入口
internal/config             配置解析、校验和脱敏
internal/acme               lego 封装、账户和订单生命周期
internal/challenge          DNS-01、HTTP-01 challenge 配置
internal/certificate        证书解析、有效期检查、原子存储
internal/deployer            Nginx/Apache/通用命令部署器
internal/scheduler           任务编排和并发控制
internal/store               SQLite schema 和状态持久化
internal/notify              webhook/邮件等告警接口
internal/health              健康检查和运行状态
deploy/systemd               service、timer 和 tmpfiles 配置
```

### 5.3 数据模型

核心表建议如下：

- `accounts`：ACME CA、账户邮箱、账户密钥路径、状态。
- `certificates`：域名集合、CA、算法、验证方式、证书目录、到期时间、最近状态。
- `deployments`：证书对应的目标文件路径、reload 命令、最近部署结果。
- `runs`：每次申请/续签的开始时间、结束时间、结果、错误摘要和 request id。
- `events`：面向 CLI 和健康检查的审计事件。

私钥和 ACME account key 默认只落盘到受限目录，不直接写进 SQLite。SQLite 只保存路径、指纹、状态和时间信息。

## 6. 配置设计草案

```yaml
ca:
  directory_url: https://acme-v02.api.letsencrypt.org/directory
  email: admin@example.com
  account_key: /etc/ssl-auto-renew/secrets/account.key

storage:
  data_dir: /var/lib/ssl-auto-renew
  certificate_dir: /etc/ssl-auto-renew/certs

certificates:
  - name: example-com
    domains:
      - example.com
      - www.example.com
      - '*.example.com'
    challenge: dns-01
    dns_provider: alidns
    key_type: ecdsa
    renew_before_days: 30
    deploy:
      fullchain_file: /etc/nginx/ssl/example.com/fullchain.pem
      key_file: /etc/nginx/ssl/example.com/privkey.pem
      reload_command: nginx -t && systemctl reload nginx

notifications:
  webhook_url_env: SSL_RENEW_WEBHOOK_URL
```

阿里云 AccessKey 不写入该 YAML。推荐使用最小权限 RAM 子账号，并通过 systemd `EnvironmentFile`、受限权限文件或实例 RAM 角色提供凭据。若运行环境支持实例 RAM 角色，优先使用短期凭据。

## 7. 关键流程与可靠性要求

### 7.1 首次申请

1. 校验域名格式、重复域名、通配符与 challenge 的兼容性。
2. 校验 CA、邮箱、DNS provider 和凭据是否完整。
3. 申请前执行 DNS 权限探测；不输出密钥内容。
4. 通过 lego 创建订单并等待 challenge 完成。
5. 校验证书域名、签发者和有效期。
6. 写入新版本目录，设置目录权限，再更新部署目标。
7. 执行 reload 命令；reload 失败时保留旧证书并将任务标记为失败。

### 7.2 自动续签

- `systemd timer` 每日触发一次检查。
- 证书距离到期日大于阈值时跳过申请，仅记录检查结果。
- 同一证书使用文件锁或 SQLite 锁避免并发续签。
- 续签成功后先验证新证书，再部署和 reload。
- 失败采用有限重试和指数退避，避免频繁触发 CA 或 DNS API 限流。
- 连续失败或剩余有效期低于告警阈值时发送告警。
- 尊重 ACME Renewal Information（ARI，若 CA/lego 版本支持），否则使用配置的剩余天数规则。

### 7.3 原子部署

```text
certs/example-com/
  versions/20260924T120000Z/{privkey.pem,cert.pem,fullchain.pem,metadata.json}
  current -> versions/20260924T120000Z
```

证书必须写入临时文件后 `fsync`、设置权限并校验，再通过重命名或软链接切换。保留最近 2 至 3 个成功版本，便于回滚和排错；清理任务不得删除当前版本。

## 8. 阿里云 ECS 部署设计

### 8.1 系统要求

- Linux 发行版：Alibaba Cloud Linux 3、Ubuntu 22.04+ 或同等 systemd 环境。
- ECS 至少 1 vCPU、512 MB 内存；生产建议 1 vCPU、1 GB 以上。
- 仅 DNS-01 时不要求工具监听 80 端口；HTTP-01 standalone 需要临时占用 80 端口。
- 出站允许 HTTPS 访问 ACME CA 和阿里云 DNS API。
- 证书部署目录由运行用户可写，服务私钥目录仅 root 或指定服务用户可读。

### 8.2 文件布局

```text
/usr/local/bin/sslctl
/etc/ssl-auto-renew/config.yaml
/etc/ssl-auto-renew/secrets/
/var/lib/ssl-auto-renew/
/etc/ssl-auto-renew/certs/
/etc/systemd/system/ssl-auto-renew.service
/etc/systemd/system/ssl-auto-renew.timer
```

建议以专用系统用户运行。若 reload 命令必须使用 root 权限，应通过最小化的 `sudoers` 规则授权固定命令，而不是允许任意 shell 命令。

### 8.3 发布流程

1. 本地执行格式化、单元测试、静态检查和构建。
2. 生成版本化二进制和校验摘要。
3. 上传到 ECS 的临时目录。
4. 校验摘要后安装到新版本目录。
5. 更新 systemd 单元并执行 `systemctl daemon-reload`。
6. 运行 `sslctl config validate` 和 staging CA 测试。
7. 启用 timer，执行一次 dry-run/检查任务。
8. 验证 journald、证书有效期、目标服务 reload 和 HTTPS 响应。

生产发布脚本必须排除 `.env`、私钥、SQLite 运行数据、证书目录和用户上传目录，不能使用会删除远端持久化数据的同步参数。

## 9. 安全设计

- 阿里云 RAM 子账号只授予目标 DNS Zone 的最小读写权限，不使用主账号 AccessKey。
- AccessKey、webhook token、ACME account key 不写入 Git、日志、错误消息或 README。
- 配置和 secrets 文件权限默认 `0600`，证书私钥目录默认 `0700`。
- 日志对域名可以保留，对 Authorization、AccessKey、token、私钥内容必须脱敏。
- 所有外部命令使用参数数组执行，禁止把未经校验的用户配置拼接成 shell 脚本。
- reload 命令采用白名单部署器或固定模板，避免任意命令执行风险。
- 默认开启 Let's Encrypt staging 配置用于验收，确认通过后再切 production，避免触发速率限制。
- 申请和续签操作需要审计记录，证书撤销提供显式二次确认参数。

## 10. 可观测性与告警

最小日志字段：`timestamp`、`level`、`certificate`、`domains_hash`、`operation`、`duration_ms`、`result`、`error_code`、`request_id`。

MVP 建议支持一个通用 webhook；后续再增加邮件、钉钉和企业微信适配器。至少对以下事件告警：

- 首次申请失败。
- 续签失败。
- reload 失败。
- 证书剩余天数低于 14 天且尚未部署成功。
- DNS API 凭据无权限或配额异常。
- 连续失败达到配置次数。

## 11. 验收标准

### 11.1 功能验收

- 使用 staging CA 成功申请 `example.com` 和 `*.example.com` 测试证书。
- 阿里云 DNS-01 TXT 记录能够自动创建、等待传播并清理。
- 重复执行检查不会在证书未到续签窗口时重复申请。
- 模拟续签成功时，证书目录完成原子切换并 reload Nginx。
- reload 命令失败时，当前软链接仍指向旧版本，任务返回失败且有告警。
- `sslctl status` 能显示证书到期时间、最近任务和错误摘要。
- systemd timer 重启 ECS 后仍能按计划运行。
- HTTP-01 webroot 模式能够完成非通配符域名申请。

### 11.2 安全验收

- 源码、构建产物和日志中不存在 AccessKey、私钥和 webhook token。
- 非授权用户无法读取证书私钥和 SQLite 状态库。
- 配置中的 reload 命令不能执行任意额外参数或命令替换。
- staging 与 production CA 配置明确可见，避免误用生产额度。

### 11.3 故障验收

- DNS API 不可用时能重试、记录明确错误并保留旧证书。
- CA 返回 rate limit 时不会立即高频重试。
- 网络中断后下次 timer 能继续工作。
- 进程被杀死或机器重启后不会留下永久锁。
- 磁盘空间不足时不会截断当前证书文件。

## 12. 开发顺序

### Phase 0：设计和验证

- 固化配置格式、目录权限、状态模型和错误码。
- 用 lego 完成阿里云 DNS-01 的最小验证程序。
- 用 staging CA 验证 ECS 网络、RAM 权限和 DNS 传播时间。

### Phase 1：MVP 核心

- 实现 CLI、配置校验、SQLite 状态和证书版本目录。
- 实现首次申请、续签判断、DNS-01、原子部署和 reload。
- 提供 systemd service/timer、日志和基础 webhook。

### Phase 2：可靠性和发布

- 加入锁、重试、退避、回滚、磁盘检查和故障注入测试。
- 增加 HTTP-01 webroot/standalone、RSA 证书和通用部署器。
- 制作 ECS 安装脚本、升级脚本和验收脚本。

### Phase 3：管理体验

- 增加本机只监听 `127.0.0.1` 的 REST API 和只读状态页面。
- 增加证书到期概览、运行历史、配置导入导出和权限分级。
- 增加钉钉/企业微信/邮件告警适配器。

### Phase 4：规模化能力

- 多 ECS agent 或中心服务模式。
- 阿里云 SLB、API 网关、CDN 等证书部署目标。
- 多账户、多 CA、集中审计和高可用状态存储。

## 13. 主要风险与应对

| 风险 | 影响 | 应对 |
|---|---|---|
| DNS 传播慢或 TXT 记录缓存 | 首次申请/续签超时 | 可配置等待和轮询；提供传播诊断；优先 staging 验证 |
| 阿里云 RAM 权限过大 | 凭据泄露后影响扩大 | 使用最小权限和实例 RAM 角色；凭据不进配置仓库 |
| reload 失败 | 新证书已生成但服务仍用旧证书 | 先校验配置再 reload；保留旧版本；告警并支持回滚 |
| 多任务并发 | 触发 CA/DNS 限流或覆盖文件 | 每证书锁 + 全局并发上限 + 退避 |
| 证书私钥误读 | 严重安全事件 | 专用用户、目录权限、日志脱敏、权限测试 |
| CA 速率限制 | 续签延迟 | staging 验收、仅到期窗口申请、尊重 Retry-After/ARI |
| 服务器磁盘满 | 证书写入失败或日志异常 | 写入前空间检查、版本保留策略、日志轮转 |

## 14. 首个实现前需要确认的产品决策

以下默认值可直接用于 MVP：

- 默认 CA：Let's Encrypt production 免费证书，安装和验收阶段显式切换 staging。
- 默认验证：阿里云 DNS-01。
- 默认密钥：ECDSA P-256。
- 默认续签窗口：剩余 30 天。
- 默认运行方式：专用系统用户 + systemd timer。
- 默认状态存储：SQLite。
- 默认告警：通用 webhook。
- 默认管理入口：CLI；Web 页面放到 Phase 3。

如果产品更偏向非技术用户，下一步应优先做本机 Web 管理界面；如果产品主要服务运维人员，应先完成 CLI、systemd 和故障恢复能力。
