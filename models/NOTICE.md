# 第三方资源与用途说明

EasyRun 自有源码采用根目录 MIT 许可证；本目录第三方文件保留各自权利及用途限制，不受该许可证重新授权。

## Megatron 模型

本目录保留当前部署使用的 `Megatron` MNN 资源包，具体 SHA-256 见 `SHA256SUMS`，不声称它与上游当前最新包字节相同。内容为通用网络权重与配置，不含本平台账号底图或识别模板。

[InspireFace 上游用途说明](https://github.com/HyperInspire/InspireFace#license)将这些模型限定为学术用途；[InsightFace 说明](https://github.com/deepinsight/insightface#license)将预训练模型及相关训练数据限定为非商业研究。将项目代码部署为商业平台，不会自动获得模型的商业使用权；商业使用需另行满足上游授权条件。

## 原生推理 SDK

运行库来自 [HyperInspire/InspireFace 官方发行页](https://github.com/HyperInspire/InspireFace/releases)：

- Windows amd64：`v1.2.4 / inspireface-windows-x64-1.2.4.zip`。
- Linux amd64：`v1.2.3 / inspireface-linux-x86-ubuntu18-1.2.3.zip`。
- Linux arm64：`v1.2.3 / inspireface-linux-aarch64-1.2.3.zip`。

下载归档先按上游公布的 SHA-256 验证，再提取库文件；仓库保留库文件自身校验值。SDK 及其 MNN、Eigen、SQLite、yaml-cpp 等组成部分遵循各自上游许可与版权声明，原作者保留全部相关权利。参考 [InspireFace](https://github.com/HyperInspire/InspireFace)、[InsightFace](https://github.com/deepinsight/insightface)、[MNN](https://github.com/alibaba/MNN)、[Eigen](https://gitlab.com/libeigen/eigen)。

本目录不包含微软 C++ 运行时的再分发副本；Windows 系统依赖由微软运行时安装包提供。
