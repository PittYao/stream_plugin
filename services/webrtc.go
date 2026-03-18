package services

import (
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/PittYao/stream_plugin/config"
	"github.com/google/uuid"
	pionice "github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/sirupsen/logrus"
)

var (
	webRTCMutex sync.RWMutex
	webRtcs     = map[string]*WebRTCClient{} // key: rtspURL
)

// WebRTCClient 维护一路 RTSP 的状态
type WebRTCClient struct {
	RTSPClient *RTSPClient
	URI        string
	Ready      chan struct{} // 就绪信号

	// 连接管理
	mu         sync.RWMutex
	viewers    map[string]*Viewer
	timerMutex sync.Mutex
	timer      *time.Timer
}

// Viewer 代表一个看流的 WebRTC 客户端
type Viewer struct {
	ID             string
	Track          *webrtc.TrackLocalStaticRTP
	AudioTrack     *webrtc.TrackLocalStaticRTP // PCMA 音频 RTP 轨道
	PeerConnection *webrtc.PeerConnection
	Expiration     int64
	ErrorCount     int // 连续写入失败计数
}

// StartWebRTC 启动 WebRTC 转流
func StartWebRTC(rtspURL, sdpData string, timeout int64) (string, string, error) {
	// 确保 RTSP 客户端存在，如果不存在则拉流
	webRTCMutex.Lock()
	client, ok := webRtcs[rtspURL]
	if !ok {
		client = &WebRTCClient{
			URI:     rtspURL,
			Ready:   make(chan struct{}),
			viewers: make(map[string]*Viewer),
		}
		webRtcs[rtspURL] = client
		// 异步启动 RTSP 客户端
		go runRTSPClient(client, timeout)
	}
	webRTCMutex.Unlock()

	// 检查并停止之前的 cleanup timer（如果有）
	client.timerMutex.Lock()
	if client.timer != nil {
		client.timer.Stop()
		client.timer = nil
	}
	client.timerMutex.Unlock()

	// 解析 SDP
	sd, err := base64.StdEncoding.DecodeString(sdpData)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode SDP: %w", err)
	}

	viewerID := uuid.New().String()

	// 由于改为每次都生成新的 viewerID，我们不再检查相同的 SDP 是否已存在
	// 前端页面每次刷新应该建立新的 UUID 即可，如果有老的死连接，靠 ConnectionStateFailed 剔除


	// 创建支持 mDNS 解析和 NACK 重传的 Pion WebRTC API
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return "", "", fmt.Errorf("failed to register default codecs: %w", err)
	}

	// 注册默认拦截器（包含 NACK、TWCC、RTCP Reports 的处理），这对于防止丢包卡顿至关重要
	i := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		return "", "", fmt.Errorf("failed to register default interceptors: %w", err)
	}

	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetICEMulticastDNSMode(pionice.MulticastDNSModeQueryOnly)

	webrtcAPI := webrtc.NewAPI(
		webrtc.WithSettingEngine(settingEngine),
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(i),
	)

	// 创建 PeerConnection，从 config.json 获取 ICE Servers (如果是空则不使用 STUN)
	webrtcConfig := webrtc.Configuration{
		ICEServers: config.GetICEServers(),
	}

	peerConnection, err := webrtcAPI.NewPeerConnection(webrtcConfig)
	if err != nil {
		return "", "", fmt.Errorf("failed to create peer connection: %w", err)
	}

	// 创建视频 RTP 轨道 (H264)
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"pion2",
	)
	if err != nil {
		peerConnection.Close()
		return "", "", fmt.Errorf("failed to create video RTP track: %w", err)
	}

	if _, err = peerConnection.AddTrack(videoTrack); err != nil {
		peerConnection.Close()
		return "", "", fmt.Errorf("failed to add video RTP track: %w", err)
	}

	// 创建音频 RTP 轨道 (PCMA / G.711 A-law)
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1},
		"audio",
		"pion2",
	)
	if err != nil {
		peerConnection.Close()
		return "", "", fmt.Errorf("failed to create audio RTP track: %w", err)
	}

	if _, err = peerConnection.AddTrack(audioTrack); err != nil {
		peerConnection.Close()
		return "", "", fmt.Errorf("failed to add audio RTP track: %w", err)
	}

	// 监听连接状态并处理清理
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		logrus.Infof("WebRTC Peer Connection State has changed: %s (Viewer=%s)", s.String(), viewerID)
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			removeViewer(client, viewerID)
		}
	})

	// 打印本地生成的 ICE Candidate
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			logrus.Debugf("Backend ICE Gathering Completed (Viewer=%s)", viewerID)
			return
		}
		logrus.Debugf("Backend generated ICE Candidate: %s (Viewer=%s)", c.String(), viewerID)
	})

	// 设置远端 SDP
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(sd),
	}

	if err := peerConnection.SetRemoteDescription(offer); err != nil {
		peerConnection.Close()
		return "", "", fmt.Errorf("failed to set remote description: %w", err)
	}

	// 创建 answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		peerConnection.Close()
		return "", "", fmt.Errorf("failed to create answer: %w", err)
	}

	// === 修改点：等待包含 candidate 的 Answer ===
	// 使用 pion 提供的 gatherCompletePromise
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	if err = peerConnection.SetLocalDescription(answer); err != nil {
		peerConnection.Close()
		return "", "", fmt.Errorf("failed to set local description: %w", err)
	}

	// 阻塞等待 ICE 收集完成 (由于没有实现前端 Trickle ICE，需要收集完一并发送)
	<-gatherComplete

	// 获取包含本地描述（此时必然包含 host/srflx candidates）
	localDesc := peerConnection.LocalDescription()
	answerSDP := base64.StdEncoding.EncodeToString([]byte(localDesc.SDP))

	viewer := &Viewer{
		ID:             viewerID,
		Track:          videoTrack,
		AudioTrack:     audioTrack,
		PeerConnection: peerConnection,
		Expiration:     time.Now().Unix() + timeout,
	}

	client.mu.Lock()
	client.viewers[viewerID] = viewer
	client.mu.Unlock()

	// 此时因为我们改成了透传，如果没有 I 帧观众会一直黑屏，这里不再发送空时长的 Sample 缓存。
	// WebRTC 对于 RTP 通道的解析是强制依赖关键帧起始的。因此移除旧的 SPS/PPS 直接拼凑。

	return answerSDP, viewerID, nil
}

