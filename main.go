//go:build cgo
// +build cgo

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/opus"
	"github.com/pion/mediadevices/pkg/codec/x264"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v4"

	_ "github.com/pion/mediadevices/pkg/driver/audiotest"
	_ "github.com/pion/mediadevices/pkg/driver/videotest"
)

const (
	// Codec and media settings
	DefaultVideoBitrate = 2000 // kbps
	AudioLatency        = 10 * time.Millisecond
	VideoFrameRate      = 30.0
	AudioSampleRate     = 48000
	AudioChannels       = 2

	// WebRTC and connection settings
	ICETimeout          = 30 * time.Second
	ICEKeepalive        = time.Second
	DefaultConnTimeout  = 10 * time.Second
	CleanupTimeout      = 2 * time.Second
	WriteDeadlineBuffer = time.Second
	ForceExitDelay      = 3 * time.Second

	// Connection retry settings
	MaxRetries       = 5
	BaseRetryDelay   = 3 * time.Second
	JitterMultiplier = 0.75

	// Default CLI values
	DefaultConnectionTimeout = 15 // seconds

	// Codec parameters
	H264Profile     = "profile-level-id=42e01f;packetization-mode=1"
	OpusProfile     = "sprop-stereo=1;stereo=1"
	H264PayloadType = 96
	OpusPayloadType = 111
	H264ClockRate   = 90000
	OpusClockRate   = 48000
)

func logf(component, format string, args ...interface{}) {
	log.Printf("[%s] "+format, append([]interface{}{component}, args...)...)
}

type Config struct {
	WSSUrl            string
	AppName           string
	StreamKey         string
	VideoBitrate      int
	ConnectTimeout    time.Duration
	TestDuration      time.Duration
	ConnectionTimeout time.Duration
}

type WowzaMessage struct {
	Status            int            `json:"status"`
	StatusDescription string         `json:"statusDescription"`
	Direction         string         `json:"direction"`
	Command           string         `json:"command"`
	StreamInfo        StreamInfo     `json:"streamInfo"`
	SDP               SDPMessage     `json:"sdp"`
	ICECandidates     []ICECandidate `json:"iceCandidates,omitempty"`
}

type StreamInfo struct {
	ApplicationName string `json:"applicationName"`
	StreamName      string `json:"streamName"`
}

type SDPMessage struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

type ICECandidate struct {
	Candidate     string `json:"candidate"`
	SDPMid        string `json:"sdpMid"`
	SDPMLineIndex int    `json:"sdpMLineIndex"`
}

type Client struct {
	conn        *websocket.Conn
	pc          *webrtc.PeerConnection
	mediaStream mediadevices.MediaStream
	connected   chan bool
	once        sync.Once
}

func NewClient() *Client {
	return &Client{connected: make(chan bool)}
}

func wrapError(operation string, err error) error {
	return fmt.Errorf("%s failed: %v", operation, err)
}

// parseICECandidate extracts human-readable info from an ICE candidate string
func parseICECandidate(candidateStr string) string {
	// ICE candidate format: "candidate:foundation component protocol priority ip port typ type raddr rport generation"
	parts := strings.Fields(candidateStr)
	if len(parts) < 8 {
		return "invalid"
	}
	
	protocol := parts[2]  // usually "udp" or "tcp"
	ip := parts[4]        // IP address
	port := parts[5]      // port number
	typ := parts[7]       // candidate type: "host", "srflx", "relay", etc.
	
	return fmt.Sprintf("%s:%s/%s (%s)", ip, port, protocol, typ)
}

// registerCodecs sets up both audio and video codecs
func registerCodecs(me *webrtc.MediaEngine) error {
	codecs := []struct {
		params webrtc.RTPCodecParameters
		typ    webrtc.RTPCodecType
	}{
		{
			webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:    "video/H264",
					ClockRate:   H264ClockRate,
					SDPFmtpLine: H264Profile,
				},
				PayloadType: H264PayloadType,
			},
			webrtc.RTPCodecTypeVideo,
		},
		{
			webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:    "audio/opus",
					ClockRate:   OpusClockRate,
					Channels:    AudioChannels,
					SDPFmtpLine: OpusProfile,
				},
				PayloadType: OpusPayloadType,
			},
			webrtc.RTPCodecTypeAudio,
		},
	}

	for _, codec := range codecs {
		if err := me.RegisterCodec(codec.params, codec.typ); err != nil {
			return wrapError(fmt.Sprintf("%s codec registration", codec.params.MimeType), err)
		}
	}
	return nil
}

