# SSE Streaming Example

This example demonstrates server-streaming RPCs over REST using Server-Sent Events (SSE) with Vanguard.

## What This Demonstrates

- **Server-Streaming RPCs**: Methods that stream multiple responses to the client
- **SSE Protocol**: Using `text/event-stream` content type for real-time updates
- **REST+SSE**: Accessing gRPC/Connect streaming methods via REST with SSE
- **Protocol Transcoding**: Vanguard automatically converts between Connect and REST+SSE

## The Service

The `StreamingService` provides several methods demonstrating different streaming scenarios:

### SSE Streaming Methods (require `Accept: text/event-stream`)
1. **CountToTen**: Counts from 1 to 10 with configurable delay between numbers
2. **WatchBooks**: Simulates watching for book updates in a library shelf

### HttpBody Streaming (works without SSE)
3. **StreamVideo**: Streams binary data using `google.api.HttpBody` - works with regular REST

### Standard Unary Method (no streaming)
4. **GetStatus**: Returns a single status response - regular REST GET request

All methods are defined with `google.api.http` annotations, making them accessible via REST.

## How SSE is Enabled

In the code, SSE support is enabled with one line:

```go
service := vanguard.NewService(
    path,
    handler,
    vanguard.WithRESTServerSentEvents(true), // Enable SSE!
)
```

This opts in to SSE for server-streaming methods accessed via REST.

## Running the Example

```bash
cd internal/examples/sse-streaming
go run main.go
```

The server starts on http://localhost:8080

## Quick Test Summary

```bash
# SSE streaming (structured messages) - REQUIRES Accept header
curl -N -H "Accept: text/event-stream" http://localhost:8080/v1/count

# HttpBody streaming (binary data) - NO special headers needed
curl http://localhost:8080/v1/videos/sample-video:stream

# Standard unary GET - NO special headers needed
curl http://localhost:8080/v1/status

# Without SSE header - FAILS for server-streaming RPCs
curl http://localhost:8080/v1/count
# Returns: 415 Unsupported Media Type
```

## Testing with curl

### Count to Ten
```bash
curl -N -H "Accept: text/event-stream" http://localhost:8080/v1/count
```

Output:
```
event: open

id: 1
event: message
data: {"number":1,"timestamp":"2025-09-30T12:00:00Z"}

id: 2
event: message
data: {"number":2,"timestamp":"2025-09-30T12:00:01Z"}

...

event: complete
data: {}
```

### Watch Books with Custom Delay
```bash
curl -N -H "Accept: text/event-stream" "http://localhost:8080/v1/count?delay_ms=2000"
```

### Watch Book Updates
```bash
curl -N -H "Accept: text/event-stream" "http://localhost:8080/v1/shelves/my-shelf/books:watch"
```

Output:
```
id: 1
event: message
data: {"type":"CREATED","bookName":"shelves/my-shelf/books/go-programming","timestamp":"..."}

id: 2
event: message
data: {"type":"CREATED","bookName":"shelves/my-shelf/books/distributed-systems","timestamp":"..."}

...
```

### Stream Video (HttpBody - No SSE Required)
```bash
curl http://localhost:8080/v1/videos/sample-video:stream
```

Output (binary chunks streamed directly):
```
CHUNK 1: [simulated video data...]
CHUNK 2: [more video data...]
CHUNK 3: [even more video data...]
CHUNK 4: [final video data...]
```

**Note**: HttpBody streaming works with regular REST (no SSE needed) because the response body type is already designed for streaming binary content.

### Get Status (Standard Unary Request)
```bash
curl http://localhost:8080/v1/status
```

Output:
```json
{
  "status": "healthy",
  "activeStreams": 0,
  "timestamp": "2025-09-30T12:00:00Z"
}
```

**Note**: This is a regular REST GET request - no streaming involved.

## SSE Event Types

Vanguard sends four types of SSE events:

1. **open**: Sent immediately when connection is established (before any messages)
2. **message**: Regular message data (with sequential IDs)
3. **error**: Error occurred (contains gRPC status)
4. **complete**: Stream finished successfully (includes trailers if any)

The **open** event is sent as soon as the SSE connection is established, allowing clients (like JavaScript EventSource) to detect when the stream is ready to receive messages.

## Streaming Types Comparison

### SSE Streaming (Server-Streaming RPCs)
- **Requires**: `Accept: text/event-stream` header + SSE enabled in Vanguard
- **Use Case**: Streaming structured protobuf messages (JSON over SSE)
- **Examples**: CountToTen, WatchBooks
- **Without SSE header**: Returns 415 Unsupported Media Type error

```bash
# This will fail - missing Accept header
curl http://localhost:8080/v1/count
# Error: stream type server not supported with REST protocol
```

### HttpBody Streaming
- **Requires**: Nothing special - works with regular REST
- **Use Case**: Streaming binary/unstructured data
- **Example**: StreamVideo
- **Format**: Direct streaming of bytes, no SSE framing

### Unary (Non-Streaming)
- **Requires**: Nothing special - standard REST
- **Use Case**: Single request → single response
- **Example**: GetStatus
- **Format**: Standard JSON response

## Protocol Detection

When a request arrives:
1. Client sends `Accept: text/event-stream` header
2. Vanguard checks if SSE is enabled for the service
3. If both conditions are met, response uses SSE format
4. Backend handler uses standard Connect streaming (unaware of SSE)

## Implementation Notes

- The backend service uses standard `connect.ServerStream` - no SSE-specific code needed
- Vanguard handles all SSE formatting (event framing, IDs, etc.)
- JSON is used for message encoding over SSE
- Binary data is automatically base64-encoded if needed
- Compression is handled at the message level, not SSE level
- Multiline data is properly formatted with multiple `data:` fields
- All line ending types are supported: LF (`\n`), CRLF (`\r\n`), and CR (`\r`)

## Key Takeaways

**When to use SSE:**
- You have server-streaming RPCs with **structured protobuf messages**
- You want to access them via REST (e.g., from browsers using EventSource API)
- You need event IDs, event types, and structured data fields

**When HttpBody streaming is better:**
- You're streaming **binary or unstructured data** (videos, files, logs)
- You don't need SSE event framing
- Works with standard HTTP streaming (no special headers)

**The key difference:**
- SSE wraps each message with `id:`, `event:`, and `data:` fields
- HttpBody just streams raw bytes directly
- Both support server streaming, but for different use cases
