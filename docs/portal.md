# 客户前台与商业部署

EasyRun 分为管理后台和可选客户前台。数据库升级保留现有账号、计划、区域、任务及记录；首次初始化把无归属的旧账号分配给新超级管理员。

## 入口与首次初始化

| 入口 | 用途 |
| --- | --- |
| `/` | 首次显示初始化向导；之后按登录身份进入工作空间 |
| `/admin` | 管理员登录和管理后台 |
| `/app` | 客户登录、自助注册和客户中心 |
| `/face-upload/ACCOUNT_ID#token=SECRET` | 专用人脸照片上传页，无需平台登录 |

首次访问填写超管邮箱、12–72 字节密码，并选择：

- **个人使用**：管理后台可用，客户前台、注册、计费关闭。
- **商业使用**：可选择启用客户前台；后台可独立关闭前台、自助注册、邮箱验证或计费。

关闭客户前台只关闭客户的登录 / 注册入口，不中断已安排的商业任务；停用某个用户才会阻止其后续任务执行。切换回个人模式则停止客户角色的任务执行。

没有预置超管密码。初始化通过数据库锁保证只执行一次；旧数据不会被清空。本机直连可直接初始化；远程初始化请先在环境变量中设置随机 `PORTAL_SETUP_TOKEN`，并在向导中填写。HTTPS 反向代理部署设置 `COOKIE_SECURE=true`。

## 权限与隔离

- 超级管理员：站点、邮件、钱包密钥、其他管理员及客户管理。
- 管理员：全站账号、任务、客户信息、充值码与归集流水；不读取或修改钱包 seed 和邮件密钥配置。
- 客户：只访问自己绑定的多个跑步账号、对应计划、区域、记录、任务、余额和套餐。

隔离在服务端 Store 层实施，既过滤列表，也校验按 ID 查询、更新、删除。登录会话使用 HttpOnly、SameSite Cookie；数据库仅存会话令牌摘要。密码使用 bcrypt；敏感配置、人脸照片与账号凭据使用现有 AES-GCM 主密钥加密。停用用户会撤销其平台会话，并阻止其后续任务执行。

## 邮件验证码

后台「客户与商业配置 → 站点与计费 → 邮件服务器」支持三种发信连接方式：

| 协议 | 常用端口 | 行为 |
| --- | --- | --- |
| SMTP + STARTTLS | 587 | 强制升级 TLS，升级失败就终止 |
| SMTPS | 465 | 从连接开始即使用 TLS |
| SMTP 中继 | 25 | 用于可信中继；不绕过库的明文认证限制 |

TLS 连接校验服务器证书；没有跳过证书校验的开关。IMAP、POP3 用于收信，不用于发送注册验证码。

验证码有效期 10 分钟、最多校验 5 次、使用后失效，并设置邮箱/IP 发送限流。邮件密码或服务商授权码加密存储，API 不返回明文。修改配置后先保存，再使用「测试已保存的配置」。未开启邮箱验证时允许直接注册，用户的邮箱验证状态保持未验证。

## 余额与计费

记账单位为整数 **微 USDT**，`1 USDT = 1,000,000 micro`，不使用浮点余额。

| 方式 | 规则 |
| --- | --- |
| 按次 | 每次成功提交按配置单价结算 |
| 按公里 | 按本次目标距离 × 公里单价计算，向上取整到 1 micro |
| 完整计划套餐 | 一次购买，绑定一个账号的一个计划；覆盖该组合后续任务，不覆盖其他账号或后续学期 |

客户在任务编辑中选择计费方式，也可在「余额与计费」调整。任务先预扣余额，成功提交结算，失败或跳过退回预扣。已购套餐优先。管理员任务不收费。计费默认关闭、价格默认 0，需超管按自身业务设置后开启；套餐购买不保证上游的考核判定结果。

每笔执行、退款、充值码兑换和链上充值都有唯一业务参考号，重复请求不会重复入账或扣费。预扣、余额、流水写入在同一数据库事务内完成。服务进程异常中止时，尚未确认结果的运行中预扣应先结合上游记录核对，勿直接当作重复充值或再次扣款。

### 充值码

管理员可批量生成 1–100 张，设置面额与有效天数，并下载完整码。完整码只在生成响应中显示一次，数据库只保存摘要和可识别前缀。兑换采用事务和原子状态更新，支持并发防重复兑换。

## USDT TRC20 自助充值

钱包默认关闭、默认指向 **Shasta 测试网**。测试网地址不接收真实 USDT；切换主网后合约固定为官方 USDT 合约 `TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t`。

1. 选择网络、HTTPS 节点及对应代币合约；主网合约固定。
2. 输入超管密码，创建 HD 钱包并立即保存一次性展示的 seed；也可导入 32–64 字节十六进制 seed。
3. 保存配置后启用充值，客户按需分配自己的专属收款地址。
4. 后台每分钟扫描固化交易；核对合约、收款地址、交易回执与 Transfer 事件后入账。每个 `(txID, eventIndex)` 仅入账一次。

HD 派生路径为 `m/44'/195'/0'/0/index`，索引由数据库序列分配。**已有地址后禁止修改网络、合约，seed 一经保存禁止覆盖**，避免已分配地址失去控制。客户端及节点均不接收私钥。扫描使用分页、15 分钟重叠窗口与每日全量对账；失败不推进扫描检查点。链上 API 使用全局约 3 QPS 的发送节奏，与地图 QPS 独立。

