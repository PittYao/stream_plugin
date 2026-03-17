package services

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	pionice "github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
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
	mu      sync.RWMutex
	viewers map[string]*Viewer
}

// Viewer 代表一个看流的 WebRTC 客户端
type Viewer struct {
	ID             string
	Track          *webrtc.TrackLocalStaticSample
	AudioTrack     *webrtc.TrackLocalStaticSample // PCMA 音频轨道
	PeerConnection *webrtc.PeerConnection
	Expiration     int64
}

// StartWebRTC 启动 WebRTC 转流
func StartWebRTC(rtspURL, sdpData string, timeout int64) (string, error) {
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

	md5Data := fmt.Sprintf("%x", md5.Sum([]byte(sdpData)))

	// 解析 SDP
	sd, err := base64.StdEncoding.DecodeString(sdpData)
	if err != nil {
		return "", fmt.Errorf("failed to decode SDP: %w", err)
	}

	// 检查是否已存在该Viewer
	client.mu.Lock()
	if v, exists := client.viewers[md5Data]; exists {
		v.Expiration = time.Now().Unix() + timeout
		client.mu.Unlock()
		return base64.StdEncoding.EncodeToString([]byte(v.PeerConnection.LocalDescription().SDP)), nil
	}
	client.mu.Unlock()

	// 创建支持 mDNS 解析的 Pion WebRTC API
	// Chrome 会生成 `.local` mDNS 候选，Pion 默认无法解析
	// 使用 QueryOnly 模式：Pion 可以解析浏览器发来的 .local 地址，
	// 同时 Pion 自己仍然发送真实的 IP 地址候选（而不是 .local 地址）
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetICEMulticastDNSMode(pionice.MulticastDNSModeQueryOnly)

	webrtcAPI := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))

	// 创建 PeerConnection
	webrtcConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	peerConnection, err := webrtcAPI.NewPeerConnection(webrtcConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create peer connection: %w", err)
	}

	// 创建视频轨道 (H264)
	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"pion2",
	)
	if err != nil {
		peerConnection.Close()
		return "", fmt.Errorf("failed to create video track: %w", err)
	}

	if _, err = peerConnection.AddTrack(videoTrack); err != nil {
		peerConnection.Close()
		return "", fmt.Errorf("failed to add video track: %w", err)
	}

	// 创建音频轨道 (PCMA / G.711 A-law)
	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1},
		"audio",
		"pion2",
	)
	if err != nil {
		peerConnection.Close()
		return "", fmt.Errorf("failed to create audio track: %w", err)
	}

	if _, err = peerConnection.AddTrack(audioTrack); err != nil {
		peerConnection.Close()
		return "", fmt.Errorf("failed to add audio track: %w", err)
	}

	// 监听连接状态并处理清理
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Printf("Peer Connection State has changed: %s\n", s.String())
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			client.mu.Lock()
			delete(client.viewers, md5Data)
			client.mu.Unlock()
		}
	})

	// 打印本地生成的 ICE Candidate
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			fmt.Println("Backend ICE Gathering Completed")
			return
		}
		fmt.Printf("Backend generated ICE Candidate: %s\n", c.String())
	})

	// 设置远端 SDP
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(sd),
	}

	if err := peerConnection.SetRemoteDescription(offer); err != nil {
		peerConnection.Close()
		return "", fmt.Errorf("failed to set remote description: %w", err)
	}

	// 创建 answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		peerConnection.Close()
		return "", fmt.Errorf("failed to create answer: %w", err)
	}

	// === 修改点：立即将生成的 SDP 发给客户端，不等待 ICE 收集 ===
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		peerConnection.Close()
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// 等待非常短的时间(50ms)让 Pion 收集到 host candidate (因为 host 是毫秒级的)
	// 这样可以避免完全没有 IP 的空白 SDP 或者导致 GatheringCompletePromise 死锁
	time.Sleep(50 * time.Millisecond)

	// 获取包含本地描述（至少包含 host candidates）
	localDesc := peerConnection.LocalDescription()
	answerSDP := base64.StdEncoding.EncodeToString([]byte(localDesc.SDP))

	viewer := &Viewer{
		ID:             md5Data,
		Track:          videoTrack,
		AudioTrack:     audioTrack,
		PeerConnection: peerConnection,
		Expiration:     time.Now().Unix() + timeout,
	}

	client.mu.Lock()
	client.viewers[md5Data] = viewer
	client.mu.Unlock()

	// 等待 RTSP 开始接收，立即为它发送缓存的 SPS/PPS 以保证开播速度
	go func() {
		<-client.Ready
		if client.RTSPClient != nil && client.RTSPClient.SPS != nil && client.RTSPClient.PPS != nil {
			sps := append([]byte{0x00, 0x00, 0x00, 0x01}, client.RTSPClient.SPS...)
			pps := append([]byte{0x00, 0x00, 0x00, 0x01}, client.RTSPClient.PPS...)

			// pion/webrtc 的 WriteSample 支持包含多个起止码的完整 payload
			payload := append(sps, pps...)
			videoTrack.WriteSample(media.Sample{Data: payload, Duration: 0})
		}
	}()

	return answerSDP, nil
}

