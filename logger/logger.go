package logger

import (
	"os"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Init initializes the global logrus logger
func Init() {
	// 确保日志目录存在
	err := os.MkdirAll("logs", 0755)
	if err != nil {
		logrus.Fatalf("Failed to create log directory: %v", err)
	}

	// 设置输出样式为带有时间戳的 TextFormatter
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		DisableColors: true, // 写入文件不需要颜色
	})

	// 设置 lumberjack 日志轮转，默认保留7天
	lumberjackLogger := &lumberjack.Logger{
		Filename:   "logs/stream_plugin.log", // 日志文件路径
		MaxSize:    100,                      // 每个日志文件最大 100 MB
		MaxBackups: 7,                        // 最多保留 7 个备份
		MaxAge:     7,                        // 最多保留 7 天
		Compress:   true,                     // 压缩旧日志
	}

	logrus.SetOutput(lumberjackLogger)
	logrus.SetLevel(logrus.InfoLevel)
	
	// 在控制台也输出一份，方便调试时查看
	logrus.SetOutput(os.Stdout)
}
