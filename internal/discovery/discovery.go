package discovery

import (
	"encoding/json"
	"errors"
	"net"
	"runtime"
	"sort"
	"sync"
	"time"
)

const (
	interval = 3 * time.Second
	peerTTL  = 12 * time.Second
)

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Secure   bool   `json:"secure"`
	LastSeen int64  `json:"last_seen"`
}

type packet struct {
	Magic  string `json:"magic"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Port   int    `json:"port"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Secure bool   `json:"secure"`
}

type Service struct {
	id       string
	name     string
	httpPort int
	udpPort  int

	mu     sync.RWMutex
	peers  map[string]Device
	secure bool
	conn   *net.UDPConn
	stop   chan struct{}
	once   sync.Once
}

func New(id, name string, httpPort, udpPort int) *Service {
	return &Service{
		id:       id,
		name:     name,
		httpPort: httpPort,
		udpPort:  udpPort,
		peers:    make(map[string]Device),
		stop:     make(chan struct{}),
	}
}

func (s *Service) Start() error {
	if s.udpPort <= 0 || s.udpPort > 65535 {
		return errors.New("invalid discovery port")
	}
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: s.udpPort}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return err
	}
	if err := enableUDPBroadcast(conn); err != nil {
		_ = conn.Close()
		return err
	}
	s.conn = conn
	go s.readLoop()
	go s.broadcastLoop()
	go s.cleanupLoop()
	return nil
}

func (s *Service) Close() error {
	var err error
	s.once.Do(func() {
		close(s.stop)
		if s.conn != nil {
			err = s.conn.Close()
		}
	})
	return err
}

func (s *Service) SetName(name string) {
	s.mu.Lock()
	s.name = name
	s.mu.Unlock()
}

func (s *Service) SetSecure(secure bool) {
	s.mu.Lock()
	s.secure = secure
	s.mu.Unlock()
}

func (s *Service) Find(id string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.peers[id]
	if !ok || time.Since(time.Unix(p.LastSeen, 0)) > peerTTL {
		return Device{}, false
	}
	return p, true
}

func (s *Service) Devices() []Device {
	now := time.Now()
	s.mu.RLock()
	devices := make([]Device, 0, len(s.peers))
	for _, p := range s.peers {
		if now.Sub(time.Unix(p.LastSeen, 0)) <= peerTTL {
			devices = append(devices, p)
		}
	}
	s.mu.RUnlock()
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Name == devices[j].Name {
			return devices[i].IP < devices[j].IP
		}
		return devices[i].Name < devices[j].Name
	})
	return devices
}

func (s *Service) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, src, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
				return
			}
		}
		var p packet
		if err := json.Unmarshal(buf[:n], &p); err != nil || p.Magic != "LANSHARE/1" || p.ID == "" || p.ID == s.id || p.Port <= 0 || p.Port > 65535 {
			continue
		}
		if src.IP.IsLoopback() {
			continue
		}
		s.mu.Lock()
		s.peers[p.ID] = Device{
			ID: p.ID, Name: p.Name, IP: src.IP.String(), Port: p.Port,
			OS: p.OS, Arch: p.Arch, Secure: p.Secure, LastSeen: time.Now().Unix(),
		}
		s.mu.Unlock()
	}
}

func (s *Service) broadcastLoop() {
	send := func() {
		s.mu.RLock()
		payload, _ := json.Marshal(packet{
			Magic: "LANSHARE/1", ID: s.id, Name: s.name, Port: s.httpPort,
			OS: runtime.GOOS, Arch: runtime.GOARCH, Secure: s.secure,
		})
		s.mu.RUnlock()
		for _, dst := range broadcastAddrs(s.udpPort) {
			_, _ = s.conn.WriteToUDP(payload, dst)
		}
	}
	send()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			send()
		case <-s.stop:
			return
		}
	}
}

func (s *Service) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			s.mu.Lock()
			for id, p := range s.peers {
				if now.Sub(time.Unix(p.LastSeen, 0)) > peerTTL {
					delete(s.peers, id)
				}
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

func LocalIPv4s() []string {
	var ips []string
	seen := make(map[string]bool)
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				s := ip4.String()
				if !seen[s] {
					seen[s] = true
					ips = append(ips, s)
				}
			}
		}
	}
	sort.SliceStable(ips, func(i, j int) bool {
		si, sj := lanIPScore(net.ParseIP(ips[i])), lanIPScore(net.ParseIP(ips[j]))
		if si == sj {
			return ips[i] < ips[j]
		}
		return si < sj
	})
	return ips
}

func lanIPScore(ip net.IP) int {
	ip4 := ip.To4()
	if ip4 == nil {
		return 99
	}
	switch {
	case ip4[0] == 192 && ip4[1] == 168:
		return 0
	case ip4[0] == 10:
		return 1
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return 2
	case ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19):
		return 8
	case ip4[0] == 169 && ip4[1] == 254:
		return 9
	default:
		return 3
	}
}

func IsLocalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, raw := range LocalIPv4s() {
		if local := net.ParseIP(raw); local != nil && local.Equal(ip) {
			return true
		}
	}
	return false
}

func broadcastAddrs(port int) []*net.UDPAddr {
	out := []*net.UDPAddr{{IP: net.IPv4bcast, Port: port}}
	seen := map[string]bool{"255.255.255.255": true}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || len(ipnet.Mask) != net.IPv4len {
				continue
			}
			b := make(net.IP, net.IPv4len)
			for i := 0; i < net.IPv4len; i++ {
				b[i] = ip4[i] | ^ipnet.Mask[i]
			}
			if raw := b.String(); !seen[raw] {
				seen[raw] = true
				out = append(out, &net.UDPAddr{IP: b, Port: port})
			}
		}
	}
	return out
}
