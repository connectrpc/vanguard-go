# SSE Streaming Example

This example demonstrates server-streaming RPCs over REST using Server-Sent Events (SSE) with Vanguard.

The `StreamingService` provides several methods demonstrating different streaming scenarios:

1. **CountToTen**: Counts from 1 to 10 with configurable delay between numbers
2. **WatchBooks**: Simulates watching for book updates in a library shelf (with custom SSE event names and field omission)
3. **StreamVideo**: Streams binary data using `google.api.HttpBody` - works without SSE
4. **GetStatus**: Returns a single status response - standard unary GET request

## Running the Example

```bash
cd internal/examples/sse-streaming
go run main.go
```

The server starts on http://localhost:8080

## Testing with curl

```bash
# SSE streaming (structured messages) - REQUIRES Accept header
curl -N -H "Accept: text/event-stream" http://localhost:8080/v1/count

# Watch book updates with custom event names
curl -N -H "Accept: text/event-stream" "http://localhost:8080/v1/shelves/my-shelf/books:watch"

# HttpBody streaming (binary data) - NO special headers needed
curl http://localhost:8080/v1/videos/sample-video:stream

# Standard unary GET - NO special headers needed
curl http://localhost:8080/v1/status
```

See the main [README](../../../README.md) for SSE configuration details.
