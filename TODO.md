Place where I write notes about what need to be done

HTTP/3 UDP Buffer Size Issue

The server is encountering a UDP buffer size limitation which is critical for HTTP/3 performance. The error message indicates that the system cannot allocate the requested UDP buffer size: wanted 7168 KiB but only got 416 KiB. This limitation occurs in the QUIC implementation used by HTTP/3.

The server's implementation in server.go attempts to configure the UDP buffer size through a two-step process:
1. First, it tries to increase the system-wide buffer size using the sysctl command (net.core.rmem_max)
2. If that fails, it attempts to set the buffer size directly on a test UDP connection
3. It then verifies the actual buffer size obtained using getUDPBufferSize
4. If the actual size is less than requested, it returns an error

The configuration shows that the server requests different buffer sizes in different contexts:
- Default configuration: 8MB (8 * 1024 * 1024 bytes)
- Test configuration: 20MB (20 * 1024 * 1024 bytes)

The issue manifests differently in GitHub Actions versus local development because:
1. GitHub Actions runs with restricted privileges, preventing sysctl modification
2. System default UDP buffer limits are lower in the CI environment
3. The server continues to run even when the desired buffer size cannot be achieved
4. The warning message comes from the QUIC implementation, not our server code directly

To properly address this, we need to:
1. Implement proper fallback behavior when requested buffer sizes cannot be achieved
2. Add explicit documentation about system requirements for optimal HTTP/3 performance
3. Consider making the buffer size requirements configurable with reasonable defaults
4. Add system capability detection to adjust QUIC parameters automatically

The current implementation assumes privileges that aren't always available and doesn't gracefully handle cases where the system cannot provide the requested resources. This needs to be made more robust for different deployment environments.
