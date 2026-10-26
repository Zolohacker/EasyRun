# 开发与测试

## 本地运行

准备 Go 1.23+、Node.js 22、PostgreSQL。前端产物嵌入 Go 程序。

```bash
cd frontend
npm ci
npm test
npm run build
cd ..
go test ./...
go vet ./...
```

Windows 本地脚本会初始化数据库、读取 `.env.local` 并以守护模式启动：

```powershell
.\scripts\start-local.ps1 -Rebuild
```

其他平台配置 `DATABASE_URL`，从项目目录运行：

```bash
go build -buildvcs=false -trimpath -o bin/easyrun ./cmd/server
./bin/easyrun
```

直接启动支持版本检查；在线安装需按 [更新文档](./updates.md#启动方式) 使用独立守护进程。

本地人脸推理构建需 `CGO_ENABLED=1` 和 C 编译器；Linux 运行库与系统依赖见 [部署文档](./portal.md)。地图 Key 在网页系统配置中填写，不放入环境变量。环境参数参考 [`.env.example`](../.env.example)。

## 数据库集成测试

未提供 `PORTAL_TEST_DATABASE_URL` 时，Go 测试会跳过相关数据库场景。完整验证需指向**隔离测试数据库**，不要使用正式数据库；测试账号需有创建与删除 schema 的权限，每个测试创建独立 schema 并在结束后清理。

```powershell
$env:PORTAL_TEST_DATABASE_URL = 'postgres://TEST_USER:TEST_PASSWORD@127.0.0.1:55439/TEST_DB?sslmode=disable'
go test ./... -count=1 -timeout 240s
go test -race ./internal/portal ./internal/automation ./internal/mapapi
```

充值测试使用模拟节点；通过这些测试不代表已完成主网资金验收。[测试网验收记录](./testnet-recharge-verification.md) 单独保留。

## 代码入口

| 目录 | 职责 |
| --- | --- |
| `cmd/server` | 启动和依赖装配 |
| `frontend/src` | 后台、客户前台与独立配置页 |
| `internal/httpapi` | HTTP API、静态前端 |
| `internal/automation` | 任务配置校验、调度和工作池 |
| `internal/portal` | 用户权限、充值、预扣账务与配置链接 |
| `internal/route` / `internal/mapapi` | 路线、地图限流和缓存 |
| `internal/store` | PostgreSQL 迁移与存储 |
| `internal/selfupdate` | 版本检查、安装、守护与回滚 |

API 参考见 [`frontend/public/openapi.json`](../frontend/public/openapi.json)，具体新增接口以服务端路由与集成测试为准。

## 提交与发布

- 保持提交聚焦，行为变更附测试；前端修改后重新构建内嵌资源。
- 不提交 `.env.local`、主密钥、钱包备份、日志、数据库、真实账号数据或截图；仓库随附模型遵守各自 [资源条款](../models/NOTICE.md)。
- 发布时同步 `internal/buildinfo/info.go` 和 `Dockerfile` 的版本、整理 `CHANGELOG.md`，推送稳定 `vX.Y.Z` 标签。
- 发布工作流在三平台构建与检查通过后上传完整发行包和 `checksums.txt`，发布说明使用更新日志中的对应版本章节。
