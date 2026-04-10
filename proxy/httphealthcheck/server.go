package httphealthcheck

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet/stat"
)

func init() {
	common.Must(common.RegisterConfig((*HealthServerConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewHealthServer(ctx, config.(*HealthServerConfig))
	}))
}

type HealthServer struct {
	timeout time.Duration
	ready   atomic.Bool
}

func NewHealthServer(ctx context.Context, config *HealthServerConfig) (*HealthServer, error) {
	return &HealthServer{
		timeout: time.Duration(config.Timeout) * time.Second,
	}, nil
}

func (s *HealthServer) Network() []net.Network {
	return []net.Network{net.Network_TCP}
}

type readerOnly struct {
	io.Reader
}

func (s *HealthServer) Process(ctx context.Context, network net.Network, conn stat.Connection, dispatcher routing.Dispatcher) error {
	reader := bufio.NewReaderSize(readerOnly{conn}, buf.Size)
	if err := conn.SetReadDeadline(time.Now().Add(s.timeout)); err != nil {
		errors.LogWarning(ctx, "failed to set read deadline: ", err)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	request, err := http.ReadRequest(reader)
	if err != nil {
		if err != io.EOF {
			return errors.New("failed to read http health check request").Base(err)
		}
		return nil
	}
	if err := s.handleHttp(ctx, request, conn, dispatcher); err != nil {
		return errors.New("failed to handle http health check request").Base(err)
	}
	return nil
}

func (s *HealthServer) handleHttp(ctx context.Context, request *http.Request, writer io.Writer, dispatcher routing.Dispatcher) error {
	response := &http.Response{
		Status:        "OK",
		StatusCode:    200,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header(make(map[string][]string)),
		Body:          nil,
		ContentLength: 0,
		Close:         true,
	}
	response.Header.Set("Connection", "close")
	return response.Write(writer)
}
