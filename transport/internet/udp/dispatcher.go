package udp

import (
	"context"
	goerrors "errors"
	"io"
	"sync"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol/udp"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/common/signal/done"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"
)

type ResponseCallback func(ctx context.Context, packet *udp.Packet)

type connEntry struct {
	link        *transport.Link
	timer       *signal.ActivityTimer
	cancel      context.CancelFunc
	closed      bool
	maxDeadline time.Time
}

func (c *connEntry) Close() error {
	c.timer.SetTimeout(0)
	return nil
}

func (c *connEntry) terminate() {
	if c.closed {
		panic("terminate called more than once")
	}
	c.closed = true
	c.cancel()
	common.Interrupt(c.link.Reader)
	common.Interrupt(c.link.Writer)
}

type Dispatcher struct {
	sync.RWMutex
	conn          *connEntry
	dispatcher    routing.Dispatcher
	callback      ResponseCallback
	callClose     func() error
	closed        bool
	toleranceTime time.Duration
	connCreatedAt time.Time
	// sharedConn 表示一条连接被多次互不相关的请求共用，因此它的生命周期不能
	// 跟着创建它的那次请求走。详见 getInboundRay。
	sharedConn bool
}

func NewDispatcher(dispatcher routing.Dispatcher, callback ResponseCallback) *Dispatcher {
	return &Dispatcher{
		dispatcher: dispatcher,
		callback:   callback,
	}
}

// NewNoCacheDispatcher returns a Dispatcher that stops reusing a connection once it
// is older than toleranceTime, so a long-lived stream of requests keeps going through
// route selection instead of pinning itself to whichever outbound it first picked.
//
// Its connections are shared by unrelated requests, so they are detached from the
// context of the request that happened to create them.
func NewNoCacheDispatcher(dispatcher routing.Dispatcher, callback ResponseCallback, toleranceTime time.Duration) *Dispatcher {
	return &Dispatcher{
		dispatcher:    dispatcher,
		callback:      callback,
		toleranceTime: toleranceTime,
		sharedConn:    true,
	}
}

func (v *Dispatcher) RemoveRay() {
	v.Lock()
	defer v.Unlock()
	v.closed = true
	if v.conn != nil {
		v.conn.Close()
		v.conn = nil
	}
}

func (v *Dispatcher) getInboundRay(ctx context.Context, dest net.Destination) (*connEntry, error) {
	v.Lock()
	defer v.Unlock()

	if v.closed {
		return nil, errors.New("dispatcher is closed")
	}

	if v.conn != nil {
		if v.conn.closed {
			v.conn = nil
		} else if v.toleranceTime > 0 && time.Since(v.connCreatedAt) > v.toleranceTime {
			// 连接超过容忍时间，脱管旧连接让其自然消亡，新建连接走负载均衡
			oldConn := v.conn
			if !oldConn.maxDeadline.IsZero() {
				if remaining := time.Until(oldConn.maxDeadline) + v.toleranceTime; remaining > 0 {
					oldConn.timer.SetTimeout(remaining)
				} else {
					// deadline 已过，用过这条连接的请求都已放弃等待，没有在途响应
					// 需要保留。不主动关的话它会退回 1 分钟 inactivity 白等。
					oldConn.Close()
				}
			}
			// 没有 deadline 的情况不动 timer，保持原有 1 分钟 inactivity 超时
			v.conn = nil
		} else {
			return v.conn, nil
		}
	}

	errors.LogInfo(ctx, "establishing new connection for ", dest)

	// A shared connection outlives the request that created it, so it must not inherit
	// that request's cancellation: the cancel that fires when the request returns would
	// travel down the context tree and tear the connection down, discarding the replies
	// still in flight for every other request using it. Values are kept, as routing
	// depends on them; only the cancellation is cut. Such a connection is closed by its
	// ActivityTimer or by RemoveRay instead.
	connCtx := ctx
	if v.sharedConn {
		connCtx = context.WithoutCancel(ctx)
	}
	connCtx, cancel := context.WithCancel(connCtx)

	link, err := v.dispatcher.Dispatch(connCtx, dest)
	if err != nil {
		cancel()
		return nil, errors.New("failed to dispatch request to ", dest).Base(err)
	}

	entry := &connEntry{
		link:   link,
		cancel: cancel,
	}

	entry.timer = signal.CancelAfterInactivity(connCtx, entry.terminate, time.Minute)
	v.conn = entry
	v.connCreatedAt = time.Now()
	go handleInput(connCtx, entry, dest, v.callback, v.callClose)
	return entry, nil
}