钱包属于部署侧托管热钱包：管理员必须分别离线备份 seed、`ACCOUNT_MASTER_KEY` 与最新数据库。不要把 seed、数据库连接串或任何 API key 放进 Git 仓库、日志或截图。账本余额和链上资金归集相互独立，归集不会扣减客户余额。

## Feee 能量与归集

后台配置 Feee API key、归集地址、最低余额、能量单价上限、链上手续费上限、Cron 与时区。主网流程：

1. 读取源地址真实代币余额并检查地址已激活。
2. 调用 Feee 估算能量和报价，报价超过所设上限就停止。
3. 提交一次能量订单，等待链上资源到账。
4. 构造 TRC20 转账；本地校验原始 protobuf 中的 owner、合约、收款地址、金额、权限、过期时间和手续费后签名。
5. **先持久化签名交易及 txID，再广播**。广播不明时只重播同一交易，不另签一笔。
6. 等待固化回执和对应 Transfer 事件成功后标记完成。

支持手动一键排队归集和 Cron 定时归集。首次开启定时归集只建立下一次排期，不立即转账；跨进程锁与按地址唯一活跃任务约束避免重复执行。测试网手动归集使用测试网已有资源，不调用主网 Feee 租赁。

> 能量不能代替地址激活与带宽。收款地址仍需要激活，以及足够 TRX / 带宽资源。Feee key 需要服务商要求的 IP 或 User-Agent 白名单和可用余额。能量订单结果不明、原交易过期但结果未确认等情况会保持「待人工核对」并阻止该地址自动另开归集。先在服务商和链上核对原订单/txID，再作账务处理。

真实 SMTP、主网充值、Feee 下单和主网转账，需要部署者填入自己的配置后做小额联调；代码验证过程不会替你启用主网或移动真实资金。

## 独立人脸上传页

后台和客户端都有「人脸上传页」。账号管理者可以直接上传，也可生成、轮换或撤销该账号的修改链接：

```text
/face-upload/ACCOUNT_ID#token=256_BIT_RANDOM_SECRET
```

- 链接使用独立的 256-bit 随机上传令牌，**不是账号登录 token**。
- 令牌放在 URL fragment 中，不进入服务器请求路径；页面再通过 Authorization header 提交。
- 仅允许修改这一账号的自定义照片，不提供账号登录、记录读取或底图下载权限。
- 链接默认有效 30 天，可设置 1–365 天；轮换和撤销立即让旧链接失效。
- 页面无需平台登录，所有持有完整链接的人都能修改照片，因此页面明确提示不要泄露。
- JPEG / PNG 最大 6 MB、1600 万像素；公开上传重编码去除 EXIF 等元数据，照片加密保存。

该页面不会读取摄像头、执行活体动作判断或替用户发起远端认证请求。

## Linux 本地人脸推理

CGO 原生加载器同时支持 Windows amd64 和 Linux amd64/arm64。Windows 使用 DLL；Linux 使用 `dlopen` 加载对应架构的 `libInspireFace.so`。识别优先调用 C API v2，也兼容 v1.2.3 的 Optional session API。模型包格式不改成 ONNX，继续使用现有 MNN 资源包。

Linux 示例：

```sh
sh scripts/setup-local-face.sh /opt/easyrun/models/libInspireFace.so /opt/easyrun/models/Megatron
export FACE_ENGINE_LIBRARY_PATH=/opt/easyrun/models/libInspireFace.so
export FACE_MODEL_PACK_PATH=/opt/easyrun/models/Megatron
export FACE_DETECT_PIXEL_SIZE=320
CGO_ENABLED=1 go build -o easyrun ./cmd/server
```

Docker 已使用 Debian/glibc 与 CGO，安装 `libstdc++6`、`libgomp1`，默认只读挂载 `./models:/app/models`。把对应架构运行库和模型包放入 `models`，设置容器内的两项绝对路径。模型与 SDK 二进制不提交到仓库。

**验证情况**：Windows 单元测试与数据库并发集成测试通过；WSL Linux amd64 全包测试通过，使用官方 v1.2.3 `.so` 与现有 Megatron 包通过真实同图推理测试。Linux arm64 有代码和构建条件支持，尚未在 arm64 硬件上验证。

## 测试

```powershell
go test ./...
go vet ./...
npm --prefix frontend run build
# 使用测试数据库连接串；测试自动创建并清理独立 schema，不修改业务数据。
$env:PORTAL_TEST_DATABASE_URL='postgres://USER:PASSWORD@HOST/TEST_DB?sslmode=disable'
go test -race ./internal/portal -count=1 -v
```

已覆盖初始化单次执行、旧数据归属、服务端隔离、跨站写入拒绝、上传令牌轮换、并发兑换、并发预扣 / 退款、套餐去重、链上充值事件去重、HD 派生、转账签名前校验和归集排期。

## 接口依据

- [Feee API 约定](https://feee.io/doc/en-US/api/intro/convenient.html)
- [Feee 能量估算](https://feee.io/doc/en-US/api/orderv2/estimate_energy.html)、[订单报价](https://feee.io/doc/en-US/api/orderv2/price.html)、[创建订单](https://feee.io/doc/en-US/api/orderv2/submit.html)
- [TRON TRC20 记录](https://developers.tron.network/docs/get-trc20-transaction-history)
- [TRON 构造、签名、广播与确认](https://developers.tron.network/docs/api-signature-and-broadcast-flow)
- [InspireFace 原生接口](https://github.com/HyperInspire/InspireFace/blob/master/cpp/inspireface/c_api/inspireface.h)