// removeViewer 从 client 中安全移除 viewer，如果 viewers 归零则启动定时清理机制
func removeViewer(client *WebRTCClient, viewerID string) {
	client.mu.Lock()
	if v, exists := client.viewers[viewerID]; exists {
		v.PeerConnection.Close()
		delete(client.viewers, viewerID)
		logrus.Infof("Viewer removed: %s, remaining viewers: %d", viewerID, len(client.viewers))
	} else {
		client.mu.Unlock()
		return // 已经被删除了
	}

	count := len(client.viewers)
	client.mu.Unlock()

	// 启动延时清理 RTSP 流的任务
	if count == 0 {
		client.timerMutex.Lock()
		if client.timer == nil {
			client.timer = time.AfterFunc(10*time.Second, func() {
				webRTCMutex.Lock()
				// 再次判断 map 中是否确实还是0 人，有可能在 10s 内又进来了人
				client.mu.RLock()
				finalCount := len(client.viewers)
				client.mu.RUnlock()

				if finalCount == 0 {
					logrus.Infof("No viewers for RTSP %s for 10s, closing RTSP client.", client.URI)
					if client.RTSPClient != nil {
						client.RTSPClient.Close()
					}
					delete(webRtcs, client.URI)
				}
				webRTCMutex.Unlock()
			})
		}
		client.timerMutex.Unlock()
	}
}

