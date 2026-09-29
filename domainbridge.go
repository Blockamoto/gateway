package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

type domainBridgeStatus struct {
	DNSRunning  bool   `json:"dns_running"`
	HTTPRunning bool   `json:"http_running"`
	Error       string `json:"error,omitempty"`
}
type domainBridge struct {
	mu       sync.RWMutex
	dnsUDP   *net.UDPConn
	dnsTCP   net.Listener
	httpLn   net.Listener
	httpSrv  *http.Server
	baseURL  string
	lastErr  string
	stopping bool
}

func (d *domainBridge) status() domainBridgeStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return domainBridgeStatus{DNSRunning: d.dnsUDP != nil || d.dnsTCP != nil, HTTPRunning: d.httpLn != nil, Error: d.lastErr}
}

func dnsQuestion(packet []byte) (name string, qtype uint16, qend int, ok bool) {
	if len(packet) < 12 || binary.BigEndian.Uint16(packet[4:6]) == 0 {
		return "", 0, 0, false
	}
	i := 12
	labels := []string{}
	for {
		if i >= len(packet) {
			return "", 0, 0, false
		}
		n := int(packet[i])
		i++
		if n == 0 {
			break
		}
		if n&0xc0 != 0 || n > 63 || i+n > len(packet) {
			return "", 0, 0, false
		}
		labels = append(labels, string(packet[i:i+n]))
		i += n
	}
	if i+4 > len(packet) {
		return "", 0, 0, false
	}
	qtype = binary.BigEndian.Uint16(packet[i : i+2])
	qend = i + 4
	return strings.ToLower(strings.Join(labels, ".")), qtype, qend, true
}
func dnsResponse(packet []byte) []byte {
	name, qtype, qend, ok := dnsQuestion(packet)
	if !ok {
		return nil
	}
	isBitcoin := name == "bitcoin" || strings.HasSuffix(name, ".bitcoin") || strings.HasSuffix(name, ".gateway") || strings.HasSuffix(name, ".bitmap")
	resp := make([]byte, qend)
	copy(resp, packet[:qend])
	flags := uint16(0x8000 | 0x0080)
	if binary.BigEndian.Uint16(packet[2:4])&0x0100 != 0 {
		flags |= 0x0100
	}
	binary.BigEndian.PutUint16(resp[2:4], flags)
	binary.BigEndian.PutUint16(resp[4:6], 1)
	binary.BigEndian.PutUint16(resp[6:8], 0)
	binary.BigEndian.PutUint16(resp[8:10], 0)
	binary.BigEndian.PutUint16(resp[10:12], 0)
	if !isBitcoin {
		binary.BigEndian.PutUint16(resp[2:4], flags|5)
		return resp
	}
	if qtype != 1 {
		return resp
	}
	answer := make([]byte, 16)
	answer[0], answer[1] = 0xc0, 0x0c
	binary.BigEndian.PutUint16(answer[2:4], 1)
	binary.BigEndian.PutUint16(answer[4:6], 1)
	binary.BigEndian.PutUint32(answer[6:10], 1)
	binary.BigEndian.PutUint16(answer[10:12], 4)
	answer[12], answer[13], answer[14], answer[15] = 127, 0, 0, 1
	binary.BigEndian.PutUint16(resp[6:8], 1)
	return append(resp, answer...)
}
func (d *domainBridge) recordErr(err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastErr == "" {
		d.lastErr = err.Error()
	} else if !strings.Contains(d.lastErr, err.Error()) {
		d.lastErr += "; " + err.Error()
	}
}
func (d *domainBridge) startDNS() {
	udpAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:53")
	udp, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		d.recordErr(fmt.Errorf("DNS bridge: %w", err))
	} else {
		d.mu.Lock()
		d.dnsUDP = udp
		d.mu.Unlock()
		go func() {
			buf := make([]byte, 4096)
			for {
				n, addr, err := udp.ReadFromUDP(buf)
				if err != nil {
					return
				}
				if out := dnsResponse(buf[:n]); len(out) > 0 {
					_, _ = udp.WriteToUDP(out, addr)
				}
			}
		}()
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:53")
	if err != nil {
		d.recordErr(fmt.Errorf("DNS TCP bridge: %w", err))
		return
	}
	d.mu.Lock()
	d.dnsTCP = tcp
	d.mu.Unlock()
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				var lbuf [2]byte
				if _, err := io.ReadFull(conn, lbuf[:]); err != nil {
					return
				}
				n := int(binary.BigEndian.Uint16(lbuf[:]))
				if n <= 0 || n > 65535 {
					return
				}
				buf := make([]byte, n)
				if _, err := io.ReadFull(conn, buf); err != nil {
					return
				}
				out := dnsResponse(buf)
				if len(out) == 0 {
					return
				}
				binary.BigEndian.PutUint16(lbuf[:], uint16(len(out)))
				_, _ = conn.Write(lbuf[:])
				_, _ = conn.Write(out)
			}(c)
		}
	}()
}

