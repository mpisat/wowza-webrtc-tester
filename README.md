# Wowza WebRTC Tester

A Go testing cli tool for Wowza Streaming Engine WebRTC functionality. Uses Pion's synthetic test drivers to generate consistent audio/video streams for comprehensive testing scenarios including stress testing, integration validation, and automated monitoring.

## Features

- **Test Signal Generation**: Uses Pion's audiotest and videotest drivers for deterministic test streams
- **WebRTC Testing**: Validates Wowza WebRTC publishing functionality with H.264/Opus streams
- **Stress Testing Capable**: Designed for high-frequency connection testing and load validation
- **Integration Testing**: Automated testing for CI/CD pipelines and deployment validation
- **Monitoring Support**: Periodic health checks and availability testing
- **Robust Retry Logic**: Exponential backoff with jitter (5 retries, 3s base delay, 75% jitter) for reliable test execution
- **Status Reporting**: Machine-readable status output for test automation

## Testing Use Cases

- **Stress Testing**: Launch multiple instances to test Wowza under load
- **Integration Testing**: Validate WebRTC functionality in CI/CD pipelines  
- **Availability Monitoring**: Periodic connection tests for uptime validation
- **Performance Benchmarking**: Measure connection establishment times and throughput
- **Configuration Validation**: Test different Wowza configurations and stream parameters
- **Network Testing**: Validate WebRTC connectivity across different network conditions

## Requirements

### System Dependencies

**Linux (Ubuntu/Debian):**
```bash
sudo apt-get update
sudo apt-get install -y \
    build-essential \
    pkg-config \
    libopus-dev \
    libx264-dev \
    libavcodec-dev \
    libavformat-dev
```

**macOS:**
```bash
brew install opus x264 ffmpeg pkg-config
```

**Alpine Linux:**
```bash
apk add --no-cache gcc g++ musl-dev pkgconfig opus-dev x264-dev ffmpeg-dev
```

### Go Requirements

- Go 1.24+ with CGO enabled
- Build tags: `cgo`

## Installation

### Local Build

1. **Clone and navigate:**
   ```bash
   git clone https://github.com/mpisat/wowza-webrtc-tester.git
   cd wowza-webrtc-tester
   ```

2. **Install dependencies:**
   ```bash
   go mod download
   ```

3. **Build:**
   ```bash
   CGO_ENABLED=1 go build -o wowza-webrtc-tester .
   ```

### Docker Build

1. **Build image:**
   ```bash
   docker build -t wowza-webrtc-tester .
   ```

2. **Run container:**
   ```bash
   docker run --rm wowza-webrtc-tester \
     -wss "wss://your-wowza.com/webrtc-session.json" \
     -app "webrtc/your-app" \
     -stream "your-stream?token=your-token"
   ```

## Usage

### Command Line Options

```bash
./wowza-webrtc-tester [OPTIONS]

Required:
  -wss string          Wowza WebRTC WSS URL
  -app string          Application name (e.g., "webrtc/myapp")  
  -stream string       Stream key with optional token

Optional:
  -bitrate int         Video bitrate in kbps (default 2000)
  -connect-timeout     WebSocket connection timeout (default 10s)
  -time int            Test duration in seconds, 0 = no limit (default 0)
  -timeout int         Connection timeout in seconds (default 15)
```

### Parameter Details

**-time** (Test Duration):
- Specifies how long to maintain the WebRTC connection after successful connection
- `0` (default): Run indefinitely until interrupted (Ctrl+C)
- `> 0`: Disconnect automatically after specified seconds
- Useful for automated testing scenarios and benchmarking

**-timeout** (Connection Timeout):
- Maximum time to wait for initial WebRTC connection establishment
- `15` (default): Wait up to 15 seconds for connection
- Applied to each retry attempt independently
- If connection isn't established within timeout, exits with code 124

### Examples

**Basic Testing:**
```bash
./wowza-webrtc-tester \
  -wss "wss://engine.example.com/webrtc-session.json" \
  -app "webrtc/live" \
  -stream "mystream"
```

**With Authentication Token:**
```bash
./wowza-webrtc-tester \
  -wss "wss://engine.example.com/webrtc-session.json" \
  -app "webrtc/live" \
  -stream "mystream?token=abc123"
```

