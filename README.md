# Go SSE

A lightweight, production-ready Server‑Sent Events (SSE) helper for Go with Gin. It provides:

- Broadcast and targeted events (to specific session IDs)
- Event replay using Last-Event-ID for missed events during reconnects
- Automatic heartbeats to keep connections alive
- Proper SSE and CORS headers
- Graceful server-side shutdown per session or by multiple session IDs

This package is designed to be simple to integrate, concurrency-safe, and easy to test.


## Requirements
- Go 1.24+
- Gin (github.com/gin-gonic/gin)


## Install

```
go get github.com/dan-sherwin/go-sse
```

Import:

```
import sse "github.com/dan-sherwin/go-sse"
```


## Quick Start

Server wiring with Gin:

```go
package main

import (
	"net/http"

	sse "github.com/dan-sherwin/go-sse"
	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	// SSE endpoint: binds a client connection to a logical session ID
	r.GET("/sse", func(c *gin.Context) {
		// You can derive sessionID from auth, query param, etc.
		sessionID := c.Query("session")
		if sessionID == "" {
			c.String(http.StatusBadRequest, "missing session")
			return
		}
		sse.NewSSESession(c, sessionID)
	})

	// Example: broadcast endpoint to send an event to everyone
	r.POST("/notify", func(c *gin.Context) {
		_ = sse.BroadcastEvent("notification", gin.H{"msg": "hello world"})
		c.Status(http.StatusAccepted)
	})

	// Example: targeted message
	r.POST("/pm", func(c *gin.Context) {
		sid := c.Query("session")
		_ = sse.SendSessionEvent("private", gin.H{"msg": "hi"}, sid)
		c.Status(http.StatusAccepted)
	})

	_ = r.Run(":8080")
}
```

Client example (browser):

```html
<script>
  const session = 'user-123';
  const es = new EventSource(`/sse?session=${encodeURIComponent(session)}`);

  es.onmessage = e => console.log('message', e.data);
  es.addEventListener('notification', e => console.log('notification', e.data));
  es.addEventListener('private', e => console.log('private', e.data));

  es.onerror = () => console.warn('SSE error, the server will signal retry interval');
</script>
```


## Behavior and Features

- SSE headers: Content-Type=text/event-stream, no-cache, keep-alive
- CORS: Access-Control-Allow-Origin mirrors the request Origin, and Access-Control-Allow-Credentials=true
- Retry hint: server sends `retry: 15000` (15s) advising client reconnect delay
- Heartbeats: periodic `: keep-alive` comments keep intermediaries from closing idle connections
- Replay: If the client reconnects with `Last-Event-ID`, missed events are replayed, including only those targeted to that session (if applicable)


## API

All functions are in the `sse` package.

- NewSSESession(c *gin.Context, sessionID any)
  - Upgrades the HTTP connection into an SSE stream for the provided logical `sessionID`.
  - Honors `Last-Event-ID` header to replay missed events since the provided event ID.

- BroadcastEvent(eventType string, data any) error
  - Sends an event with the given `eventType` and JSON-encoded `data` to all connected sessions.

- SendSessionEvent(eventType string, data any, sessionID any) error
  - Sends an event only to the specified `sessionID`.

- SendSessionsEvent(eventType string, data any, sessionIDs []any) error
  - Sends an event to multiple specific sessions.

- ShutdownBySessionId(sessionID any)
  - Gracefully closes the SSE stream for a single session.

- ShutdownBySessionIds(sessionIDs []any)
  - Gracefully closes the SSE streams for a set of sessions.

Event format:

```
id: <sequential-int>
event: <event-type>
data: <json>

```

Notes:
- `data` is JSON-encoded using the standard library.
- `event` is optional from the client perspective but used here for named listeners via `addEventListener`.


## Concurrency and Safety

- The package safely handles concurrent broadcasts and connections.
- Internal state is protected with mutexes. Sessions are snapshotted for iteration to prevent race conditions.
- Missed events are computed under lock, then queued to the session.


## Testing

This repository includes tests. To run them:

```
go test ./...
```

Tests cover broadcasting, replay with `Last-Event-ID`, and server-initiated shutdown.


## Limitations and Tips

- Memory: Events are kept in-memory to support replay; for long-running systems you may want to add eviction or persistence as needed.
- Session identity: `sessionID` is typed as `any` for flexibility. In practice prefer stable scalar types (string, int).
- Proxies: Ensure reverse proxies allow streaming and don’t buffer SSE (e.g., disable buffering in Nginx for the path).


## License

This project’s license is defined by the repository. If none is provided, treat usage according to your organization’s policy.