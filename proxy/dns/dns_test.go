package dns_test

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/miekg/dns"
	"github.com/xtls/xray-core/app/dispatcher"
	dnsapp "github.com/xtls/xray-core/app/dns"
	"github.com/xtls/xray-core/app/policy"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	dns_proxy "github.com/xtls/xray-core/proxy/dns"
	"github.com/xtls/xray-core/proxy/dokodemo"
	"github.com/xtls/xray-core/testing/servers/tcp"
	"github.com/xtls/xray-core/testing/servers/udp"
)

type staticHandler struct{}

func (*staticHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	ans := new(dns.Msg)
	ans.Id = r.Id
	// Real servers echo the question back, and a forwarded reply is passed through
	// as is, so the mock has to do the same.
	ans.Question = r.Question

	var clientIP net.IP

	opt := r.IsEdns0()
	if opt != nil {
		for _, o := range opt.Option {
			if o.Option() == dns.EDNS0SUBNET {
				subnet := o.(*dns.EDNS0_SUBNET)
				clientIP = subnet.Address
			}
		}
	}

	for _, q := range r.Question {
		switch {
		case q.Name == "google.com." && q.Qtype == dns.TypeA:
			if clientIP == nil {
				rr, _ := dns.NewRR("google.com. IN A 8.8.8.8")
				ans.Answer = append(ans.Answer, rr)
			} else {
				rr, _ := dns.NewRR("google.com. IN A 8.8.4.4")
				ans.Answer = append(ans.Answer, rr)
			}

		case q.Name == "facebook.com." && q.Qtype == dns.TypeA:
			rr, _ := dns.NewRR("facebook.com. IN A 9.9.9.9")
			ans.Answer = append(ans.Answer, rr)

		case q.Name == "ipv6.google.com." && q.Qtype == dns.TypeA:
			rr, err := dns.NewRR("ipv6.google.com. IN A 8.8.8.7")
			common.Must(err)
			ans.Answer = append(ans.Answer, rr)

		case q.Name == "ipv6.google.com." && q.Qtype == dns.TypeAAAA:
			rr, err := dns.NewRR("ipv6.google.com. IN AAAA 2001:4860:4860::8888")
			common.Must(err)
			ans.Answer = append(ans.Answer, rr)

		case q.Name == "notexist.google.com." && q.Qtype == dns.TypeAAAA:
			ans.MsgHdr.Rcode = dns.RcodeNameError

		case q.Name == "_minecraft._tcp.example.com." && q.Qtype == dns.TypeSRV:
			rr, err := dns.NewRR("_minecraft._tcp.example.com. 60 IN SRV 0 5 25565 mc.example.com.")
			common.Must(err)
			ans.Answer = append(ans.Answer, rr)
			// Glue for the target. The handler must strip it, or a client could reach
			// the target without ever querying its address record.
			glue, err := dns.NewRR("mc.example.com. 60 IN A 1.2.3.4")
			common.Must(err)
			ans.Extra = append(ans.Extra, glue)
		}
	}
	w.WriteMsg(ans)
}

// srvTargetHandler answers every SRV query with a target naming the server that
// produced it, so a test can tell which upstream was picked.
type srvTargetHandler struct {
	target string
}

func (h *srvTargetHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	ans := new(dns.Msg)
	ans.Id = r.Id
	ans.Question = r.Question

	for _, q := range r.Question {
		if q.Qtype == dns.TypeSRV {
			rr, err := dns.NewRR(q.Name + " 60 IN SRV 0 5 5060 " + h.target)
			common.Must(err)
			ans.Answer = append(ans.Answer, rr)
		}
	}
	w.WriteMsg(ans)
}

