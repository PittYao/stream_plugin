package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/PittYao/stream_plugin/models"
	"github.com/PittYao/stream_plugin/services"
	"github.com/gin-gonic/gin"
)

// StartPreviewHandler 处理启动预览请求
func StartPreviewHandler(c *gin.Context) {
	rtspURL := c.PostForm("rtspUrl")
	data := c.PostForm("data")

	if rtspURL == "" {
		c.JSON(http.StatusBadRequest, models.Error("rtspUrl 不能为空"))
		return
	}

	// 验证 URL 格式
	if _, err := url.ParseRequestURI(rtspURL); err != nil {
		c.JSON(http.StatusBadRequest, models.Error("rtspUrl 格式无效"))
		return
	}
	// 验证是否为 rtsp 或 rtsps 协议
	if !strings.HasPrefix(strings.ToLower(rtspURL), "rtsp://") && !strings.HasPrefix(strings.ToLower(rtspURL), "rtsps://") {
		c.JSON(http.StatusBadRequest, models.Error("rtspUrl 必须以 rtsp:// 或 rtsps:// 开头"))
		return
	}

	if data == "" {
		c.JSON(http.StatusBadRequest, models.Error("data 不能为空"))
		return
	}

	// 预先启动或确保 RTSP 在后台运行
	services.RunWebRtc(rtspURL, 3600)

	// 获取 WebRTC SDP Answer (并带有 viewerID 但是前端可能不读取)
	resultSdp, _, err := services.StartWebRTC(rtspURL, data, 3600)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.Error(err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.Success(resultSdp))
}

// StopPreviewHandler 处理停止预览请求
func StopPreviewHandler(c *gin.Context) {
	rtspURL := c.Query("rtspUrl")
	// 注意：新版采用引用计数管理，不再强制要求前端传回 viewerId。
	// 如果前端未传 viewerId，则依赖网络层的 ConnectionStateFailed 事件来自动清理失效连接。
	// 为了兼容旧版“强制停止”逻辑，若只传 rtspUrl 则可能触发该流下的全局清理（不推荐并发场景使用）。
	// 建议让“断开”逻辑完全交给 WebRTC 层的 Disconnected/Failed 事件触发 removeViewer，以实现平滑退出。
	
	if rtspURL == "" {
		c.JSON(http.StatusBadRequest, models.Error("rtspUrl 不能为空"))
		return
	}

	// 我们在这里只是打个日志响应前端成功，真正的销毁交给 Viewer 内部的 Disconnect 事件
	// 如果业务实在要求点击“停止”必须秒断其他人的流，那就调用 services.ForceStopWebRTC()
	c.JSON(http.StatusOK, models.Success("关闭指令已收到，等待本地流自动回收"))
}
