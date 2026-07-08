package dnsmasq

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// ParseLeases reads the dnsmasq lease database. A missing file simply means
// no leases. Format per line:
//
//	<expiry-unix> <MAC|IAID> <IP> <hostname|*> <client-id|*>
//
// IPv6 leases store an IAID in the MAC column and are preceded by a "duid"
// header line which is skipped.
func ParseLeases(path string) ([]DHCPLease, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []DHCPLease{}, nil
		}
		return nil, err
	}
	defer f.Close()

	leases := []DHCPLease{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(strings.TrimSpace(sc.Text()))
		if len(fields) < 4 || fields[0] == "duid" {
			continue
		}
		ts, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		l := DHCPLease{
			ExpiryUnix: ts,
			Expiry:     time.Unix(ts, 0),
			MACAddress: fields[1],
			IPAddress:  fields[2],
			IPv6:       strings.Contains(fields[2], ":"),
		}
		if fields[3] != "*" {
			l.Hostname = fields[3]
		}
		if len(fields) >= 5 && fields[4] != "*" {
			l.ClientID = fields[4]
		}
		leases = append(leases, l)
	}
	return leases, sc.Err()
}
