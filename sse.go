package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/dan-sherwin/go-rest-api-server/rest_response"
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
	SSESession struct {
		event     chan *sseEvent
		shutDown  chan bool
		sessionID any
		uid       string
	}
)

var (
	sseSessions   = []*SSESession{}
	events        = map[int]*sseEvent{}
	lastEventID   = 0
	eventMutex    = &sync.Mutex{}
	sessionsMutex = &sync.RWMutex{}
)

func sendNewEventViaUids(eventType string, data any, uids []string) error {
	sessionIds := []any{}
	sessionsMutex.RLock()
	for _, sess := range sseSessions {
		if slices.Contains(uids, sess.uid) && !slices.Contains(sessionIds, sess.sessionID) {
			sessionIds = append(sessionIds, sess.sessionID)
		}
	}
	sessionsMutex.RUnlock()
	event, err := createEvent(eventType, data, sessionIds)
	if err != nil {
		return err
	}
	sessionsMutex.RLock()
	sessions := append([]*SSESession(nil), sseSessions...)
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

func sendNewEvent(eventType string, data any, sessionIds []any) error {
	event, err := createEvent(eventType, data, sessionIds)
	if err != nil {
		return err
	}
	sessionsMutex.RLock()
	sessions := append([]*SSESession(nil), sseSessions...)
	sessionsMutex.RUnlock()
	for _, sess := range sessions {
		if sessionIds != nil {
			if !slices.Contains(sessionIds, sess.sessionID) {
				continue
			}
		}
		sess.event <- event
	}
	return nil
}

func createEvent(eventType string, data any, sessionIds []any) (*sseEvent, error) {
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
		sessionIDs: sessionIds,
	}
	events[lastEventID] = event
	eventMutex.Unlock()
	return event, nil
}

func ShutdownBySessionId(sessionId any) {
	ShutdownBySessionIds([]any{sessionId})
}

func ShutdownBySessionIds(sessionIds []any) {
	sessionsMutex.RLock()
	defer sessionsMutex.RUnlock()
	for _, sess := range sseSessions {
		if slices.Contains(sessionIds, sess.sessionID) {
			sess.shutDown <- true
		}
	}
}

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

func BroadcastEvent(eventType string, data any) error {
	return sendNewEvent(eventType, data, nil)
}

func SendSessionEvent(eventType string, data any, sessionId any) error {
	return sendNewEvent(eventType, data, []any{sessionId})
}

func SendSessionsEvent(eventType string, data any, sessionIds []any) error {
	return sendNewEvent(eventType, data, sessionIds)
}

// HasActiveSessionForUid reports whether there is at least one active SSE session for the given uid.
func HasActiveSessionForUid(uid string) bool {
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

// HasActiveSessionsForSessionId reports whether there is at least one active SSE session for the given sessionId.
func HasActiveSessionsForSessionId(sessionId any) bool {
	sessionsMutex.RLock()
	defer sessionsMutex.RUnlock()
	for _, sess := range sseSessions {
		if sess.sessionID == sessionId {
			return true
		}
	}
	return false
}

func NewSSESession(c *gin.Context, sessionId any) {
	uid := c.Query("uid")
	if uid == "" {
		rest_response.RestErrorRespond(c, rest_response.BadRequest, "Missing uid query parameter")
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
							if !slices.Contains(event.sessionIDs, sessionId) {
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
		rest_response.RestErrorRespond(c, rest_response.Internal, "SSE unsupported")
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", c.Request.Header.Get("Origin"))
	c.Header("Access-Control-Allow-Credentials", "true")

	// Tell client to wait 15s before reconnect attempts
	fmt.Fprintf(c.Writer, "retry: 15000\n\n")
	flusher.Flush()
	session := &SSESession{
		event:     make(chan *sseEvent, len(eventsToSend)+1000),
		shutDown:  make(chan bool, 1),
		sessionID: sessionId,
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
		rest_response.RestErrorRespond(c, rest_response.Internal, "SSE error")
	}
}

func (s *SSESession) Start(c *gin.Context) error {
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