// Forwarding a query still goes through the domain rules, which is the whole point of
// hijacking it rather than letting the direct action send it to the original server.
func TestForwardedSRVFollowsDomainRules(t *testing.T) {
	fallbackPort := udp.PickPort()
	fallbackServer := dns.Server{
		Addr:    "127.0.0.1:" + fallbackPort.String(),
		Net:     "udp",
		Handler: &srvTargetHandler{target: "fallback.example.com."},
		UDPSize: 1200,
	}
	defer fallbackServer.Shutdown()
	go fallbackServer.ListenAndServe()

	matchedPort := udp.PickPort()
	matchedServer := dns.Server{
		Addr:    "127.0.0.1:" + matchedPort.String(),
		Net:     "udp",
		Handler: &srvTargetHandler{target: "matched.example.com."},
		UDPSize: 1200,
	}
	defer matchedServer.Shutdown()
	go matchedServer.ListenAndServe()

	time.Sleep(time.Second)

	localhost := &net.IPOrDomain{Address: &net.IPOrDomain_Ip{Ip: []byte{127, 0, 0, 1}}}
	serverPort := udp.PickPort()
	config := &core.Config{
		App: []*serial.TypedMessage{
			// The dispatcher has to come first: the DNS app resolves it through
			// RequireFeatures, and that callback is what registers the domain rules.
			// Deferring it would leave the app with no domain matcher at all.
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
			serial.ToTypedMessage(&dnsapp.Config{
				NameServer: []*dnsapp.NameServer{
					{
						// First in order, so it answers unless a rule says otherwise.
						Address: &net.Endpoint{Network: net.Network_UDP, Address: localhost, Port: uint32(fallbackPort)},
					},
					{
						Address: &net.Endpoint{Network: net.Network_UDP, Address: localhost, Port: uint32(matchedPort)},
						Domain: []*geodata.DomainRule{
							{
								Value: &geodata.DomainRule_Custom{
									Custom: &geodata.Domain{Type: geodata.Domain_Full, Value: "_sip._tcp.example.com"},
								},
							},
						},
						FinalQuery: true,
					},
				},
			}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(net.LocalHostIP),
					RewritePort:     uint32(fallbackPort),
					AllowedNetworks: []net.Network{net.Network_UDP},
				}),
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dns_proxy.Config{}),
			},
		},
	}

	v, err := core.New(config)
	common.Must(err)
	common.Must(v.Start())
	defer v.Close()

	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = []dns.Question{{Name: "_sip._tcp.example.com.", Qtype: dns.TypeSRV, Qclass: dns.ClassINET}}

		c := new(dns.Client)
		c.Timeout = 10 * time.Second
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if len(in.Answer) != 1 {
			t.Fatal("len(answer): ", len(in.Answer))
		}

		srv, ok := in.Answer[0].(*dns.SRV)
		if !ok {
			t.Fatal("not SRV record: ", in.Answer[0])
		}
		if srv.Target != "matched.example.com." {
			t.Error("expected the rule-matched server to answer, but got target ", srv.Target)
		}
	}

	// The upstream returns nothing for a TXT query, and the forwarded reply carries
	// that through as an empty NOERROR rather than making the client wait.
	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = []dns.Question{{Name: "_sip._tcp.example.com.", Qtype: dns.TypeTXT, Qclass: dns.ClassINET}}

		c := new(dns.Client)
		c.Timeout = 10 * time.Second
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if in.Rcode != dns.RcodeSuccess {
			t.Fatal("expected Success for TXT, but got ", in.Rcode)
		}

		if len(in.Answer) != 0 {
			t.Error("expected no answer for TXT, but got ", in.Answer)
		}

		// TXT is not forwarded, so this is the synthesized NODATA with its SOA.
		if len(in.Ns) != 1 {
			t.Error("expected a synthesized SOA for TXT, but got ", in.Ns)
		}
	}

	// A name no rule covers must fall back to the first server in order.
	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = []dns.Question{{Name: "_sip._udp.example.org.", Qtype: dns.TypeSRV, Qclass: dns.ClassINET}}

		c := new(dns.Client)
		c.Timeout = 10 * time.Second
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if len(in.Answer) != 1 {
			t.Fatal("len(answer): ", len(in.Answer))
		}

		srv, ok := in.Answer[0].(*dns.SRV)
		if !ok {
			t.Fatal("not SRV record: ", in.Answer[0])
		}
		if srv.Target != "fallback.example.com." {
			t.Error("expected the first server to answer, but got target ", srv.Target)
		}
	}
}

