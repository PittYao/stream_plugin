package config

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/pion/webrtc/v4"
)

//go:embed config.json
var embeddedConfig []byte

type Config struct {
	Server ServerConfig `json:"server"`
	RTSP   RTSPConfig   `json:"rtsp"`
	WebRTC WebRTCConfig `json:"webrtc"`
}

type ServerConfig struct {
	Port int `json:"port"`
}

type RTSPConfig struct {
	ConnectTimeout int `json:"connect_timeout"`
	ReadTimeout    int `json:"read_timeout"`
}

type WebRTCConfig struct {
	ICEServers   []ICEServerConfig `json:"ice_servers"`
	MediaBufSize int               `json:"media_buf_size"`
}

type ICEServerConfig struct {
	URLs []string `json:"urls"`
}

var (
	cfg  *Config
	once sync.Once
)

// Load 加载配置文件
func Load(path string) (*Config, error) {
	var err error
	once.Do(func() {
		data := embeddedConfig
		if len(data) == 0 {
			err = nil
			return
		}
		cfg = &Config{}
		e := json.Unmarshal(data, cfg)
		if e != nil {
			err = e
			cfg = nil
			return
		}
		// 设置默认值
		if cfg.Server.Port == 0 {
			cfg.Server.Port = 7779
		}
		if cfg.RTSP.ConnectTimeout == 0 {
			cfg.RTSP.ConnectTimeout = 10
		}
		if cfg.RTSP.ReadTimeout == 0 {
			cfg.RTSP.ReadTimeout = 30
		}
		if cfg.WebRTC.MediaBufSize == 0 {
			cfg.WebRTC.MediaBufSize = 150
		}
	})
	return cfg, err
}

// Get 获取配置实例
func Get() *Config {
	return cfg
}

// GetICEServers 获取 ICE 服务器配置
func GetICEServers() []webrtc.ICEServer {
	if cfg == nil || len(cfg.WebRTC.ICEServers) == 0 {
		return nil
	}
	servers := make([]webrtc.ICEServer, len(cfg.WebRTC.ICEServers))
	for i, srv := range cfg.WebRTC.ICEServers {
		servers[i] = webrtc.ICEServer{
			URLs: srv.URLs,
		}
	}
	return servers
}

// GetMediaBufSize 获取媒体缓存大小
func GetMediaBufSize() int {
	if cfg == nil {
		return 150
	}
	return cfg.WebRTC.MediaBufSize
}
