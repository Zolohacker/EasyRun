# 随附本地人脸推理资源

克隆仓库或解压完整 Release 后，这些资源已经就位，启动自动发现：

```text
models/
├── Megatron                         # 当前使用的 MNN 资源包
├── windows-amd64/libInspireFace.dll  # Windows x64 SDK v1.2.4
├── linux-amd64/libInspireFace.so     # Linux x86_64 SDK v1.2.3
├── linux-arm64/libInspireFace.so     # Linux aarch64 SDK v1.2.3
└── SHA256SUMS
```

无需从 APK 提取、上传模型或手动填写环境变量路径。资源以普通 Git 文件保存，不依赖 Git LFS；模型约 59 MiB，首次克隆下载较大。

程序优先检查工作目录 `models`，其次检查可执行文件旁边的 `models` 及其上一级。只有自定义资源布局才需要高级路径覆盖配置。模型不是 ONNX 文件，不应改名后交给 ONNX Runtime。

源码编译开启 `CGO_ENABLED=1`。Docker 使用 Debian/glibc 并包含必要的 C++/OpenMP 依赖。Windows 需要 Microsoft Visual C++ 2022 x64 运行时（[微软下载说明](https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist)）；系统缺少依赖时总览/系统配置会显示未就绪，平台本身仍能启动，不会伪造识别成功。

校验：Linux 可在本目录执行 `sha256sum -c SHA256SUMS`；Windows 运行 `scripts/setup-local-face.ps1`。系统配置页可查看实际加载状态。

这些文件只包含通用模型和推理库，**不包含客户照片、人脸模板或数据库**。第三方资源条款见 [NOTICE.md](./NOTICE.md)，不因项目源码开源而改变。

当前 Windows 与 Linux amd64 已实测加载/推理；Linux arm64 随附对应运行库并支持构建，尚未在 ARM64 硬件上验收。