// Forwarded and IP queries draw request IDs from one counter and share a single UDP
// name server, so a response has to reach the right one of the two pending tables
// even when both kinds are in flight at once.
func TestInterleavedSRVAndIPQueries(t *testing.T) {
	port := udp.PickPort()
	dnsServer := dns.Server{
		Addr:    "127.0.0.1:" + port.String(),
		Net:     "udp",
		Handler: &staticHandler{},
		UDPSize: 1200,
	}
	defer dnsServer.Shutdown()
	go dnsServer.ListenAndServe()
	time.Sleep(time.Second)

	serverPort := udp.PickPort()
	config := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
			serial.ToTypedMessage(&dnsapp.Config{
				NameServer: []*dnsapp.NameServer{
					{
						Address: &net.Endpoint{
							Network: net.Network_UDP,
							Address: &net.IPOrDomain{Address: &net.IPOrDomain_Ip{Ip: []byte{127, 0, 0, 1}}},
							Port:    uint32(port),
						},
						// Without a cache every query goes out to the server, which is
						// what puts both pending tables under load.
						DisableCache: &[]bool{true}[0],
					},
				},
			}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(net.LocalHostIP),
					RewritePort:     uint32(port),
					AllowedNetworks: []net.Network{net.Network_UDP},
				}),
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{ProxySettings: serial.ToTypedMessage(&dns_proxy.Config{})},
		},
	}

	v, err := core.New(config)
	common.Must(err)
	common.Must(v.Start())
	defer v.Close()

	const rounds = 20
	addr := "127.0.0.1:" + strconv.Itoa(int(serverPort))

	var wg sync.WaitGroup
	errCh := make(chan error, rounds*2)
	ask := func(name string, qType uint16, check func(*dns.Msg) error) {
		defer wg.Done()
		m := new(dns.Msg)
		m.Id = dns.Id()
		m.RecursionDesired = true
		m.Question = []dns.Question{{Name: name, Qtype: qType, Qclass: dns.ClassINET}}

		c := &dns.Client{Timeout: 10 * time.Second}
		in, _, err := c.Exchange(m, addr)
		if err != nil {
			errCh <- err
			return
		}
		if err := check(in); err != nil {
			errCh <- err
		}
	}

	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go ask("_minecraft._tcp.example.com.", dns.TypeSRV, func(in *dns.Msg) error {
			if len(in.Answer) != 1 {
				return fmt.Errorf("SRV: len(answer) = %d", len(in.Answer))
			}
			srv, ok := in.Answer[0].(*dns.SRV)
			if !ok {
				return fmt.Errorf("SRV: got %v", in.Answer[0])
			}
			if srv.Target != "mc.example.com." {
				return fmt.Errorf("SRV: target = %s", srv.Target)
			}
			return nil
		})
		go ask("google.com.", dns.TypeA, func(in *dns.Msg) error {
			if len(in.Answer) != 1 {
				return fmt.Errorf("A: len(answer) = %d", len(in.Answer))
			}
			a, ok := in.Answer[0].(*dns.A)
			if !ok {
				return fmt.Errorf("A: got %v", in.Answer[0])
			}
			if !a.A.Equal(net.IP{8, 8, 8, 8}) {
				return fmt.Errorf("A: address = %s", a.A)
			}
			return nil
		})
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestUDPDNSTunnel(t *testing.T) {
	port := udp.PickPort()

	dnsServer := dns.Server{
		Addr:    "127.0.0.1:" + port.String(),
		Net:     "udp",
		Handler: &staticHandler{},
		UDPSize: 1200,
	}
	defer dnsServer.Shutdown()

	go dnsServer.ListenAndServe()
	time.Sleep(time.Second)

	serverPort := udp.PickPort()
	config := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dnsapp.Config{
				NameServer: []*dnsapp.NameServer{
					{
						Address: &net.Endpoint{
							Network: net.Network_UDP,
							Address: &net.IPOrDomain{
								Address: &net.IPOrDomain_Ip{
									Ip: []byte{127, 0, 0, 1},
								},
							},
							Port: uint32(port),
						},
					},
				},
			}),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(net.LocalHostIP),
					RewritePort:     uint32(port),
					AllowedNetworks: []net.Network{net.Network_UDP},
				}),
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dns_proxy.Config{}),
			},
		},
	}

	v, err := core.New(config)
	common.Must(err)
	common.Must(v.Start())
	defer v.Close()

	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = make([]dns.Question, 1)
		m1.Question[0] = dns.Question{Name: "google.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}

		c := new(dns.Client)
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if len(in.Answer) != 1 {
			t.Fatal("len(answer): ", len(in.Answer))
		}

		rr, ok := in.Answer[0].(*dns.A)
		if !ok {
			t.Fatal("not A record")
		}
		if r := cmp.Diff(rr.A[:], net.IP{8, 8, 8, 8}); r != "" {
			t.Error(r)
		}
	}

	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = make([]dns.Question, 1)
		m1.Question[0] = dns.Question{Name: "ipv4only.google.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}

		c := new(dns.Client)
		c.Timeout = 10 * time.Second
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if len(in.Answer) != 0 {
			t.Fatal("len(answer): ", len(in.Answer))
		}
	}

	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = make([]dns.Question, 1)
		m1.Question[0] = dns.Question{Name: "notexist.google.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}

		c := new(dns.Client)
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if in.Rcode != dns.RcodeNameError {
			t.Error("expected NameError, but got ", in.Rcode)
		}
	}

	// SRV is hijacked by default, which forwards it and passes the reply through.
	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = []dns.Question{{Name: "_minecraft._tcp.example.com.", Qtype: dns.TypeSRV, Qclass: dns.ClassINET}}

		c := new(dns.Client)
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if in.Id != m1.Id {
			t.Fatal("expected transaction ID ", m1.Id, ", but got ", in.Id)
		}

		if in.Rcode != dns.RcodeSuccess {
			t.Fatal("expected Success, but got ", in.Rcode)
		}

		if len(in.Answer) != 1 {
			t.Fatal("len(answer): ", len(in.Answer))
		}

		srv, ok := in.Answer[0].(*dns.SRV)
		if !ok {
			t.Fatal("not SRV record: ", in.Answer[0])
		}
		if srv.Target != "mc.example.com." || srv.Port != 25565 {
			t.Error("unexpected SRV target ", srv.Target, " port ", srv.Port)
		}

		if len(in.Extra) != 0 {
			t.Error("expected the additional section to be stripped, but got ", in.Extra)
		}

		if len(in.Question) != 1 || in.Question[0].Name != "_minecraft._tcp.example.com." {
			t.Error("expected the question section to be echoed back, but got ", in.Question)
		}
	}
}

