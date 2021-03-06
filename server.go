package link

import (
	"net"
	"sync"
	"sync/atomic"
)

// Default send chan buffer size for sessions.
var DefaultSendChanSize uint = 1024

// Server.
type Server struct {
	// About network
	listener net.Listener
	protocol PacketProtocol
	writer   PacketWriter

	// About sessions
	sendChanSize uint
	maxSessionId uint64
	sessions     map[uint64]*Session
	sessionMutex sync.Mutex

	// About server start and stop
	stopChan chan int
	stopFlag int32
	stopWait *sync.WaitGroup

	// Put your server state here.
	State interface{}
}

// Create a server.
func NewServer(listener net.Listener, protocol PacketProtocol) *Server {
	return &Server{
		listener:     listener,
		protocol:     protocol,
		writer:       protocol.NewWriter(),
		sendChanSize: DefaultSendChanSize,
		maxSessionId: 0,
		sessions:     make(map[uint64]*Session),
		stopChan:     make(chan int),
		stopWait:     new(sync.WaitGroup),
		stopFlag:     -1,
	}
}

// Get listener address.
func (server *Server) Listener() net.Listener {
	return server.listener
}

// Set session send channel buffer size setting.
// New setting will effect on new sessions.
func (server *Server) SetSendChanSize(size uint) {
	server.sendChanSize = size
}

// Get current session send chan buffer size setting.
func (server *Server) GetSendChanSize() uint {
	return server.sendChanSize
}

// Handle incoming connections. The callback will called asynchronously when each session start.
func (server *Server) Handle(callback func(*Session)) {
	if atomic.CompareAndSwapInt32(&server.stopFlag, -1, 0) {
		server.acceptLoop(callback)
	} else {
		panic(ServerDuplicateStartError)
	}
}

// Stop server.
func (server *Server) Stop() {
	if atomic.CompareAndSwapInt32(&server.stopFlag, 0, 1) {
		// if stop server without this goroutine
		// deadlock will happen when server closed by session.
		go func() {
			// wait for accept loop exit
			server.listener.Close()
			<-server.stopChan

			// close all sessions
			server.closeSessions()
		}()
	}
}

// Loop and accept connections until get an error.
func (server *Server) acceptLoop(callback func(*Session)) {
	defer func() {
		close(server.stopChan)
		server.Stop()

		// wait for all session exit
		server.stopWait.Wait()
	}()

	for {
		conn, err := server.listener.Accept()
		if err != nil {
			break
		}
		go server.startSession(conn, callback)
	}
}

// Start a session to present the connection.
func (server *Server) startSession(conn net.Conn, callback func(*Session)) {
	session := NewSession(
		atomic.AddUint64(&server.maxSessionId, 1),
		conn,
		server.protocol,
		server.sendChanSize,
	)
	session.server = server

	// init the session state
	if callback != nil {
		callback(session)
	}

	// session maybe closed in start callback
	if !session.IsClosed() {
		server.putSession(session)
		session.Start()
	}
}

// Put a session into session list
func (server *Server) putSession(session *Session) {
	if atomic.LoadInt32(&server.stopFlag) == 0 {
		server.sessionMutex.Lock()
		defer server.sessionMutex.Unlock()

		server.sessions[session.id] = session
	}

	server.stopWait.Add(1)
}

// Delete a session from session list
func (server *Server) delSession(session *Session) {
	if atomic.LoadInt32(&server.stopFlag) == 0 {
		server.sessionMutex.Lock()
		defer server.sessionMutex.Unlock()

		delete(server.sessions, session.id)
	}

	server.stopWait.Done()
}

// Close all sessions.
func (server *Server) closeSessions() {
	server.sessionMutex.Lock()
	defer server.sessionMutex.Unlock()

	for _, session := range server.sessions {
		session.Close()
	}
}
