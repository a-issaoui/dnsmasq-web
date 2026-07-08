package dnsmasq

import "time"

// DHCPLease is one row of the dnsmasq lease database.
type DHCPLease struct {
	MACAddress string    `json:"mac_address"`
	IPAddress  string    `json:"ip_address"`
	Hostname   string    `json:"hostname"`
	ClientID   string    `json:"client_id,omitempty"`
	Expiry     time.Time `json:"expiry"`
	ExpiryUnix int64     `json:"expiry_unix"`
	IPv6       bool      `json:"ipv6,omitempty"`
}

// ServiceStatus describes the dnsmasq systemd unit.
type ServiceStatus struct {
	Running   bool      `json:"running"`
	Active    bool      `json:"active"`
	Enabled   bool      `json:"enabled"`
	Status    string    `json:"status"`
	Uptime    string    `json:"uptime"`
	StartedAt time.Time `json:"started_at,omitempty"`
	PID       int       `json:"pid"`
	MemoryMB  float64   `json:"memory_mb"`
	Version   string    `json:"version,omitempty"`
}

// BackupInfo describes one configuration backup file.
type BackupInfo struct {
	Filename string    `json:"filename"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
}