func TestTCPDNSTunnel(t *testing.T) {
	port := udp.PickPort()

	dnsServer := dns.Server{
		Addr:    "127.0.0.1:" + port.String(),
		Net:     "udp",
		Handler: &staticHandler{},
	}
	defer dnsServer.Shutdown()

	go dnsServer.ListenAndServe()
	time.Sleep(time.Second)

	serverPort := tcp.PickPort()
	config := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dnsapp.Config{
				NameServer: []*dnsapp.NameServer{
					{
						Address: &net.Endpoint{
							Network: net.Network_UDP,
							Address: &net.IPOrDomain{
								Address: &net.IPOrDomain_Ip{
									Ip: []byte{127, 0, 0, 1},
								},
							},
							Port: uint32(port),
						},
					},
				},
			}),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(net.LocalHostIP),
					RewritePort:     uint32(port),
					AllowedNetworks: []net.Network{net.Network_TCP},
				}),
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dns_proxy.Config{}),
			},
		},
	}

	v, err := core.New(config)
	common.Must(err)
	common.Must(v.Start())
	defer v.Close()

	m1 := new(dns.Msg)
	m1.Id = dns.Id()
	m1.RecursionDesired = true
	m1.Question = make([]dns.Question, 1)
	m1.Question[0] = dns.Question{Name: "google.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}

	c := &dns.Client{
		Net: "tcp",
	}
	in, _, err := c.Exchange(m1, "127.0.0.1:"+serverPort.String())
	common.Must(err)

	if len(in.Answer) != 1 {
		t.Fatal("len(answer): ", len(in.Answer))
	}

	rr, ok := in.Answer[0].(*dns.A)
	if !ok {
		t.Fatal("not A record")
	}
	if r := cmp.Diff(rr.A[:], net.IP{8, 8, 8, 8}); r != "" {
		t.Error(r)
	}
}

