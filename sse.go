// Package sse provides helpers for Server-Sent Events (SSE) using Gin.
// It supports broadcasting, targeted events, replay via Last-Event-ID, and graceful shutdown.
package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/dan-sherwin/go-rest-api-server/restresponse"
	"github.com/gin-gonic/gin"
)

type (
	sseEvent struct {
		ID         int       `json:"id"`
		Event      string    `json:"event"`
		Data       string    `json:"data"`
		sent       time.Time `json:"-"`
		sessionIDs []any     `json:"-"`
	}
	// Session represents a single server-sent events stream bound to a logical sessionID and uid.
	// Use NewSSESession to construct and Start to run the stream lifecycle.
	Session struct {
		event     chan *sseEvent
		shutDown  chan bool
		sessionID any
		uid       string
	}
)

var (
	sseSessions   = []*Session{}
	events        = map[int]*sseEvent{}
	lastEventID   = 0
	eventMutex    = &sync.Mutex{}
	sessionsMutex = &sync.RWMutex{}
)

func sendNewEventViaUids(eventType string, data any, uids []string) error {
	sessionIDs := []any{}
	sessionsMutex.RLock()
	for _, sess := range sseSessions {
		if slices.Contains(uids, sess.uid) && !slices.Contains(sessionIDs, sess.sessionID) {
			sessionIDs = append(sessionIDs, sess.sessionID)
		}
	}
	sessionsMutex.RUnlock()
	event, err := createEvent(eventType, data, sessionIDs)
	if err != nil {
		return err
	}
	sessionsMutex.RLock()
	sessions := append([]*Session(nil), sseSessions...)
	sessionsMutex.RUnlock()
	for _, sess := range sessions {
		if uids != nil {
			if !slices.Contains(uids, sess.uid) {
				continue
			}
		}
		sess.event <- event
	}
	return nil
}

func sendNewEvent(eventType string, data any, sessionIDs []any) error {
	event, err := createEvent(eventType, data, sessionIDs)
	if err != nil {
		return err
	}
	sessionsMutex.RLock()
	sessions := append([]*Session(nil), sseSessions...)
	sessionsMutex.RUnlock()
	for _, sess := range sessions {
		if sessionIDs != nil {
			if !slices.Contains(sessionIDs, sess.sessionID) {
				continue
			}
		}
		sess.event <- event
	}
	return nil
}

func createEvent(eventType string, data any, sessionIDs []any) (*sseEvent, error) {
	dataB, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	dataStr := string(dataB)
	eventMutex.Lock()
	lastEventID++
	event := &sseEvent{
		ID:         lastEventID,
		Event:      eventType,
		Data:       dataStr,
		sent:       time.Now(),
		sessionIDs: sessionIDs,
	}
	events[lastEventID] = event
	eventMutex.Unlock()
	return event, nil
}

// ShutdownBySessionID gracefully closes the SSE stream for a single session.
func ShutdownBySessionID(sessionID any) {
	ShutdownBySessionIDs([]any{sessionID})
}

// ShutdownBySessionIDs gracefully closes the SSE streams for the provided sessionIDs.
func ShutdownBySessionIDs(sessionIDs []any) {
	sessionsMutex.RLock()
	defer sessionsMutex.RUnlock()
	for _, sess := range sseSessions {
		if slices.Contains(sessionIDs, sess.sessionID) {
			sess.shutDown <- true
		}
	}
}

// BroadcastEventExceptUids sends an SSE event to all connected sessions except those
// whose uid is present in exceptUids.
func BroadcastEventExceptUids(eventType string, data any, exceptUids []string) error {
	uids := []string{}
	sessionsMutex.RLock()
	for _, sess := range sseSessions {
		if !slices.Contains(exceptUids, sess.uid) {
			uids = append(uids, sess.uid)
		}
	}
	sessionsMutex.RUnlock()
	return sendNewEventViaUids(eventType, data, uids)
}

// BroadcastEvent sends an SSE event with the given type and JSON-encoded data to all sessions.
func BroadcastEvent(eventType string, data any) error {
	return sendNewEvent(eventType, data, nil)
}

// SendSessionEvent sends an event only to the specified sessionID.
func SendSessionEvent(eventType string, data any, sessionID any) error {
	return sendNewEvent(eventType, data, []any{sessionID})
}