// ForceStopAllWebRTC 立即强制停止整个 RTSP 流及其所有 WebRTC 连接 (备用)
func ForceStopAllWebRTC(rtspURL string) error {
	webRTCMutex.Lock()
	client, ok := webRtcs[rtspURL]
	if !ok {
		webRTCMutex.Unlock()
		return fmt.Errorf("rtsp url %s not found (already closed)", rtspURL)
	}
	delete(webRtcs, rtspURL)
	webRTCMutex.Unlock()

	// 关闭 RTSP 客户端
	if client.RTSPClient != nil {
		client.RTSPClient.Close()
	}

	// 关闭所有关联的 WebRTC 连接
	client.mu.Lock()
	for _, v := range client.viewers {
		v.PeerConnection.Close()
	}
	client.viewers = make(map[string]*Viewer)
	client.mu.Unlock()

	return nil
}

// RunWebRtc 这里提供预初始化的功能，兼容 handlers 层的调用。
func RunWebRtc(rtspURL string, timeout int64) {
	webRTCMutex.Lock()
	defer webRTCMutex.Unlock()
	if _, ok := webRtcs[rtspURL]; !ok {
		client := &WebRTCClient{
			URI:     rtspURL,
			Ready:   make(chan struct{}),
			viewers: make(map[string]*Viewer),
		}
		webRtcs[rtspURL] = client
		go runRTSPClient(client, timeout)
	}
}

// runRTSPClient 运行 RTSP 客户端
func runRTSPClient(client *WebRTCClient, timeout int64) {
	rtspURL := client.URI
	rtspClient := NewRTSPClient()

	client.RTSPClient = rtspClient

	// 优化后的透传：RTSP 截获的原始 RTP 包
	rtspClient.OnH264RTP = func(pkt *rtp.Packet) {
		client.mu.RLock()
		viewers := make([]*Viewer, 0, len(client.viewers))
		for _, v := range client.viewers {
			viewers = append(viewers, v)
		}
		client.mu.RUnlock()

		for _, v := range viewers {
			if v.Track != nil {
				// WebRTC 视频 Track 发送 RTP 前不需要手动修改时间戳和序列号，
				// Pion 会自动利用 Track 底层的状态机 (TrackLocal) 和 Interceptors 进行重置与重新打包。
				// 我们只需要传入原始 RTP 包的深拷贝，避免并发竞争。
				outPkt := *pkt

				if err := v.Track.WriteRTP(&outPkt); err != nil {
					v.ErrorCount++
					if v.ErrorCount > 100 {
						logrus.Warnf("Viewer %s reached error threshold writing video RTP, forcing close", v.ID)
						removeViewer(client, v.ID)
					}
				} else {
					v.ErrorCount = 0
				}
			}
		}
	}

	// 音频透传
	rtspClient.OnAudioRTP = func(pkt *rtp.Packet) {
		client.mu.RLock()
		viewers := make([]*Viewer, 0, len(client.viewers))
		for _, v := range client.viewers {
			viewers = append(viewers, v)
		}
		client.mu.RUnlock()

		for _, v := range viewers {
			if v.AudioTrack != nil {
				outPkt := *pkt

				if err := v.AudioTrack.WriteRTP(&outPkt); err != nil {
					v.ErrorCount++
					// 可以复用 ErrorCount 获取，也可分开，暂复用
				}
			}
		}
	}

	if err := rtspClient.Open(rtspURL); err != nil {
		logrus.Errorf("[RTSP] Error opening %s: %v", rtspURL, err)
		webRTCMutex.Lock()
		delete(webRtcs, rtspURL)
		webRTCMutex.Unlock()
		return
	}

	close(client.Ready) // 标记 RTSP 已就绪
	logrus.Infof("[RTSP] Successfully started reading stream %s", rtspURL)

	// 阻塞并运行 Client, 直到遇到错误或关闭
	if err := rtspClient.Client.Wait(); err != nil {
		logrus.Errorf("[RTSP] Client finished with err: %v", err)
	}

	// 退出后进行清理
	webRTCMutex.Lock()
	if c, ok := webRtcs[rtspURL]; ok && c == client {
		delete(webRtcs, rtspURL)
	}
	webRTCMutex.Unlock()
}
