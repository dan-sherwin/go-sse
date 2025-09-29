package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// resetState clears global state; only used in tests.
func resetState() {
	// Stop any existing sessions by closing their shutdown channels to avoid leaks
	sessionsMutex.Lock()
	for _, s := range sseSessions {
		select {
		case s.shutDown <- true:
		default:
		}
	}
	sseSessions = []*SSESession{}
	sessionsMutex.Unlock()

	eventMutex.Lock()
	events = map[int]*sseEvent{}
	lastEventID = 0
	eventMutex.Unlock()
}

// helper to start a session and return its context cancel, recorder and done channel.
func startSession(t *testing.T, sessionID any, lastEventIDHeader string) (cancel context.CancelFunc, rec *httptest.ResponseRecorder, done chan struct{}) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	rec = httptest.NewRecorder()

	// Build request with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/sse", nil).WithContext(ctx)
	if lastEventIDHeader != "" {
		req.Header.Set("Last-Event-ID", lastEventIDHeader)
	}
	// Simulate CORS Origin header to test header mirroring
	req.Header.Set("Origin", "http://example.test")

	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	done = make(chan struct{})
	go func() {
		NewSSESession(c, sessionID)
		close(done)
	}()

	return cancel, rec, done
}

func readUntil(rec *httptest.ResponseRecorder, substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body.String(), substr) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestBroadcastAndReceive(t *testing.T) {
	resetState()
	cancel, rec, done := startSession(t, "sessA", "")
	defer func() {
		cancel()
		<-done
	}()

	// Give the session time to initialize
	time.Sleep(50 * time.Millisecond)

	// Send a broadcast event
	payload := map[string]string{"msg": "hello"}
	if err := BroadcastEvent("greeting", payload); err != nil {
		t.Fatalf("BroadcastEvent error: %v", err)
	}

	// Wait until the event appears in the body
	if !readUntil(rec, "event: greeting", 2*time.Second) {
		t.Fatalf("did not see event line; body=%q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "data: {\"msg\":\"hello\"}") {
		t.Fatalf("did not see data line; body=%q", rec.Body.String())
	}

	// Validate SSE headers
	headers := rec.Header()
	if ct := headers.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("unexpected Content-Type: %q", ct)
	}
	if acao := headers.Get("Access-Control-Allow-Origin"); acao != "http://example.test" {
		t.Fatalf("unexpected ACAO: %q", acao)
	}
	if acc := headers.Get("Access-Control-Allow-Credentials"); acc != "true" {
		t.Fatalf("unexpected ACC: %q", acc)
	}
}

func TestResumeWithLastEventID(t *testing.T) {
	resetState()
	// Start a first session and send targeted events, then cancel.
	cancel1, _, done1 := startSession(t, "user-1", "")
	time.Sleep(50 * time.Millisecond)
	if err := SendSessionEvent("note", map[string]int{"n": 1}, "user-1"); err != nil {
		t.Fatalf("SendSessionEvent 1 error: %v", err)
	}
	if err := SendSessionEvent("note", map[string]int{"n": 2}, "user-1"); err != nil {
		t.Fatalf("SendSessionEvent 2 error: %v", err)
	}
	// Capture lastEventID snapshot for header
	eventMutex.Lock()
	last := lastEventID
	eventMutex.Unlock()
	cancel1()
	<-done1

	// Now start a new session with Last-Event-ID set to last-1 to request replay of only the second event (n=2)
	cancel2, rec2, done2 := startSession(t, "user-1", strconv.Itoa(last-1))
	defer func() {
		cancel2()
		<-done2
	}()
	if !readUntil(rec2, "data: {\"n\":2}", 2*time.Second) {
		t.Fatalf("did not see replayed event with n=2; body=%q", rec2.Body.String())
	}
	if strings.Contains(rec2.Body.String(), "data: {\"n\":1}") {
		t.Fatalf("unexpected replay of n=1; body=%q", rec2.Body.String())
	}
}

func TestShutdownBySessionId(t *testing.T) {
	resetState()
	cancel, _, done := startSession(t, "to-close", "")
	// Give it time to initialize
	time.Sleep(50 * time.Millisecond)
	ShutdownBySessionId("to-close")
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatalf("session did not shut down in time")
	}
}
