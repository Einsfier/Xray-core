package udp_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol/udp"
	"github.com/xtls/xray-core/transport"
	. "github.com/xtls/xray-core/transport/internet/udp"
	"github.com/xtls/xray-core/transport/pipe"
)

// slowEchoDispatcher hands out links that answer replyAfter later, mimicking a remote
// DNS server behind a proxy. It records the context each link was dispatched with and
// tears the link down when that context is cancelled, as a real outbound does when its
// task.Run returns.
type slowEchoDispatcher struct {
	replyAfter time.Duration

	mu       sync.Mutex
	linkCtxs []context.Context
}

func (d *slowEchoDispatcher) Dispatch(ctx context.Context, dest net.Destination) (*transport.Link, error) {
	d.mu.Lock()
	d.linkCtxs = append(d.linkCtxs, ctx)
	d.mu.Unlock()

	upReader, upWriter := pipe.New(pipe.WithSizeLimit(1024))
	downReader, downWriter := pipe.New(pipe.WithSizeLimit(1024))

	go func() {
		<-ctx.Done()
		common.Interrupt(upReader)
		common.Interrupt(downWriter)
	}()

	go func() {
		for {
			mb, err := upReader.ReadMultiBuffer()
			if err != nil {
				return
			}
			// A real server has several queries in flight at once, so each reply waits
			// on its own rather than queueing behind the one before it.
			for range mb {
				go func() {
					time.Sleep(d.replyAfter)
					b := buf.New()
					common.Must2(b.WriteString("reply"))
					if err := downWriter.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
						b.Release()
					}
				}()
			}
			buf.ReleaseMulti(mb)
		}
	}()

	return &transport.Link{Reader: downReader, Writer: upWriter}, nil
}

func (d *slowEchoDispatcher) DispatchLink(context.Context, net.Destination, *transport.Link) error {
	return nil
}
func (d *slowEchoDispatcher) Start() error      { return nil }
func (d *slowEchoDispatcher) Close() error      { return nil }
func (d *slowEchoDispatcher) Type() interface{} { return nil }

func (d *slowEchoDispatcher) connCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.linkCtxs)
}

func (d *slowEchoDispatcher) linkCtx(i int) context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.linkCtxs[i]
}

// A shared connection must survive the cancellation of the request that created it, so
// that replies still in flight for the other requests on it are not discarded.
func TestNoCacheDispatcherKeepsConnAfterCreatorCancels(t *testing.T) {
	td := &slowEchoDispatcher{replyAfter: time.Second}
	dest := net.UDPDestination(net.LocalHostIP, 53)

	var mu sync.Mutex
	replies := 0
	d := NewNoCacheDispatcher(td, func(ctx context.Context, packet *udp.Packet) {
		packet.Payload.Release()
		mu.Lock()
		replies++
		mu.Unlock()
	}, time.Second)

	// First request creates the connection, with the short-lived context a DNS query
	// carries.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 4*time.Second)
	first := buf.New()
	common.Must2(first.WriteString("q1"))
	d.Dispatch(ctx1, dest, first)

	// Second request arrives inside the tolerance window and reuses that connection.
	time.Sleep(100 * time.Millisecond)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel2()
	second := buf.New()
	common.Must2(second.WriteString("q2"))
	d.Dispatch(ctx2, dest, second)

	if got := td.connCount(); got != 1 {
		t.Fatalf("second request should reuse the connection: got %d connections, want 1", got)
	}

	// The first request finishes early and cancels, as Client.QueryIP does on return.
	cancel1()
	time.Sleep(100 * time.Millisecond)

	select {
	case <-td.linkCtx(0).Done():
		t.Fatal("connection was torn down by the cancellation of the request that created it")
	default:
	}

	// Both replies land after the cancellation.
	time.Sleep(1500 * time.Millisecond)
	mu.Lock()
	got := replies
	mu.Unlock()
	if got != 2 {
		t.Fatalf("got %d replies, want 2", got)
	}
}

// Past the tolerance window a new connection is established, so route selection runs
// again rather than the dispatcher staying pinned to its first pick.
func TestNoCacheDispatcherRebuildsAfterTolerance(t *testing.T) {
	td := &slowEchoDispatcher{replyAfter: 10 * time.Millisecond}
	dest := net.UDPDestination(net.LocalHostIP, 53)

	d := NewNoCacheDispatcher(td, func(ctx context.Context, packet *udp.Packet) {
		packet.Payload.Release()
	}, 500*time.Millisecond)
	defer d.RemoveRay()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		b := buf.New()
		common.Must2(b.WriteString("q"))
		d.Dispatch(ctx, dest, b)
		time.Sleep(300 * time.Millisecond)
	}

	if got := td.connCount(); got < 2 {
		t.Fatalf("got %d connections, want at least 2", got)
	}
}

// A connection detached after the deadlines of its requests have already passed is
// closed right away: nothing is waiting on it, and leaving it to the inactivity timer
// would keep it around for another minute.
func TestNoCacheDispatcherClosesDetachedConnWithExpiredDeadline(t *testing.T) {
	td := &slowEchoDispatcher{replyAfter: 10 * time.Millisecond}
	dest := net.UDPDestination(net.LocalHostIP, 53)

	d := NewNoCacheDispatcher(td, func(ctx context.Context, packet *udp.Packet) {
		packet.Payload.Release()
	}, 200*time.Millisecond)
	defer d.RemoveRay()

	ctx1, cancel1 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel1()
	first := buf.New()
	common.Must2(first.WriteString("q1"))
	d.Dispatch(ctx1, dest, first)

	// Well past both the deadline and the tolerance window, so the next request detaches
	// a connection no request is waiting on any more.
	time.Sleep(600 * time.Millisecond)
	second := buf.New()
	common.Must2(second.WriteString("q2"))
	d.Dispatch(context.Background(), dest, second)

	select {
	case <-td.linkCtx(0).Done():
	case <-time.After(time.Second):
		t.Fatal("connection with an expired deadline was left to the inactivity timer")
	}
}

// A detached connection is still closed once the deadlines of the requests it carried
// have passed, so dropping it from v.conn does not leak it.
func TestNoCacheDispatcherClosesDetachedConn(t *testing.T) {
	td := &slowEchoDispatcher{replyAfter: 10 * time.Millisecond}
	dest := net.UDPDestination(net.LocalHostIP, 53)

	d := NewNoCacheDispatcher(td, func(ctx context.Context, packet *udp.Packet) {
		packet.Payload.Release()
	}, 200*time.Millisecond)
	defer d.RemoveRay()

	// A request with a short deadline, so the detached connection's timer is short too.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel1()
	first := buf.New()
	common.Must2(first.WriteString("q1"))
	d.Dispatch(ctx1, dest, first)

	// Past the tolerance window, so this detaches the first connection.
	time.Sleep(400 * time.Millisecond)
	second := buf.New()
	common.Must2(second.WriteString("q2"))
	d.Dispatch(context.Background(), dest, second)

	if got := td.connCount(); got != 2 {
		t.Fatalf("got %d connections, want 2", got)
	}

	select {
	case <-td.linkCtx(0).Done():
	case <-time.After(3 * time.Second):
		t.Fatal("detached connection was never closed")
	}
}
