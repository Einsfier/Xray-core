package dns

import (
	"context"

	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/features"
)

// IPOption is an object for IP query options.
type IPOption struct {
	IPv4Enable bool
	IPv6Enable bool
	FakeEnable bool
}

// Client is a Xray feature for querying DNS information.
//
// xray:api:stable
type Client interface {
	features.Feature

	// LookupIP returns IP address for the given domain. IPs may contain IPv4 and/or IPv6 addresses.
	LookupIP(domain string, option IPOption) ([]net.IP, uint32, error)
}

// ClientType returns the type of Client interface. Can be used for implementing common.HasType.
//
// xray:api:beta
func ClientType() interface{} {
	return (*Client)(nil)
}

// RawClient is an optional extension of Client for record types that do not resolve
// into IPs, such as SRV. The query is forwarded to the name servers picked by the
// domain rules rather than answered locally, so the upstream response is handed back
// as raw wire format. Callers must type assert for it.
//
// xray:api:beta
type RawClient interface {
	// LookupRaw forwards a query for domain of the given type and returns the raw
	// response. The transaction ID of the response is unrelated to any client's.
	LookupRaw(ctx context.Context, domain string, qType uint16) ([]byte, error)
}

// ErrEmptyResponse indicates that DNS query succeeded but no answer was returned.
var ErrEmptyResponse = errors.New("empty response")

const DefaultTTL = 300

type RCodeError uint16

func (e RCodeError) Error() string {
	return serial.Concat("rcode: ", uint16(e))
}

func (RCodeError) IP() net.IP {
	panic("Calling IP() on a RCodeError.")
}

func (RCodeError) Domain() string {
	panic("Calling Domain() on a RCodeError.")
}

func (RCodeError) Family() net.AddressFamily {
	panic("Calling Family() on a RCodeError.")
}

func (e RCodeError) String() string {
	return e.Error()
}

var _ net.Address = (*RCodeError)(nil)

func RCodeFromError(err error) uint16 {
	if err == nil {
		return 0
	}
	cause := errors.Cause(err)
	if r, ok := cause.(RCodeError); ok {
		return uint16(r)
	}
	return 0
}