**Stress Testing with Custom Bitrate:**
```bash
./wowza-webrtc-tester \
  -wss "wss://engine.example.com/webrtc-session.json" \
  -app "webrtc/live" \
  -stream "mystream" \
  -bitrate 3000
```

**Timed Test (30 seconds):**
```bash
./wowza-webrtc-tester \
  -wss "wss://engine.example.com/webrtc-session.json" \
  -app "webrtc/live" \
  -stream "mystream" \
  -time 30
```

**Quick Connection Test (5 second timeout):**
```bash
./wowza-webrtc-tester \
  -wss "wss://engine.example.com/webrtc-session.json" \
  -app "webrtc/live" \
  -stream "mystream" \
  -timeout 5 \
  -time 10
```

### Docker Examples

**Basic Docker Testing:**
```bash
docker run --rm wowza-webrtc-tester \
  -wss "wss://engine.example.com/webrtc-session.json" \
  -app "webrtc/live" \
  -stream "mystream?token=abc123"
```

**Load Testing with Custom Bitrate:**
```bash
docker run --rm wowza-webrtc-tester \
  -wss "wss://engine.example.com/webrtc-session.json" \
  -app "webrtc/live" \
  -stream "mystream" \
  -bitrate 1500
```

**Automated Testing (60 second test, 10 second timeout):**
```bash
docker run --rm wowza-webrtc-tester \
  -wss "wss://engine.example.com/webrtc-session.json" \
  -app "webrtc/live" \
  -stream "mystream?token=abc123" \
  -timeout 10 \
  -time 60
```

## Status Codes

The tester outputs status messages for test automation:

- `WEBRTC_STATUS=CONNECTED` - Successfully connected and streaming
- `WEBRTC_STATUS=INTERRUPTED` - Gracefully interrupted (Ctrl+C)  
- `WEBRTC_STATUS=TIMEOUT` - Connection timeout (exit code 124)

**Exit Codes:**
- `0` - Success
- `124` - Connection timeout
- `130` - Interrupted (Ctrl+C)
- Other - Various error conditions

## Test Media Configuration

### Audio (Opus)
- **Codec**: Opus
- **Sample Rate**: 48kHz
- **Channels**: Stereo (2)
- **Latency**: 10ms frames
- **Source**: Pion audiotest driver (synthetic sine wave)

### Video (H.264)
- **Codec**: H.264 Baseline Profile
- **Resolution**: 640x480 (VGA)
- **Preset**: ultrafast (for real-time testing)
- **Bitrate**: 2000kbps (configurable)
- **Frame Rate**: 30fps
- **Source**: Pion videotest driver (synthetic test pattern)

The synthetic test sources provide deterministic, consistent media streams ideal for automated testing and benchmarking scenarios.

## Retry Logic

The tester includes robust retry logic with exponential backoff and jitter to handle temporary connection failures:

### Parameters
- **Max Retries**: 5 attempts total (1 initial + 4 retries)
- **Base Delay**: 3 seconds
- **Exponential Backoff**: Each retry doubles the delay (3s, 6s, 12s, 24s)
- **Jitter**: ±75% random variance to prevent thundering herd
- **Per-attempt Timeout**: Configured via `-timeout` flag (default 15s)

Total retry window: Up to ~60s + (timeout × 5 attempts)

## Troubleshooting

### Common Issues

**Connection timeout:**
- Check Wowza WebRTC module is enabled
- Verify WSS URL is accessible

**Build errors:**
- Ensure CGO_ENABLED=1
- Install system dependencies (opus, x264)
- Check pkg-config can find libraries

**ICE connection failures:**
- Check firewall/NAT settings  
- Verify UDP port accessibility

### Debug Mode

Enable verbose logging by modifying the source:
```go
// Add to main() function
log.SetFlags(log.LstdFlags | log.Lshortfile)
```

## Performance

### Resource Usage
- **CPU**: Less than 0.5 cpu core for x264 and opus encoding
- **Memory**: ~20-30MB typical usage
- **Network**: Bitrate + ~10% WebRTC overhead

## License

MIT License, see LICENSE for details.
## Contributing

1. Fork the repository
2. Create feature branch
3. Make changes with tests
4. Submit pull request

## Support

For issues related to:
- **WebRTC**: Check browser compatibility and network configuration
- **Wowza**: Consult Wowza Streaming Engine documentation
- **Build Issues**: Ensure all system dependencies are installed