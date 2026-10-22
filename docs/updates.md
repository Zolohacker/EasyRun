# 客户端版本门禁与平台更新

## 两套版本相互独立

管理后台 **运行总览** 的版本卡片分别展示：

1. **闪动校园客户端版本**：检查当前已适配版本是否仍被服务端接受。
2. **EasyRun 平台版本**：从固定仓库 `Zolohacker/EasyRun` 的稳定 GitHub Release 检查及安装平台程序。

客户端发布新 APK 不会让平台自动获得新协议适配。强更后需要发布已适配的平台版本，重新检查通过后才恢复跑步任务；没有“忽略强更”的开关。

## 客户端检查更新

- 使用已适配 Android `8.7.9` / Build `26101910` 的 `POST /run-front/common/update`，复用协议客户端公共参数；不使用任何客户的登录凭据。
- 接口来源：客户端 `BaseApi.l()`、`CommonApi.v()`，返回模型 `VersionUpgrade` 与 `VersionUpgradeDialog`。
- 读取 `haveUpdate`、`forceUpdate`、`newVersion`、`supportVersion`、更新标题及说明。
- 真实无更新响应会省略 `forceUpdate`、版本号为空。只有明确 `haveUpdate=false` 时才把省略的强更标志解释为 false，并展示“当前渠道可用版本”为当前适配版本；不宣称其他渠道版本已验证。
- 启动及每 **5 分钟**检查，可在总览手动检查（每分钟一次）。结果持久化，重启后仍保留强更状态。
- 首次未成功核实、响应格式异常且没有有效缓存、缓存超过 **10 分钟**时，服务端关闭执行门禁。短暂网络失败保留已核实结果；错误不会清除已经确认的强更标记。
- `forceUpdate=true`：拒绝手动触发和定时执行，队列中任务在执行前跳过并写明原因；已经执行的跑步任务会收到取消信号，停止后续请求。定时到期的受阻任务记录为跳过，恢复后不集中补跑。
- 门禁在调度器和最终提交入口双重检查，不依赖浏览器按钮。强更检查不会阻止后台查看账号、记录、修复配置或更新平台。

## 平台自动更新

设计参考 [sub2api 的更新服务](https://github.com/Wei-Shaw/sub2api/blob/main/backend/internal/service/update_service.go)，本项目独立实现，并增加 Windows 独立守护进程、ZIP 安装和启动身份验收。

- 每 **20 分钟**检查稳定 Release，跳过草稿与预发布；提供手动检查、一键更新及上一版回滚。
- **闲时自动安装默认关闭**，超管可在版本卡片设置中开启。必须没有正在执行及排队的任务，钱包后台事务也须空闲；准备更新时暂停新任务和归集，已持久化的充值检查保留至重启恢复。更新不会为安装而中断正在运行的任务或钱包事务。
- 固定来源、固定 OS/架构安装包名、GitHub 下载域名白名单，跨域重定向移除 Token。
- 必须有匹配的 `checksums.txt` 与 SHA-256，禁止路径穿越、链接、重复程序及超限压缩包。
- 先运行候选程序 `--version-json` 验证版本、系统、架构与本地推理支持，探测进程不继承数据库、主密钥及钱包凭据。
- 独立守护进程在服务器退出后替换程序，保留上一版备份；新版本在 **60 秒**内通过本机数据库健康检查、版本/提交号、随机启动标识的共同验收后才放行任务。失败自动恢复原二进制。
- 同一失败发行版不会被闲时自动安装无限重试；主动回滚后也不会立即自动重装刚撤回的版本，超管可手动重试或等待后续新版本。只有上一份完整二进制备份可回滚。
- 更新只替换平台二进制与内嵌前端，不替换 `.env.local`、数据库、钱包、模型及资源文件；外部 DLL/SO、模型文件的更新需单独部署。

### 私有仓库

在 **平台更新设置** 中填写仅限本仓库、`Contents: Read` 权限的 Fine-grained Token；Token 在数据库 AES-GCM 加密存储，不回显、也不复用开发机 `gh` 的登录 Token。公开仓库可留空。权限要求见 [GitHub Releases API](https://docs.github.com/en/rest/releases/releases#get-the-latest-release)。

没有 Release 或 Token 无权限时会明确显示“更新源待就绪”，而不是错误地显示已经是最新版。安装、回滚和修改设置仅超级管理员可操作，并需再次验证密码。

### 启动方式

**Windows 本地**：

```powershell
.\scripts\start-local.ps1
```

脚本复制 `bin/easyrun.exe` 为独立的 `bin/easyrun-supervisor.exe`，后者使用 `--supervise` 启动真正的服务器。直接运行服务器仍可检查版本，但在线安装按钮保持禁用并提示使用守护模式。

**Linux 二进制部署**：在程序停止时准备独立守护副本：

```bash
cp /opt/easyrun/bin/easyrun /opt/easyrun/bin/easyrun-supervisor
chmod 755 /opt/easyrun/bin/easyrun{,-supervisor}
# 在原有配置/模型路径对应的工作目录中启动，环境保持不变：
/opt/easyrun/bin/easyrun-supervisor --supervise /opt/easyrun/bin/easyrun
```

systemd 使用独立守护副本作为 `ExecStart`，设置正确的 `WorkingDirectory`、原有 `EnvironmentFile`，建议 `Restart=always`。运行用户需能写程序目录与其下 `.easyrun-update`。健康检查要求 HTTP 绑定回环地址或通配地址，例如 `HTTP_ADDR=:8080`。

**容器部署**：只提供版本检查，使用拉取新镜像并重新部署的标准流程；不在运行容器内替换镜像中的程序。

### 发布发行包

维护者推送稳定标签（例如 `v0.2.0`）触发 `.github/workflows/release.yml`：

| 系统 | 安装包 |
| --- | --- |
| Windows amd64 | `easyrun_v0.2.0_windows_amd64.zip` |
| Linux amd64 | `easyrun_v0.2.0_linux_amd64.tar.gz` |
| Linux arm64 | `easyrun_v0.2.0_linux_arm64.tar.gz` |

各包根目录仅放 `easyrun.exe` 或 `easyrun`，都启用 CGO，保留已有本地推理动态加载能力；另附统一的 `checksums.txt`。工作流通过全部构建后先创建草稿、上传完整资产再发布，已有 Release 不覆盖。首次上线此功能时尚无正式 Release，完整远端发布/安装链路需在维护者发布后验收。

### 回滚边界与测试

二进制回滚不是数据库时间旅行。发行迁移应向后兼容，安装大版本前仍需备份数据库、主密钥和钱包；若新版本已经执行不兼容迁移，应按发行说明恢复数据库备份。

自动化测试覆盖强更取消/拒绝、缓存过期、重启持久化、调度跳过、权限与 Token 加密、校验文件与压缩包拒绝场景、Windows 真实进程替换与失败自动回滚。测试使用临时目录、独立数据库 schema 和模拟发行包，不触碰真实客户或链上资产。
