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

	// 获取 WebRTC SDP Answer
	result, err := services.StartWebRTC(rtspURL, data, 3600)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.Error(err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.Success(result))
}

// StopPreviewHandler 处理停止预览请求
func StopPreviewHandler(c *gin.Context) {
	rtspURL := c.Query("rtspUrl")

	if rtspURL == "" {
		c.JSON(http.StatusBadRequest, models.Error("rtspUrl 不能为空"))
		return
	}

	err := services.StopWebRTC(rtspURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.Error(err.Error()))
		return
	}

	c.JSON(http.StatusOK, models.Success("关闭成功"))
}
