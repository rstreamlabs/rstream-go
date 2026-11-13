// See LICENSE file in the project root for license information.

package main

import (
	"errors"
	"net"
)

func localRelayIP() (net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil {
				return nil, err
			}
			if ip.To4() != nil && ip.IsGlobalUnicast() {
				return ip.To4(), nil
			}
		}
	}
	return nil, errors.New("WebRTC TURN fixture requires an active non-loopback IPv4 interface")
}
