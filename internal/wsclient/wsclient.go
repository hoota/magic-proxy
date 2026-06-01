package wsclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
	"socks5-ws-proxy/internal/logger"
	"socks5-ws-proxy/internal/protocol"
)

const writeBufSize = 256

type session struct {
	conn     net.Conn
	statusCh chan byte
}

type Client struct {
	wsURL    string
	insecure bool
	ws       *websocket.Conn
	sessions sync.Map
	nextID   atomic.Uint32
	ctx      context.Context
	cancel   context.CancelFunc
	writeCh  chan protocol.Frame
	ready    chan struct{}
}

func New(wsURL string, insecure bool) *Client {
	return &Client{
		wsURL:    wsURL,
		insecure: insecure,
		ready:    make(chan struct{}),
	}
}

func (c *Client) Connect(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	return c.dial()
}

func (c *Client) dial() error {
	dialCtx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()

	opts := &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: c.insecure,
				},
			},
		},
	}

	ws, _, err := websocket.Dial(dialCtx, c.wsURL, opts)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	ws.SetReadLimit(1 << 20)

	c.ws = ws
	c.writeCh = make(chan protocol.Frame, writeBufSize)
	close(c.ready)

	go c.writerPump()
	go c.readPump()

	logger.Info.Printf("connected to %s", c.wsURL)
	return nil
}

func (c *Client) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.ws != nil {
		c.ws.Close(websocket.StatusNormalClosure, "client shutdown")
	}
	c.sessions.Range(func(key, value interface{}) bool {
		s := value.(*session)
		s.conn.Close()
		return true
	})
}

func (c *Client) WaitReady() {
	<-c.ready
}

func (c *Client) OpenSession(browserConn net.Conn, addrType byte, addr string, port uint16) (uint32, error) {
	sid := c.nextID.Add(1)
	sess := &session{
		conn:     browserConn,
		statusCh: make(chan byte, 1),
	}
	c.sessions.Store(sid, sess)

	target, err := protocol.EncodeTarget(addrType, addr, port)
	if err != nil {
		c.sessions.Delete(sid)
		return 0, fmt.Errorf("encode target: %w", err)
	}

	if err := c.writeFrame(protocol.Frame{
		SessionID: sid,
		Type:      protocol.MsgOpen,
		Payload:   target,
	}); err != nil {
		c.sessions.Delete(sid)
		return 0, fmt.Errorf("write open: %w", err)
	}

	select {
	case status := <-sess.statusCh:
		if status != 0x00 {
			c.sessions.Delete(sid)
			return 0, fmt.Errorf("remote status 0x%02x", status)
		}
	case <-time.After(30 * time.Second):
		c.sessions.Delete(sid)
		return 0, fmt.Errorf("timeout waiting for status")
	case <-c.ctx.Done():
		c.sessions.Delete(sid)
		return 0, fmt.Errorf("client shutting down")
	}

	return sid, nil
}

func (c *Client) StartRelay(sid uint32, browserConn net.Conn) {
	val, ok := c.sessions.Load(sid)
	if !ok {
		return
	}
	sess := val.(*session)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.relayBrowserToWS(sid, sess)
	}()
	<-done
}

func (c *Client) writeFrame(f protocol.Frame) error {
	select {
	case c.writeCh <- f:
		return nil
	case <-c.ctx.Done():
		return fmt.Errorf("client shutting down")
	default:
		select {
		case c.writeCh <- f:
			return nil
		case <-time.After(10 * time.Second):
			return fmt.Errorf("write buffer full")
		case <-c.ctx.Done():
			return fmt.Errorf("client shutting down")
		}
	}
}

func (c *Client) writerPump() {
	for {
		select {
		case f, ok := <-c.writeCh:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			err := c.ws.Write(ctx, websocket.MessageBinary, f.Marshal())
			cancel()
			if err != nil {
				return
			}
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) readPump() {
	for {
		msgType, data, err := c.ws.Read(c.ctx)
		if err != nil {
			if !isClosedErr(err) {
				logger.Error.Printf("ws read error: %v", err)
			}
			c.reconnect()
			return
		}
		if msgType != websocket.MessageBinary {
			continue
		}

		frame, err := protocol.UnmarshalFrame(data)
		if err != nil {
			logger.Error.Printf("unmarshal frame: %v", err)
			continue
		}

		val, ok := c.sessions.Load(frame.SessionID)
		if !ok {
			continue
		}
		sess := val.(*session)

		switch frame.Type {
		case protocol.MsgStatus:
			if len(frame.Payload) > 0 {
				select {
				case sess.statusCh <- frame.Payload[0]:
				default:
				}
			}

		case protocol.MsgData:
			sess.conn.Write(frame.Payload)

		case protocol.MsgClose:
			sess.conn.Close()
			c.sessions.Delete(frame.SessionID)
		}
	}
}

func (c *Client) reconnect() {
	c.ws.Close(websocket.StatusInternalError, "reconnecting")
	close(c.writeCh)

	c.sessions.Range(func(key, value interface{}) bool {
		s := value.(*session)
		s.conn.Close()
		c.sessions.Delete(key)
		return true
	})

	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		logger.Info.Printf("reconnecting to %s (backoff %v)...", c.wsURL, backoff)

		c.ready = make(chan struct{})
		if err := c.dial(); err != nil {
			logger.Error.Printf("reconnect failed: %v", err)
			time.Sleep(backoff)
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		logger.Info.Printf("reconnected to %s", c.wsURL)
		return
	}
}

func (c *Client) relayBrowserToWS(sid uint32, sess *session) {
	buf := make([]byte, 32*1024)
	for {
		n, err := sess.conn.Read(buf)
		if n > 0 {
			payload := make([]byte, n)
			copy(payload, buf[:n])
			if werr := c.writeFrame(protocol.Frame{
				SessionID: sid,
				Type:      protocol.MsgData,
				Payload:   payload,
			}); werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}

	c.writeFrame(protocol.Frame{SessionID: sid, Type: protocol.MsgClose})
	c.sessions.Delete(sid)
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