func TestUDP2TCPDNSTunnel(t *testing.T) {
	port := tcp.PickPort()

	dnsServer := dns.Server{
		Addr:    "127.0.0.1:" + port.String(),
		Net:     "tcp",
		Handler: &staticHandler{},
	}
	defer dnsServer.Shutdown()

	go dnsServer.ListenAndServe()
	time.Sleep(time.Second)

	serverPort := tcp.PickPort()
	config := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dnsapp.Config{
				NameServer: []*dnsapp.NameServer{
					{
						Address: &net.Endpoint{
							Network: net.Network_UDP,
							Address: &net.IPOrDomain{
								Address: &net.IPOrDomain_Ip{
									Ip: []byte{127, 0, 0, 1},
								},
							},
							Port: uint32(port),
						},
					},
				},
			}),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(net.LocalHostIP),
					RewritePort:     uint32(port),
					AllowedNetworks: []net.Network{net.Network_TCP},
				}),
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dns_proxy.Config{
					RewriteServer: &net.Endpoint{
						Network: net.Network_TCP,
					},
				}),
			},
		},
	}

	v, err := core.New(config)
	common.Must(err)
	common.Must(v.Start())
	defer v.Close()

	m1 := new(dns.Msg)
	m1.Id = dns.Id()
	m1.RecursionDesired = true
	m1.Question = make([]dns.Question, 1)
	m1.Question[0] = dns.Question{Name: "google.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}

	c := &dns.Client{
		Net: "tcp",
	}
	in, _, err := c.Exchange(m1, "127.0.0.1:"+serverPort.String())
	common.Must(err)

	if len(in.Answer) != 1 {
		t.Fatal("len(answer): ", len(in.Answer))
	}

	rr, ok := in.Answer[0].(*dns.A)
	if !ok {
		t.Fatal("not A record")
	}
	if r := cmp.Diff(rr.A[:], net.IP{8, 8, 8, 8}); r != "" {
		t.Error(r)
	}
}