// createMediaStream sets up the media pipeline with both audio and video
func createMediaStream(config Config) (mediadevices.MediaStream, error) {
	log.Printf("Creating Opus encoder parameters...")
	opusParams, err := opus.NewParams()
	if err != nil {
		return nil, wrapError("opus params creation", err)
	}
	opusParams.Latency = opus.Latency10ms
	log.Printf("Opus latency set to: %v", opusParams.Latency.Duration())

	log.Printf("Creating x264 encoder parameters...")
	x264Params, err := x264.NewParams()
	if err != nil {
		return nil, wrapError("x264 params creation", err)
	}
	x264Params.Preset = x264.PresetUltrafast
	bitrate := config.VideoBitrate
	if bitrate == 0 {
		bitrate = DefaultVideoBitrate
	}
	x264Params.BitRate = bitrate * 1000
	log.Printf("x264 configured: ultrafast, %dkbps, baseline profile", bitrate)

	log.Printf("Creating MediaStream with audiotest and videotest drivers...")
	return mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Audio: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String("audiotest")
			c.SampleRate = prop.Int(AudioSampleRate)
			c.ChannelCount = prop.Int(AudioChannels)
			c.Latency = prop.Duration(AudioLatency)
		},
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String("videotest")
			c.FrameRate = prop.Float(VideoFrameRate)
		},
		Codec: mediadevices.NewCodecSelector(
			mediadevices.WithAudioEncoders(&opusParams),
			mediadevices.WithVideoEncoders(&x264Params),
		),
	})
}

// addTracks adds media tracks to peer connection and starts RTCP drainage
func (c *Client) addTracks() error {
	// Add audio tracks
	audioTracks := c.mediaStream.GetAudioTracks()
	if len(audioTracks) > 0 {
		sender, err := c.pc.AddTrack(audioTracks[0])
		if err != nil {
			return wrapError("adding audio track", err)
		}
		logf("MEDIA", "Added mediadevices audio track")
		go c.drainRTCP(sender, "audio")
	}

	// Add video tracks
	videoTracks := c.mediaStream.GetVideoTracks()
	if len(videoTracks) > 0 {
		sender, err := c.pc.AddTrack(videoTracks[0])
		if err != nil {
			return wrapError("adding video track", err)
		}
		logf("MEDIA", "Added mediadevices video track")
		go c.drainRTCP(sender, "video")
	}

	return nil
}

func (c *Client) setupMediaStream(config Config) error {
	if c.mediaStream != nil {
		return nil
	}

	var err error
	c.mediaStream, err = createMediaStream(config)
	if err != nil {
		return wrapError("media stream creation", err)
	}
	log.Printf("MediaStream created successfully with audiotest and videotest")
	return nil
}

func (c *Client) Connect(config Config) error {
	var err error

	// WebSocket connection
	c.conn, _, err = (&websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}).Dial(config.WSSUrl, nil)
	if err != nil {
		return wrapError("websocket connection", err)
	}

	// Setup WebRTC
	me := &webrtc.MediaEngine{}
	if err := registerCodecs(me); err != nil {
		return err
	}

	se := webrtc.SettingEngine{}
	se.SetICETimeouts(ICETimeout, ICETimeout, ICEKeepalive)

	c.pc, err = webrtc.NewAPI(
		webrtc.WithMediaEngine(me),
		webrtc.WithSettingEngine(se),
	).NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	})
	if err != nil {
		return wrapError("peer connection creation", err)
	}

	if err := c.addTracks(); err != nil {
		return err
	}

	// Setup event handlers
	c.pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		logf("ICE", "connection state: %s", state)
		if state == webrtc.ICEConnectionStateConnected {
			select {
			case c.connected <- true:
			default:
			}
		}
	})

	c.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Peer connection state: %s", state)
		if state == webrtc.PeerConnectionStateClosed {
			log.Printf("Peer connection closed")
		}
	})

	return c.establishConnection(config)
}

