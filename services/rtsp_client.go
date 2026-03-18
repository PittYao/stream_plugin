package services

import (
	"fmt"
	"strings"
	"sync"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/bluenviron/gortsplib/v4/pkg/url"
	"github.com/pion/rtp"
	"github.com/sirupsen/logrus"
)

// RTSPClient RTSP 客户端封装
type RTSPClient struct {
	Client      *gortsplib.Client
	URI         string
	SPS         []byte
	PPS         []byte
	OnH264RTP   func(pkt *rtp.Packet)
	OnAudioRTP  func(pkt *rtp.Packet) // G711 PCMA 音频 RTP 回调
	HasAudio    bool                  // 是否存在音频轨道

	closed  bool
	closeMu sync.Mutex
}

// NewRTSPClient 创建 RTSP 客户端
func NewRTSPClient() *RTSPClient {
	tcpTransport := gortsplib.TransportTCP
	return &RTSPClient{
		Client: &gortsplib.Client{
			// 强制使用 TCP 传输，从源头上解决内网交换机或弱网导致的 UDP RTSP 丢包，大幅度减少卡顿
			Transport: &tcpTransport,
			OnDecodeError: func(err error) {
				// 忽略 112 这类海康等摄像头经常自带的私有轨道/无用轨道的告警
				if strings.Contains(err.Error(), "unknown payload type") {
					return
				}
				logrus.Warnf("[RTSP Decode Warn] %v", err)
			},
		},
	}
}

// Open 连接到 RTSP 流
func (r *RTSPClient) Open(rtspURL string) error {
	r.URI = rtspURL

	u, err := url.Parse(rtspURL)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}

	// 连接服务器并获取媒体列表
	err = r.Client.Start(u.Scheme, u.Host)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	session, _, err := r.Client.Describe(u)
	if err != nil {
		r.Client.Close()
		return fmt.Errorf("failed to describe: %w", err)
	}

	// 查找 H264 视频媒体格式
	var forma *format.H264
	var videoMedia *description.Media

	// 查找 G711 PCMA 音频媒体格式
	var audioForma *format.G711
	var audioMedia *description.Media

	for _, m := range session.Medias {
		for _, f := range m.Formats {
			if h264Format, ok := f.(*format.H264); ok {
				forma = h264Format
				videoMedia = m
			}
			if g711Format, ok := f.(*format.G711); ok {
				if !g711Format.MULaw { // 只取 A-law (PCMA)
					audioForma = g711Format
					audioMedia = m
				}
			}
		}
	}

	if forma == nil {
		r.Client.Close()
		return fmt.Errorf("h264 media not found in the stream")
	}

	// 提取并保存 SPS/PPS
	if len(forma.SPS) > 0 {
		r.SPS = forma.SPS
	}
	if len(forma.PPS) > 0 {
		r.PPS = forma.PPS
	}

	// Setup 视频轨道
	_, err = r.Client.Setup(session.BaseURL, videoMedia, 0, 0)
	if err != nil {
		r.Client.Close()
		return fmt.Errorf("failed to setup video media: %w", err)
	}

	// Setup 音频轨道（如果存在）
	if audioForma != nil && audioMedia != nil {
		_, err = r.Client.Setup(session.BaseURL, audioMedia, 0, 0)
		if err != nil {
			logrus.Warnf("[RTSP] Warning: failed to setup audio media: %v\n", err)
			// 音频 setup 失败不影响视频
			audioForma = nil
			audioMedia = nil
		} else {
			r.HasAudio = true
			logrus.Infof("[RTSP] Audio track (PCMA/G711) detected and setup")
		}
	}

	// 监听并直接透传 H264 RTP 数据，避免 Decoder 重组 NALU 丢失原始时间戳
	r.Client.OnPacketRTP(videoMedia, forma, func(pkt *rtp.Packet) {
		if r.OnH264RTP != nil {
			r.OnH264RTP(pkt)
		}
	})

	// 挂载音频 RTP 监听并透传
	if audioForma != nil && audioMedia != nil {
		r.Client.OnPacketRTP(audioMedia, audioForma, func(pkt *rtp.Packet) {
			if r.OnAudioRTP != nil {
				r.OnAudioRTP(pkt)
			}
		})
	}

	_, err = r.Client.Play(nil)
	if err != nil {
		r.Client.Close()
		return fmt.Errorf("failed to play: %w", err)
	}

	return nil
}

// Close 关闭连接
func (r *RTSPClient) Close() {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()

	if r.closed {
		return
	}
	r.closed = true

	if r.Client != nil {
		r.Client.Close()
	}
}

// IsClosed 检查是否已关闭
func (r *RTSPClient) IsClosed() bool {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	return r.closed
}