// bitcoinProxyHandler serves the Gateway Client UI/API directly at the requested .bitcoin
// host. v0.4.1 redirected to localhost, which made the human name disappear
// from the address bar. This reverse proxy keeps 0.bitcoin (and deeper names)
// as the visible browser location while all application logic stays in the one
// local Gateway Client HTTP server.
func bitcoinProxyHandler(baseURL string) (http.Handler, error) {
	target, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	orig := proxy.Director
	proxy.Director = func(r *http.Request) {
		host := strings.ToLower(strings.TrimSpace(strings.Split(r.Host, ":")[0]))
		orig(r)
		r.Host = target.Host
		r.Header.Set("X-Gateway-On-Demand-Host", host)
		if r.URL.Path == "/" && r.URL.Query().Get("resolve") == "" {
			q := r.URL.Query()
			q.Set("resolve", host)
			r.URL.RawQuery = q.Encode()
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(strings.TrimSpace(strings.Split(r.Host, ":")[0]))
		if !validGatewayHost(host) {
			http.Error(w, "Gateway On Demand .bitcoin bridge only", http.StatusNotFound)
			return
		}
		proxy.ServeHTTP(w, r)
	}), nil
}
func (d *domainBridge) startHTTP() {
	ln, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		d.recordErr(fmt.Errorf(".bitcoin HTTP bridge: %w", err))
		return
	}
	h, err := bitcoinProxyHandler(d.baseURL)
	if err != nil {
		_ = ln.Close()
		d.recordErr(err)
		return
	}
	srv := &http.Server{Handler: h}
	d.mu.Lock()
	d.httpLn, d.httpSrv = ln, srv
	d.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
}
func newDomainBridge(baseURL string) *domainBridge {
	return &domainBridge{baseURL: strings.TrimRight(baseURL, "/")}
}
func (d *domainBridge) start() { d.startDNS(); d.startHTTP() }
func (d *domainBridge) stop() {
	d.mu.Lock()
	udp, tcp, srv, ln := d.dnsUDP, d.dnsTCP, d.httpSrv, d.httpLn
	d.dnsUDP, d.dnsTCP, d.httpSrv, d.httpLn = nil, nil, nil, nil
	d.stopping = true
	d.mu.Unlock()
	if udp != nil {
		_ = udp.Close()
	}
	if tcp != nil {
		_ = tcp.Close()
	}
	if srv != nil {
		_ = srv.Close()
	} else if ln != nil {
		_ = ln.Close()
	}
}
func (a *app) startDomainBridge() {
	if strings.TrimSpace(a.runtimeURL) == "" {
		return
	}
	if a.domainBridge != nil {
		st := a.domainBridge.status()
		if st.DNSRunning || st.HTTPRunning {
			return
		}
	}
	d := newDomainBridge(a.runtimeURL)
	a.domainBridge = d
	d.start()
}
func (a *app) stopDomainBridge() {
	if a.domainBridge != nil {
		a.domainBridge.stop()
	}
}