// StopWebRTC 停止整个 RTSP 流及其所有 WebRTC 连接
func StopWebRTC(rtspURL string) error {
	webRTCMutex.Lock()
	client, ok := webRtcs[rtspURL]
	if !ok {
		webRTCMutex.Unlock()
		return fmt.Errorf("rtsp url %s not found", rtspURL)
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

	// 设置向 Track 写入的回调
	rtspClient.OnH264Data = func(nalus [][]byte, pts time.Duration) {
		// gortsplib v4 解出的 NALU 不带 Annex B start code (0x00, 0x00, 0x00, 0x01)
		// 我们必须在送给 webrtc track 前加上 start code
		var payload []byte
		for _, nalu := range nalus {
			payload = append(payload, []byte{0x00, 0x00, 0x00, 0x01}...)
			payload = append(payload, nalu...)
		}

		if len(payload) == 0 {
			return
		}

		client.mu.RLock()
		viewers := make([]*Viewer, 0, len(client.viewers))
		for _, v := range client.viewers {
			viewers = append(viewers, v)
		}
		client.mu.RUnlock()

		for _, v := range viewers {
			if err := v.Track.WriteSample(media.Sample{Data: payload, Duration: pts}); err != nil {
				// 忽略失败
			}
		}
	}

	// 设置音频回调：将 PCMA 音频数据广播给所有 WebRTC 观看者
	rtspClient.OnAudioData = func(samples []byte, pts time.Duration) {
		if len(samples) == 0 {
			return
		}

		client.mu.RLock()
		viewers := make([]*Viewer, 0, len(client.viewers))
		for _, v := range client.viewers {
			viewers = append(viewers, v)
		}
		client.mu.RUnlock()

		for _, v := range viewers {
			if v.AudioTrack != nil {
				// G711 PCMA: 8000Hz, 每个样本 1 字节, 每帧通常 160 样本 = 20ms
				duration := time.Duration(len(samples)) * time.Second / 8000
				if err := v.AudioTrack.WriteSample(media.Sample{Data: samples, Duration: duration}); err != nil {
					// 忽略失败
				}
			}
		}
	}

	if err := rtspClient.Open(rtspURL); err != nil {
		fmt.Printf("[RTSP] Error opening %s: %v\n", rtspURL, err)
		webRTCMutex.Lock()
		delete(webRtcs, rtspURL)
		webRTCMutex.Unlock()
		return
	}

	close(client.Ready) // 标记 RTSP 已就绪

	// 阻塞并运行 Client, 直到遇到错误或关闭
	if err := rtspClient.Client.Wait(); err != nil {
		fmt.Printf("[RTSP] Client finished with err: %v\n", err)
	}

	// 退出后进行清理
	webRTCMutex.Lock()
	if c, ok := webRtcs[rtspURL]; ok && c == client {
		delete(webRtcs, rtspURL)
	}
	webRTCMutex.Unlock()
}
