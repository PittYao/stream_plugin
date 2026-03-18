# RTSP to WebRTC Stream Plugin

这是一个高性能的 RTSP 转 WebRTC 流媒体插件，基于 Go 语言开发，专门针对内网弱网环境下的实时监控播放进行了底层优化。

## 核心特性

- **极致流畅 (Zero-Drift RTP Passthrough)**: 采用原始 RTP 包透传技术。不同于常规的 NALU 解码重组方案，本项目直接转发摄像头的原始 RTP 报文及硬件时钟戳，彻底解决了 WebRTC 播放中常见的时间戳漂移导致的视频卡顿、跳帧问题。
- **高并发流复用**: 多个浏览器观看同一个 RTSP 地址时，后端仅建立一路 RTSP 连接，极大地节省了摄像头的连接数和带宽压力。
- **智能资源回收 (Reference Counting)**: 内置引用计数器。当最后一个观看者断开连接 10 秒后，系统会自动关闭 RTSP 推流资源，确保服务器资源不被僵尸连接占用。
- **弱网抗性**: 强制启用 TCP 传输 RTSP 数据，配合 Pion WebRTC 的拦截器 (NACK / RTCP) 机制，在网络抖动环境下依然能提供稳定的画面。
- **工业级日志**: 集成 `Logrus` 与 `Lumberjack`，支持日志自动按天轮转，默认保留最近 7 天的日志，方便排查现场问题。

## 技术栈

- **后端**: Go 1.25.0, Gin (HTTP Framework)
- **WebRTC**: Pion WebRTC v4 (内核 RTP 透传模式)
- **RTSP**: Gortsplib v4
- **日志**: Logrus + Lumberjack

## 快速开始

### 1. 编译

项目提供 Makefile，支持在 Linux/Mac 上交叉编译出 Windows 版本。

```bash
# 编译当前系统版本
go build -o stream_plugin .

# 编译 Windows 64位版本 (用于现场部署)
make windows
```

### 2. 运行

确保根目录下存在 `config.json` 配置文件。

```bash
./stream_plugin
```

### 3. 使用

插件监听端口（默认 7779），可通过浏览器打开 `http://localhost:7779` 进行测试。

#### API 接口

- **开始播放**: `POST /app/plugin/rtsp2webrtc`
  - 参数: `rtspUrl` (RTSP 地址), `data` (前端生成的 Offer SDP)
  - 响应: 返回服务器生成的 Answer SDP

- **停止播放**: `GET /app/plugin/rtsp2webrtcEnd`
  - 参数: `rtspUrl`
  - 响应: 成功状态（实际销毁逻辑由连接状态自动触发）

## 配置文件 (config.json)

```json
{
  "server": {
    "port": 7779
  },
  "rtsp": {
    "connect_timeout": 10,
    "read_timeout": 30
  },
  "webrtc": {
    "ice_servers": [],
    "media_buf_size": 150
  }
}
```

## 日志查看

日志文件存储在 `./logs/stream_plugin.log`。如果程序启动失败或播放卡顿，请优先检查此文件。

---

