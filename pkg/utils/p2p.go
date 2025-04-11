package utils

import "github.com/multiformats/go-multiaddr"

func StringsToMultiAddr(peers []string) []multiaddr.Multiaddr {
	res := []multiaddr.Multiaddr{}
	for _, p := range peers {
		addr, err := multiaddr.NewMultiaddr(p)
		if err != nil {
			continue
		}
		res = append(res, addr)
	}
	return res
}