func (v *Dispatcher) Dispatch(ctx context.Context, destination net.Destination, payload *buf.Buffer) {
	// TODO: Add user to destString
	errors.LogDebug(ctx, "dispatch request to: ", destination)

	conn, err := v.getInboundRay(ctx, destination)
	if err != nil {
		errors.LogInfoInner(ctx, err, "failed to get inbound")
		return
	}

	// 更新 maxDeadline 用于脱管时计算旧连接存活时间
	if deadline, ok := ctx.Deadline(); ok {
		v.Lock()
		if deadline.After(conn.maxDeadline) {
			conn.maxDeadline = deadline
		}
		v.Unlock()
	}

	outputStream := conn.link.Writer
	if outputStream != nil {
		if err := outputStream.WriteMultiBuffer(buf.MultiBuffer{payload}); err != nil {
			errors.LogInfoInner(ctx, err, "failed to write first UDP payload")
			conn.Close()
			return
		}
	}
}

func handleInput(ctx context.Context, conn *connEntry, dest net.Destination, callback ResponseCallback, callClose func() error) {
	defer func() {
		conn.Close()
		if callClose != nil {
			callClose()
		}
	}()

	input := conn.link.Reader
	timer := conn.timer

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		mb, err := input.ReadMultiBuffer()
		if err != nil {
			if !goerrors.Is(err, io.EOF) {
				errors.LogInfoInner(ctx, err, "failed to handle UDP input")
			}
			return
		}
		timer.Update()
		for _, b := range mb {
			if b.UDP != nil {
				dest = *b.UDP
			}
			callback(ctx, &udp.Packet{
				Payload: b,
				Source:  dest,
			})
		}
	}
}

type dispatcherConn struct {
	dispatcher *Dispatcher
	cache      chan *udp.Packet
	done       *done.Instance
	ctx        context.Context
}

func DialDispatcher(ctx context.Context, dispatcher routing.Dispatcher) (net.PacketConn, error) {
	c := &dispatcherConn{
		cache: make(chan *udp.Packet, 16),
		done:  done.New(),
		ctx:   ctx,
	}

	d := &Dispatcher{
		dispatcher: dispatcher,
		callback:   c.callback,
		callClose:  c.Close,
	}
	c.dispatcher = d
	return c, nil
}

func (c *dispatcherConn) callback(ctx context.Context, packet *udp.Packet) {
	select {
	case <-c.done.Wait():
		packet.Payload.Release()
		return
	case c.cache <- packet:
	default:
		packet.Payload.Release()
		return
	}
}

func (c *dispatcherConn) ReadFrom(p []byte) (int, net.Addr, error) {
	var packet *udp.Packet
s:
	select {
	case <-c.done.Wait():
		select {
		case packet = <-c.cache:
			break s
		default:
			return 0, nil, io.EOF
		}
	case packet = <-c.cache:
	}
	return copy(p, packet.Payload.Bytes()), &net.UDPAddr{
		IP:   packet.Source.Address.IP(),
		Port: int(packet.Source.Port),
	}, nil
}

func (c *dispatcherConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	buffer := buf.New()
	raw := buffer.Extend(buf.Size)
	n := copy(raw, p)
	buffer.Resize(0, int32(n))

	destination := net.DestinationFromAddr(addr)
	buffer.UDP = &destination
	c.dispatcher.Dispatch(c.ctx, destination, buffer)
	return n, nil
}

func (c *dispatcherConn) Close() error {
	return c.done.Close()
}

func (c *dispatcherConn) LocalAddr() net.Addr {
	return &net.UDPAddr{
		IP:   []byte{0, 0, 0, 0},
		Port: 0,
	}
}

func (c *dispatcherConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *dispatcherConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *dispatcherConn) SetWriteDeadline(t time.Time) error {
	return nil
}
