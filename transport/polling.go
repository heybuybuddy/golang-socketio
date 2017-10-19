package transport

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	PlDefaultPingInterval   = 30 * time.Second
	PlDefaultPingTimeout    = 60 * time.Second
	PlDefaultReceiveTimeout = 60 * time.Second
	PlDefaultSendTimeout    = 60 * time.Second
)

type PollingTransportParams struct {
	Headers http.Header
}

type PollingConnection struct {
	transport *PollingTransport
	eventsIn  chan string
	eventsOut chan string
	errors    chan string
}

func (plc *PollingConnection) GetMessage() (string, error) {
	select {
	case <-time.After(plc.transport.ReceiveTimeout):
		return "", errors.New("Receive time out")
	case msg := <-plc.eventsIn:
		return msg, nil
	}
}

func (plc *PollingConnection) WriteMessage(message string) error {
	plc.eventsOut <- message
	select {
	case <-time.After(plc.transport.SendTimeout):
		return errors.New("Write time out")
	case errString := <-plc.errors:
		if errString != "0" {
			return errors.New(errString)
		}
	}
	return nil
}

func (plc *PollingConnection) Close() {

}

func (plc *PollingConnection) PingParams() (time.Duration, time.Duration) {
	return plc.transport.PingInterval, plc.transport.PingTimeout
}

// sessionMap describes sessions needed for identifying polling connections with socket.io connections
type sessionMap struct {
	sync.Mutex
	sessions map[string]*PollingConnection
}

// Set sets sid to polling connection tr
func (s *sessionMap) Set(sid string, tr *PollingConnection) {
	s.Lock()
	defer s.Unlock()
	s.sessions[sid] = tr
}

// Get returns polling connection if if exists, and bool existence flag
func (s *sessionMap) Get(sid string) (*PollingConnection, bool) {
	s.Lock()
	defer s.Unlock()
	tr, exists := s.sessions[sid]
	return tr, exists
}

type PollingTransport struct {
	PingInterval   time.Duration
	PingTimeout    time.Duration
	ReceiveTimeout time.Duration
	SendTimeout    time.Duration

	Headers  http.Header
	sessions sessionMap
}

func (plt *PollingTransport) Connect(url string) (Connection, error) {
	return nil, nil
}

func (plt *PollingTransport) HandleConnection(w http.ResponseWriter, r *http.Request) (Connection, error) {
	eventChan := make(chan string)
	eventOutChan := make(chan string)
	plc := &PollingConnection{
		transport: plt,
		eventsIn:  eventChan,
		eventsOut: eventOutChan,
		errors:    make(chan string),
	}

	return plc, nil
}

func (plt *PollingTransport) SetSid(sid string, conn Connection) {
	plt.sessions.Set(sid, conn.(*PollingConnection))
}

func (plt *PollingTransport) Serve(w http.ResponseWriter, r *http.Request) {
	sessionId := r.URL.Query().Get("sid")
	conn, exists := plt.sessions.Get(sessionId)
	switch r.Method {
	case http.MethodGet:
		if !exists {
			return
		}
		conn.PollingWriter(w, r)
	case http.MethodPost:
		bodyBytes, err := ioutil.ReadAll(r.Body)
		if err != nil {
			fmt.Println("error in PollingTransport.Serve():", err)
			return
		}
		bodyString := string(bodyBytes)
		index := strings.Index(bodyString, ":")
		body := bodyString[index+1:]
		setHeaders(w)
		w.Write([]byte("ok"))
		conn.eventsIn <- body
	}
}

/**
Returns polling transport with default params
*/
func GetDefaultPollingTransport() *PollingTransport {
	return &PollingTransport{
		PingInterval:   PlDefaultPingInterval,
		PingTimeout:    PlDefaultPingTimeout,
		ReceiveTimeout: PlDefaultReceiveTimeout,
		SendTimeout:    PlDefaultSendTimeout,
		sessions: sessionMap{
			Mutex:    sync.Mutex{},
			sessions: map[string]*PollingConnection{},
		},
		Headers: nil,
	}
}

func (plc *PollingConnection) PollingWriter(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	select {
	case <-time.After(plc.transport.PingTimeout):
		_, err := w.Write([]byte("1:3"))
		if err != nil {
			plc.errors <- err.Error()
			return
		}
		plc.errors <- "0"
	case events := <-plc.eventsOut:
		events = strconv.Itoa(len(events)) + ":" + events
		_, err := w.Write([]byte(events))
		if err != nil {
			plc.errors <- err.Error()
			return
		}
		plc.errors <- "0"
	}
}

func setHeaders(w http.ResponseWriter) {
	// We are going to return JSON no matter what:
	w.Header().Set("Content-Type", "application/json")
	// Don't cache response:
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1.
	w.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0.
	w.Header().Set("Expires", "0")                                         // Proxies.
}