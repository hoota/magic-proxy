package wsserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
	"socks5-ws-proxy/internal/logger"
	"socks5-ws-proxy/internal/protocol"
)

const writeBufSize = 256

type targetSession struct {
	conn net.Conn
}

type Server struct {
	listenAddr   string
	wsPath       string
	allowedPorts map[int]bool
	maxConns     int
	activeConns  atomic.Int64
	httpServer   *http.Server
}

type Config struct {
	ListenAddr   string
	WSPath       string
	AllowedPorts []int
	MaxConns     int
}

func New(cfg Config) *Server {
	allowed := make(map[int]bool)
	for _, p := range cfg.AllowedPorts {
		allowed[p] = true
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 100
	}
	return &Server{
		listenAddr:   cfg.ListenAddr,
		wsPath:       cfg.WSPath,
		allowedPorts: allowed,
		maxConns:     cfg.MaxConns,
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(s.wsPath, s.handleWebSocket)

	s.httpServer = &http.Server{
		Addr:    s.listenAddr,
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info.Printf("ws-proxy-server listening on %s, path %s", s.listenAddr, s.wsPath)
		if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) ActiveConnections() int64 {
	return s.activeConns.Load()
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		logger.Error.Printf("ws accept error: %v", err)
		return
	}
	defer ws.Close(websocket.StatusInternalError, "closing")

	ws.SetReadLimit(1 << 20)

	sessions := sync.Map{}
	writeCh := make(chan protocol.Frame, writeBufSize)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		for {
			select {
			case f, ok := <-writeCh:
				if !ok {
					return
				}
				wCtx, wCancel := context.WithTimeout(ctx, 10*time.Second)
				err := ws.Write(wCtx, websocket.MessageBinary, f.Marshal())
				wCancel()
				if err != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	writeFrame := func(f protocol.Frame) {
		select {
		case writeCh <- f:
		case <-time.After(10 * time.Second):
			logger.Error.Printf("write buffer full, dropping frame sid=%d type=0x%02x", f.SessionID, f.Type)
		case <-ctx.Done():
		}
	}

	closeSession := func(sid uint32, ts *targetSession) {
		if ts != nil {
			ts.conn.Close()
		}
		if _, loaded := sessions.LoadAndDelete(sid); loaded {
			s.activeConns.Add(-1)
		}
		writeFrame(protocol.Frame{SessionID: sid, Type: protocol.MsgClose})
	}

	for {
		msgType, data, err := ws.Read(ctx)
		if err != nil {
			if !isClosedErr(err) {
				logger.Error.Printf("ws read error: %v", err)
			}
			break
		}
		if msgType != websocket.MessageBinary {
			continue
		}

		frame, err := protocol.UnmarshalFrame(data)
		if err != nil {
			logger.Error.Printf("unmarshal frame: %v", err)
			continue
		}

		switch frame.Type {
		case protocol.MsgOpen:
			s.handleOpen(ctx, &sessions, frame, writeFrame, closeSession)

		case protocol.MsgData:
			val, ok := sessions.Load(frame.SessionID)
			if !ok {
				continue
			}
			ts := val.(*targetSession)
			ts.conn.Write(frame.Payload)

		case protocol.MsgClose:
			val, ok := sessions.LoadAndDelete(frame.SessionID)
			if !ok {
				continue
			}
			ts := val.(*targetSession)
			ts.conn.Close()
			s.activeConns.Add(-1)
		}
	}

	close(writeCh)

	sessions.Range(func(key, value interface{}) bool {
		ts := value.(*targetSession)
		ts.conn.Close()
		return true
	})
}

func (s *Server) handleOpen(ctx context.Context, sessions *sync.Map, frame protocol.Frame, writeFrame func(protocol.Frame), closeSession func(uint32, *targetSession)) {
	sid := frame.SessionID

	addrType, addr, port, err := protocol.DecodeTarget(frame.Payload)
	if err != nil {
		logger.Error.Printf("decode target (session %d): %v", sid, err)
		writeFrame(protocol.Frame{SessionID: sid, Type: protocol.MsgStatus, Payload: []byte{0x01}})
		return
	}

	if len(s.allowedPorts) > 0 && !s.allowedPorts[int(port)] {
		logger.Error.Printf("port %d not allowed (session %d)", port, sid)
		writeFrame(protocol.Frame{SessionID: sid, Type: protocol.MsgStatus, Payload: []byte{0x02}})
		return
	}

	logger.Info.Printf("session %d: connecting to %s:%d (type=0x%02x)", sid, addr, port, addrType)

	tcpConn, err := net.DialTimeout("tcp", net.JoinHostPort(addr, strconv.Itoa(int(port))), 30*time.Second)
	if err != nil {
		logger.Error.Printf("session %d: tcp connect to %s:%d failed: %v", sid, addr, port, err)
		status := mapTCPErrno(err)
		writeFrame(protocol.Frame{SessionID: sid, Type: protocol.MsgStatus, Payload: []byte{status}})
		return
	}

	s.activeConns.Add(1)
	sessions.Store(sid, &targetSession{conn: tcpConn})
	logger.Info.Printf("session %d: connected to %s:%d, active: %d", sid, addr, port, s.activeConns.Load())

	writeFrame(protocol.Frame{SessionID: sid, Type: protocol.MsgStatus, Payload: []byte{0x00}})

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := tcpConn.Read(buf)
			if n > 0 {
				payload := make([]byte, n)
				copy(payload, buf[:n])
				writeFrame(protocol.Frame{
					SessionID: sid,
					Type:      protocol.MsgData,
					Payload:   payload,
				})
			}
			if readErr != nil {
				break
			}
		}
		closeSession(sid, &targetSession{conn: tcpConn})
	}()
}

func mapTCPErrno(err error) byte {
	if netErr, ok := err.(*net.OpError); ok {
		msg := netErr.Error()
		switch {
		case strings.Contains(msg, "connection refused"):
			return 0x05
		case strings.Contains(msg, "network is unreachable"):
			return 0x03
		case strings.Contains(msg, "no route to host"):
			return 0x04
		}
	}
	return 0x01
}

func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return io.EOF == err ||
		strings.Contains(msg, "use of closed") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "WebSocket closed") ||
		strings.Contains(msg, "status")
}
