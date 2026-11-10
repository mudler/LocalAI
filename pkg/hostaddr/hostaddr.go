// Package hostaddr reads per-address kernel flags of the host's IP addresses.
package hostaddr

import (
	"bufio"
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"
)

// ifaFDeprecated is the kernel's IFA_F_DEPRECATED address flag.
const ifaFDeprecated = 0x20

// IfInet6Path lists the host's IPv6 addresses with their flags (Linux).
var IfInet6Path = "/proc/net/if_inet6"

// DeprecatedIPv6 returns the addresses that /proc/net/if_inet6 content marks
// deprecated. Lines read "<addr hex> <ifindex> <prefixlen> <scope> <flags>
// <ifname>", all numbers in hex.
func DeprecatedIPv6(content string) map[string]bool {
	dep := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || len(f[0]) != 32 {
			continue
		}
		flags, err := strconv.ParseUint(f[4], 16, 32)
		if err != nil || flags&ifaFDeprecated == 0 {
			continue
		}
		raw, err := hex.DecodeString(f[0])
		if err != nil {
			continue
		}
		dep[net.IP(raw).String()] = true
	}
	return dep
}

// NotDeprecated is an IP filter (pion SettingEngine.SetIPFilter) that drops deprecated IPv6 addresses.
//
// After an IPv6 prefix change (routers rotate it, often daily) the old
// addresses stay on the interface as "deprecated" until their valid lifetime
// runs out. pion still gathers host candidates on them, and the ICE checks
// over those pairs time out: the data channel never opens. The file is read on
// every call because the set changes while the server runs. Without the file
// (non-Linux) every address is kept.
func NotDeprecated(ip net.IP) bool {
	if ip.To4() != nil {
		return true
	}
	data, err := os.ReadFile(IfInet6Path)
	if err != nil {
		return true
	}
	return !DeprecatedIPv6(string(data))[ip.String()]
}
