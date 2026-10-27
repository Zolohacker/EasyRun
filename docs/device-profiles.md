# 随机设备资料

添加账号时，机型与 Android 版本按已核对组合随机选择。不是把机型列表和 Android 版本列表各抽一次。

## 行为

- 首次打开添加弹窗生成一组；“换一组”同时更换机型和对应系统版本，保留账号、密码与手动 Android ID。
- 密码登录、短信验证码确认、入库和后续请求使用同一组已保存信息。既有账号不自动更换设备。
- API 未填写设备信息时，服务端使用同一份资料库补齐。只填写机型时，从该机型已核对的系统版本中选择；只填写系统版本时，只选择对应机型。
- 高级选项仍支持手动填写真实设备信息。未知机型需要同时填写 Android 版本，不自动拼接一个通用版本。
- 随机池按当前日期筛选近四年机型。发布信息精确到月份时，保守按该月 1 日计算；资料全部过期后提示手动填写，不回退到老机型。

## 资料与核对

核对日期：**2026-10-27**。首批 **7 个品牌、28 个国行型号、33 个机型/Android 组合**，不是近四年全部手机或全部 OTA 版本的穷举。

- 型号代码与产品名称逐项核对 [Google Play 官方设备清单](https://support.google.com/googleplay/answer/1727131)。大陆版、国际版及 Pro/Ultra/E 版本分开处理。
- Android 版本优先采用厂商公开参数；补充版本采用 [H3C 实机兼容性报告](https://www.h3c.com/en/Support/Resource_Center/EN/Home/Public/00-Public/Technical_Documents/Product_Literature/Endpoints_Test_Reports/H3C_WLAN_Wi_Fi_7_Endpoints-25089/202510/2670476_294551_0.htm) 中明确测试的系统，不推测连续升级区间。
- `system_version` 保存 Android 大版本，不填 MIUI、HyperOS、ColorOS、OriginOS 或 One UI 的版本号，也不填 Android API Level。
- 这批 iQOO 型号在设备清单中归为 Vivo，显示品牌仍为 iQOO；协议型号前缀沿用对应的 `vivo`。
- 资料库随前端和 Go 服务一起构建；添加账号时没有额外联网抓取。

| 品牌 | 产品 | 型号代码 | 发布月份 | 随机 Android 版本 |
| --- | --- | --- | --- | --- |
| 小米 | 小米 13 | 2211133C | 2022-12 | 13、14 |
| 小米 | 小米 14 | 23127PN0CC | 2023-10 | 14 |
| 小米 | 小米 15 | 24129PN74C | 2024-10 | 15 |
| 小米 | 小米 15 Ultra | 25010PN30C | 2025-02 | 15 |
| 红米 | Redmi K60 Pro | 22127RK46C | 2022-12 | 14 |
| 红米 | Redmi K70 | 23113RKC6C | 2023-11 | 14 |
| 红米 | REDMI K80 | 24117RK2CC | 2024-11 | 15 |
| 红米 | REDMI K80 至尊版 | 25060RK16C | 2025-06 | 15 |
| 三星 | Galaxy S23 | SM-S9110 | 2023-02 | 13 |
| 三星 | Galaxy S24 | SM-S9210 | 2024-01 | 14 |
| 三星 | Galaxy S25 | SM-S9310 | 2025-02 | 15 |
| 三星 | Galaxy S26 | SM-S9420 | 2026-03 | 16 |
| OPPO | Find X6 | PGFM10 | 2023-03 | 13、14 |
| OPPO | Find X7 | PHZ110 | 2024-01 | 14 |
| OPPO | Find X8 | PKB110 | 2024-10 | 15 |
| OPPO | Find X8 Ultra | PKJ110 | 2025-04 | 15 |
| vivo | X90 | V2241A | 2022-11 | 13、14 |
| vivo | X100 | V2309A | 2023-11 | 14 |
| vivo | X200 | V2415A | 2024-10 | 15 |
| vivo | X200 Ultra | V2454A | 2025-04 | 15 |
| 一加 | 一加 11 | PHB110 | 2023-01 | 13、14 |
| 一加 | 一加 12 | PJD110 | 2023-12 | 14 |
| 一加 | 一加 13 | PJZ110 | 2024-10 | 15 |
| 一加 | 一加 13T | PKX110 | 2025-04 | 15 |
| iQOO | iQOO 11 | V2243A | 2022-12 | 13、14 |
| iQOO | iQOO 12 | V2307A | 2023-11 | 14 |
| iQOO | iQOO 13 | V2408A | 2024-10 | 15 |
| iQOO | iQOO Z10 Turbo Pro | V2453A | 2025-04 | 15 |

## 维护

前后端共用 [资料库](../internal/deviceprofile/catalog.json)，每个型号、发布月份和每个 Android 版本分别记录来源引用。新增组合先补齐证据，再更新核对日期。

[核对脚本](../scripts/verify-device-catalog.py) 可重新读取 Google 官方 CSV，逐个比对型号、认证产品名称和品牌。它不读取私有配置，不使用账号 Token。

```powershell
python scripts/verify-device-catalog.py
```

脚本验证的是公开型号对应关系；Android 版本仍需核对资料库中对应的厂商参数页或实机报告。单元测试另外检查：来源完整性、四年窗口、组合有效性、短信流程稳定性及既有设备保持不变。