func (c *Client) drainRTCP(sender *webrtc.RTPSender, senderType string) {
	for {
		if _, _, err := sender.ReadRTCP(); err != nil {
			logf("RTCP", "drain stopped for %s sender: %v", senderType, err)
			return
		}
	}
}

func (c *Client) establishConnection(config Config) error {
	offer, err := c.pc.CreateOffer(nil)
	if err != nil {
		return wrapError("offer creation", err)
	}

	if err = c.pc.SetLocalDescription(offer); err != nil {
		return wrapError("setting local description", err)
	}

	// Send offer to Wowza
	msg := WowzaMessage{
		Direction: "publish",
		Command:   "sendOffer",
		StreamInfo: StreamInfo{
			ApplicationName: config.AppName,
			StreamName:      config.StreamKey,
		},
		SDP: SDPMessage{SDP: offer.SDP, Type: "offer"},
	}

	timeout := config.ConnectTimeout
	if timeout == 0 {
		timeout = DefaultConnTimeout
	}

	c.conn.SetWriteDeadline(time.Now().Add(timeout))
	if err = c.conn.WriteJSON(msg); err != nil {
		return wrapError("sending offer", err)
	}

	// Receive answer from Wowza
	c.conn.SetReadDeadline(time.Now().Add(timeout))
	var answer WowzaMessage
	if err = c.conn.ReadJSON(&answer); err != nil {
		return wrapError("receiving answer", err)
	}

	if answer.Status > 400 {
		return fmt.Errorf("wowza error response: %s", answer.StatusDescription)
	}
	if answer.SDP.SDP == "" {
		return fmt.Errorf("received empty SDP from wowza")
	}

	if err = c.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answer.SDP.SDP,
	}); err != nil {
		return wrapError("setting remote description", err)
	}

	// Add ICE candidates
	logf("ICE", "Received %d candidates from Wowza", len(answer.ICECandidates))
	for _, candidate := range answer.ICECandidates {
		if candidate.SDPMLineIndex < 0 {
			logf("ICE", "Invalid SDPMLineIndex: %d, skipping candidate", candidate.SDPMLineIndex)
			continue
		}
		
		// Log candidate in human-readable format
		candidateInfo := parseICECandidate(candidate.Candidate)
		logf("ICE", "Candidate: %s (mid:%s)", candidateInfo, candidate.SDPMid)
		
		mLineIndex := uint16(candidate.SDPMLineIndex)
		if err := c.pc.AddICECandidate(webrtc.ICECandidateInit{
			Candidate:     candidate.Candidate,
			SDPMid:        &candidate.SDPMid,
			SDPMLineIndex: &mLineIndex,
		}); err != nil {
			logf("ICE", "Adding candidate failed: %v", err)
		}
	}

	return nil
}

func (c *Client) StartStreaming() error {
	log.Printf("Starting video streaming...")
	return nil
}

func (c *Client) Stop() {
	c.once.Do(func() {
		log.Printf("Stopping client...")

		ctx, cancel := context.WithTimeout(context.Background(), CleanupTimeout)
		defer cancel()

		var wg sync.WaitGroup
		cleanupTasks := []func(){
			func() {
				if c.mediaStream != nil {
					for _, track := range c.mediaStream.GetTracks() {
						track.Close()
					}
					if closer, ok := c.mediaStream.(interface{ Close() error }); ok {
						if err := closer.Close(); err != nil {
							log.Printf("mediaStream.Close error: %v", err)
						}
					}
				}
			},
			func() {
				if c.pc != nil {
					if err := c.pc.Close(); err != nil {
						log.Printf("pc.Close error: %v", err)
					}
				}
			},
			func() {
				if c.conn != nil {
					c.conn.SetWriteDeadline(time.Now().Add(WriteDeadlineBuffer))
					if err := c.conn.WriteMessage(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
						log.Printf("websocket WriteMessage error: %v", err)
					}
					if err := c.conn.Close(); err != nil {
						log.Printf("websocket Close error: %v", err)
					}
				}
			},
		}

		for _, task := range cleanupTasks {
			wg.Add(1)
			go func(task func()) {
				defer wg.Done()
				task()
			}(task)
		}

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			log.Printf("Client stopped gracefully")
		case <-ctx.Done():
			log.Printf("Client stop timeout - forcing exit")
		}
	})
}

