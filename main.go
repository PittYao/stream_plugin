package main

import (
	"embed"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/PittYao/stream_plugin/config"
	"github.com/PittYao/stream_plugin/handlers"
	"github.com/PittYao/stream_plugin/logger"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

//go:embed html
var htmlFiles embed.FS

// CORS 中间件
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Header("Access-Control-Allow-Credentials", "true")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// staticServeMiddleware 创建静态文件服务中间件
func staticServeMiddleware(fsEmbed embed.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if path == "" || path == "/" {
			path = "html/index.html"
		} else {
			path = "html/" + path
		}

		data, err := fsEmbed.ReadFile(path)
		if err != nil {
			c.Next()
			return
		}

		// 根据文件扩展名设置 Content-Type
		contentType := getContentType(path)
		c.Header("Content-Type", contentType)
		c.Data(http.StatusOK, contentType, data)
		c.Abort()
	}
}

// serveHTML 处理 HTML 路由
func serveHTML(c *gin.Context) {
	path := c.Param("filepath")
	if path == "" || path == "/" {
		path = "/index.html"
	}

	data, err := htmlFiles.ReadFile("html" + path)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	contentType := getContentType(path)
	c.Header("Content-Type", contentType)
	c.Data(http.StatusOK, contentType, data)
}

// getContentType 根据文件扩展名返回 Content-Type
func getContentType(path string) string {
	ext := strings.ToLower(path)
	switch {
	case strings.HasSuffix(ext, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(ext, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(ext, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(ext, ".json"):
		return "application/json"
	case strings.HasSuffix(ext, ".png"):
		return "image/png"
	case strings.HasSuffix(ext, ".jpg"), strings.HasSuffix(ext, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(ext, ".gif"):
		return "image/gif"
	case strings.HasSuffix(ext, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(ext, ".ico"):
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

func main() {
	// 初始化日志
	logger.Init()

	// 加载配置
	cfg, err := config.Load("config.json")
	if err != nil {
		logrus.Warnf("配置文件加载失败，使用默认配置: %v", err)
		cfg = &config.Config{
			Server: config.ServerConfig{Port: 7779},
		}
	}

	// 创建 Gin 路由
	r := gin.Default()

	// 使用 CORS 中间件
	r.Use(CORS())

	// 加载 HTML 静态文件
	r.Use(staticServeMiddleware(htmlFiles))

	// 注册静态文件路由
	r.GET("/html/*filepath", serveHTML)

	// 注册路由
	r.POST("/app/plugin/rtsp2webrtc", handlers.StartPreviewHandler)
	r.GET("/app/plugin/rtsp2webrtcEnd", handlers.StopPreviewHandler)

	// 启动服务器
	addr := ":" + strconv.Itoa(cfg.Server.Port)
	logrus.Infof("服务启动，监听端口: %d", cfg.Server.Port)
	if err := r.Run(addr); err != nil {
		logrus.Fatalf("服务启动失败: %v", err)
		os.Exit(1)
	}
}
