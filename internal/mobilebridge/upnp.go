package mobilebridge

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/huin/goupnp/dcps/internetgateway2"
)

// ProbeUPnP discovers the local gateway via SSDP and attempts to create a port
// mapping (AddPortMapping) to obtain a server-reflexive candidate without STUN.
// This helps when both peers are behind consumer NAT routers that support UPnP-IGD.
//
// Returns the external IP + external port on success, or empty strings on failure.
// 3s timeout, silent on failure (graceful degradation to STUN).
func ProbeUPnP(localPort int) (externalIP string, externalPort int) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	clients, errs := internetgateway2.NewWANIPConnection1Clients()
	if len(errs) > 0 || len(clients) == 0 {
		// try WANPPPConnection (older routers)
		clients2, _ := internetgateway2.NewWANPPPConnection1Clients()
		clients = append(clients, clients2...)
	}
	if len(clients) == 0 {
		return "", 0
	}
	c := clients[0]

	// Get external IP
	extIP, err := c.GetExternalIPAddress(ctx)
	if err != nil {
		return "", 0
	}

	extPort := localPort
	// AddPortMapping: map external:extPort → internal:localPort (UDP)
	err = c.AddPortMapping(ctx, "", uint16(extPort), "UDP",
		localIP(), uint16(localPort), "linkpeer", 0)
	if err != nil {
		return "", 0
	}

	return extIP.String(), extPort
}

// localIP returns the first non-loopback IPv4 address.
func localIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "0.0.0.0"
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String()
			}
		}
	}
	return "0.0.0.0"
}

// UPnPCandidate generates a srflx-style ICE candidate from a UPnP port mapping.
// Returns empty string if UPnP unavailable.
func UPnPCandidate(localPort int) string {
	extIP, extPort := ProbeUPnP(localPort)
	if extIP == "" {
		return ""
	}
	return fmt.Sprintf("candidate:upnp 1 udp 2113929471 %s %d typ srflx raddr 0.0.0.0 rport 0", extIP, extPort)
}