func main() {
	config := Config{}
	flag.StringVar(&config.WSSUrl, "wss", "", "Wowza WebRTC WSS URL")
	flag.StringVar(&config.AppName, "app", "", "Application name")
	flag.StringVar(&config.StreamKey, "stream", "", "Stream key")
	flag.IntVar(&config.VideoBitrate, "bitrate", DefaultVideoBitrate, "Video bitrate in kbps")
	flag.DurationVar(&config.ConnectTimeout, "connect-timeout", DefaultConnTimeout, "WebSocket connection timeout")

	var timeSeconds int
	var timeoutSeconds int
	flag.IntVar(&timeSeconds, "time", 0, "Test duration in seconds (0 = no limit)")
	flag.IntVar(&timeoutSeconds, "timeout", DefaultConnectionTimeout, "Connection timeout in seconds")
	flag.Parse()

	config.TestDuration = time.Duration(timeSeconds) * time.Second
	config.ConnectionTimeout = time.Duration(timeoutSeconds) * time.Second

	if config.WSSUrl == "" || config.AppName == "" || config.StreamKey == "" {
		log.Fatal("Required parameters: -wss, -app, and -stream")
	}

	client := NewClient()

	// Signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		fmt.Println("WEBRTC_STATUS=INTERRUPTED")
		go func() {
			time.Sleep(ForceExitDelay)
			log.Printf("Force exit - cleanup taking too long")
			os.Exit(130)
		}()
		client.Stop()
		os.Exit(130)
	}()

	// Setup media stream once before retries
	if err := client.setupMediaStream(config); err != nil {
		log.Fatalf("Media stream setup failed: %v", err)
	}

	// Connection with exponential backoff
	const maxRetries = MaxRetries
	baseDelay := BaseRetryDelay
	var lastError error

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			// Close previous connection attempt
			if client.pc != nil {
				client.pc.Close()
				client.pc = nil
			}
			if client.conn != nil {
				client.conn.Close()
				client.conn = nil
			}

			multiplier := 1 << (i - 1)
			backoffDelay := time.Duration(float64(baseDelay) * float64(multiplier))
			jitter := time.Duration(rand.Float64() * float64(backoffDelay) * JitterMultiplier)
			if rand.Float64() < 0.5 {
				jitter = -jitter
			}
			delay := backoffDelay + jitter
			log.Printf("Retry attempt %d/%d after %v", i+1, maxRetries, delay)
			time.Sleep(delay)
		}

		if err := client.Connect(config); err != nil {
			log.Printf("Connection attempt failed: %v", err)
			lastError = err
			continue
		}
		lastError = nil
		break
	}

	if lastError != nil {
		log.Fatalf("Failed to connect after %d attempts: %v", maxRetries, lastError)
	}

	if err := client.StartStreaming(); err != nil {
		client.Stop()
		log.Fatalf("Streaming failed: %v", err)
	}

	// Wait for connection or timeout
	select {
	case <-client.connected:
		fmt.Println("WEBRTC_STATUS=CONNECTED")

		// If test duration is specified, disconnect after that time
		if config.TestDuration > 0 {
			select {
			case <-time.After(config.TestDuration):
				log.Printf("Test duration completed (%v), stopping...", config.TestDuration)
				client.Stop()
				os.Exit(0)
			case <-sigChan:
				// Signal handler will take care of cleanup
			}
		} else {
			// Block indefinitely until interrupted
			select {}
		}

	case <-time.After(config.ConnectionTimeout):
		fmt.Println("WEBRTC_STATUS=TIMEOUT")
		client.Stop()
		os.Exit(124)
	}
}