func TestDNSRules(t *testing.T) {
	port := udp.PickPort()

	dnsServer := dns.Server{
		Addr:    "127.0.0.1:" + port.String(),
		Net:     "udp",
		Handler: &staticHandler{},
	}
	defer dnsServer.Shutdown()

	go dnsServer.ListenAndServe()
	time.Sleep(time.Second)

	serverPort := udp.PickPort()
	config := &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dnsapp.Config{
				NameServer: []*dnsapp.NameServer{
					{
						Address: &net.Endpoint{
							Network: net.Network_UDP,
							Address: &net.IPOrDomain{
								Address: &net.IPOrDomain_Ip{
									Ip: []byte{127, 0, 0, 1},
								},
							},
							Port: uint32(port),
						},
					},
				},
			}),
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&policy.Config{}),
		},
		Inbound: []*core.InboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
					RewriteAddress:  net.NewIPOrDomain(net.LocalHostIP),
					RewritePort:     uint32(port),
					AllowedNetworks: []net.Network{net.Network_UDP},
				}),
				ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
					PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
					Listen:   net.NewIPOrDomain(net.LocalHostIP),
				}),
			},
		},
		Outbound: []*core.OutboundHandlerConfig{
			{
				ProxySettings: serial.ToTypedMessage(&dns_proxy.Config{
					Rule: []*dns_proxy.DNSRuleConfig{
						{
							QType: []int32{int32(dns.TypeA)},
							Domain: []*geodata.DomainRule{
								{
									Value: &geodata.DomainRule_Custom{
										Custom: &geodata.Domain{
											Type:  geodata.Domain_Domain,
											Value: "facebook.com",
										},
									},
								},
							},
							Action: dns_proxy.RuleAction_Direct,
						},
						{
							QType: []int32{int32(dns.TypeA)},
							Domain: []*geodata.DomainRule{
								{
									Value: &geodata.DomainRule_Custom{
										Custom: &geodata.Domain{
											Type:  geodata.Domain_Full,
											Value: "google.com",
										},
									},
								},
							},
							Action: dns_proxy.RuleAction_Return,
							RCode:  5,
						},
					},
				}),
			},
		},
	}

	v, err := core.New(config)
	common.Must(err)
	common.Must(v.Start())
	defer v.Close()

	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = []dns.Question{{Name: "google.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}}

		c := new(dns.Client)
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		// The rule asks for REFUSED explicitly, which conveys a refusal rather than
		// authoritative knowledge of the name, so no SOA comes back with it.
		if in.Rcode != dns.RcodeRefused {
			t.Fatal("expected Refused, but got ", in.Rcode)
		}

		if len(in.Answer) != 0 {
			t.Fatal("expected empty answer section, but got ", in.Answer)
		}

		if len(in.Ns) != 0 {
			t.Fatal("expected no authority record, but got ", in.Ns)
		}
	}

	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = []dns.Question{{Name: "facebook.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}}

		c := new(dns.Client)
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if in.Rcode != dns.RcodeSuccess {
			t.Fatal("expected Success, but got ", in.Rcode)
		}
	}

	// No rule matches type 65, so it falls through to the implicit return for
	// non-IP queries, whose rCode defaults to 0, and must come back as NODATA
	// with a SOA for negative caching rather than as an unanswered query.
	{
		m1 := new(dns.Msg)
		m1.Id = dns.Id()
		m1.RecursionDesired = true
		m1.Question = []dns.Question{{Name: "google.com.", Qtype: dns.TypeHTTPS, Qclass: dns.ClassINET}}

		c := new(dns.Client)
		in, _, err := c.Exchange(m1, "127.0.0.1:"+strconv.Itoa(int(serverPort)))
		common.Must(err)

		if in.Rcode != dns.RcodeSuccess {
			t.Fatal("expected Success for type 65, but got ", in.Rcode)
		}

		if len(in.Answer) != 0 {
			t.Fatal("expected empty answer section for type 65, but got ", in.Answer)
		}

		if len(in.Ns) != 1 {
			t.Fatal("expected a single authority record for type 65, but got ", in.Ns)
		}

		soa, ok := in.Ns[0].(*dns.SOA)
		if !ok {
			t.Fatal("expected SOA in authority section, but got ", in.Ns[0])
		}

		if soa.Hdr.Ttl != 300 {
			t.Fatal("expected SOA TTL 300, but got ", soa.Hdr.Ttl)
		}

		if len(in.Question) != 1 || in.Question[0].Qtype != dns.TypeHTTPS {
			t.Fatal("expected the question section to be echoed back, but got ", in.Question)
		}
	}
}