// SendSessionsEvent sends an event to multiple specific sessionIDs.
func SendSessionsEvent(eventType string, data any, sessionIDs []any) error {
	return sendNewEvent(eventType, data, sessionIDs)
}

// HasActiveSessionForUID reports whether there is at least one active SSE session for the given uid.
func HasActiveSessionForUID(uid string) bool {
	if uid == "" {
		return false
	}
	sessionsMutex.RLock()
	defer sessionsMutex.RUnlock()
	for _, sess := range sseSessions {
		if sess.uid == uid {
			return true
		}
	}
	return false
}

// HasActiveSessionsForSessionID reports whether there is at least one active SSE session for the given sessionId.
func HasActiveSessionsForSessionID(sessionID any) bool {
	sessionsMutex.RLock()
	defer sessionsMutex.RUnlock()
	for _, sess := range sseSessions {
		if sess.sessionID == sessionID {
			return true
		}
	}
	return false
}

// NewSSESession upgrades the HTTP connection to an SSE stream bound to the provided sessionID.
// It mirrors CORS headers, emits an initial retry hint, replays missed events based on Last-Event-ID,
// and then starts the session loop until the client disconnects or shutdown is requested.
func NewSSESession(c *gin.Context, sessionID any) {
	uid := c.Query("uid")
	if uid == "" {
		restresponse.RestErrorRespond(c, restresponse.BadRequest, "Missing uid query parameter")
		return
	}
	eventsToSend := []*sseEvent{}
	lastIDstr := c.GetHeader("Last-Event-ID")
	if lastIDstr != "" {
		if lastID, err := strconv.Atoi(lastIDstr); err == nil {
			eventMutex.Lock()
			snapshotLastID := lastEventID
			if lastID < snapshotLastID {
				for i := lastID + 1; i <= snapshotLastID; i++ {
					if event, ok := events[i]; ok {
						if event.sessionIDs != nil {
							if !slices.Contains(event.sessionIDs, sessionID) {
								continue
							}
						}
						eventsToSend = append(eventsToSend, event)
					}
				}
			}
			eventMutex.Unlock()
		}
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		restresponse.RestErrorRespond(c, restresponse.Internal, "SSE unsupported")
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", c.Request.Header.Get("Origin"))
	c.Header("Access-Control-Allow-Credentials", "true")

	// Tell client to wait 15s before reconnect attempts
	if _, err := fmt.Fprintf(c.Writer, "retry: 15000\n\n"); err != nil {
		// If we cannot write, the connection is likely unusable; abort early.
		return
	}
	flusher.Flush()
	session := &Session{
		event:     make(chan *sseEvent, len(eventsToSend)+1000),
		shutDown:  make(chan bool, 1),
		sessionID: sessionID,
		uid:       uid,
	}
	for _, event := range eventsToSend {
		session.event <- event
	}
	sessionsMutex.Lock()
	sseSessions = append(sseSessions, session)
	sessionsMutex.Unlock()
	err := session.Start(c)
	if err != nil {
		restresponse.RestErrorRespond(c, restresponse.Internal, "SSE error")
	}
}

// Start begins the SSE session loop: sending heartbeats and queued events until
// the client disconnects or the session is shut down.
func (s *Session) Start(c *gin.Context) error {
	// Heartbeat ticker for keeping the connection alive
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	ctx := c.Request.Context()
	flusher := c.Writer.(http.Flusher)

	// Ensure session is removed from the global slice on any exit path
	defer func() {
		sessionsMutex.Lock()
		for i, ss := range sseSessions {
			if ss == s {
				sseSessions = append(sseSessions[:i], sseSessions[i+1:]...)
				break
			}
		}
		sessionsMutex.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			// Context canceled (client disconnected or server shut down)
			return nil

		case <-ticker.C:
			if _, err := fmt.Fprintf(c.Writer, ": keep-alive\n\n"); err != nil {
				// Client likely disconnected; exit and cleanup via defer
				return nil
			}
			flusher.Flush()

		case event := <-s.event:
			if _, err := fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Event, event.Data); err != nil {
				// Write error indicates a broken connection
				return nil
			}
			flusher.Flush()

		case <-s.shutDown:
			return nil
		}
	}
}
