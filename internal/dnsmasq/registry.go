package dnsmasq

// Directive kinds drive how the UI edits an option:
//
//	flag   — present/absent toggle (no value)
//	scalar — single value, one logical occurrence (last one wins in dnsmasq)
//	multi  — repeatable; each line is an independent record with CRUD
const (
	KindFlag   = "flag"
	KindScalar = "scalar"
	KindMulti  = "multi"
)

// Directive describes one dnsmasq option for the UI: where it lives, how it
// is edited and what its value syntax looks like.
type Directive struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Cat         string `json:"cat"`
	Label       string `json:"label"`
	Help        string `json:"help"`
	Syntax      string `json:"syntax,omitempty"`      // human syntax summary from the man page
	Placeholder string `json:"placeholder,omitempty"` // example value for simple inputs
}

// Category groups directives into UI sections.
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Page  string `json:"page"` // which page of the console renders it
}

var Categories = []Category{
	{"upstream", "Upstream & Forwarding", "dns"},
	{"records", "DNS Records", "dns"},
	{"domains", "Local Domains & Hosts", "dns"},
	{"cache", "Cache & TTL", "dns"},
	{"dnssec", "DNSSEC", "dns"},
	{"sets", "ipset / nftset", "dns"},
	{"filtering", "Rebind Protection & Filtering", "dns"},
	{"dhcp-core", "Ranges & Leases", "dhcp"},
	{"dhcp-hosts", "Static Hosts", "dhcp"},
	{"dhcp-options", "DHCP Options", "dhcp"},
	{"dhcp-boot", "Boot & PXE", "dhcp"},
	{"dhcp-tags", "Tags & Matching", "dhcp"},
	{"dhcp-v6", "IPv6 & Router Advertisement", "dhcp"},
	{"dhcp-relay", "DHCP Relay", "dhcp"},
	{"tftp", "TFTP Server", "tftp"},
	{"network", "Interfaces & Listening", "network"},
	{"logging", "Logging", "settings"},
	{"system", "System & Includes", "settings"},
}

// Registry is the full catalogue of directives the console edits with rich
// forms. Anything not listed here is still fully editable through the Config
// File explorer — nothing is off-limits, this just drives the nice UI.
var Registry = []Directive{
	// ── Upstream & Forwarding ────────────────────────────────────────────
	{Key: "server", Kind: KindMulti, Cat: "upstream", Label: "Upstream server",
		Syntax: "[/domain/[domain/]][server[#port]][@interface][@source-ip[#port]]",
		Help:   "Upstream DNS server. With /domain/ prefixes the server is only used for those domains (conditional forwarding). An empty server with /domain/ makes the domain local-only.", Placeholder: "1.1.1.1"},
	{Key: "rev-server", Kind: KindMulti, Cat: "upstream", Label: "Reverse server",
		Syntax: "<ip-address>[/<prefix-len>][,<server>][#port]",
		Help:   "Send reverse (address→name) queries for a subnet to a specific upstream server. Sugar for server=/x.x.in-addr.arpa/.", Placeholder: "192.168.1.0/24,192.168.1.1"},
	{Key: "local", Kind: KindMulti, Cat: "upstream", Label: "Local-only domain",
		Syntax: "/domain/[domain/]",
		Help:   "Domains answered only from local config or /etc/hosts — never forwarded upstream.", Placeholder: "/home.lan/"},
	{Key: "no-resolv", Kind: KindFlag, Cat: "upstream", Label: "Ignore /etc/resolv.conf",
		Help: "Don't read upstream servers from /etc/resolv.conf; use only servers configured here."},
	{Key: "resolv-file", Kind: KindScalar, Cat: "upstream", Label: "Alternate resolv file",
		Help: "Read upstream servers from this file instead of /etc/resolv.conf.", Placeholder: "/etc/resolv.dnsmasq"},
	{Key: "strict-order", Kind: KindFlag, Cat: "upstream", Label: "Strict server order",
		Help: "Query upstream servers strictly in the order they appear instead of picking the fastest."},
	{Key: "all-servers", Kind: KindFlag, Cat: "upstream", Label: "Query all servers",
		Help: "Send every query to every upstream server and return the first answer."},
	{Key: "domain-needed", Kind: KindFlag, Cat: "upstream", Label: "Require qualified names",
		Help: "Never forward plain names (without a dot or domain part) upstream."},
	{Key: "bogus-priv", Kind: KindFlag, Cat: "upstream", Label: "Block private reverse lookups",
		Help: "Answer reverse queries for RFC1918 ranges from local data only; never forward them upstream."},
	{Key: "localise-queries", Kind: KindFlag, Cat: "upstream", Label: "Localise /etc/hosts answers",
		Help: "Return the /etc/hosts address on the same subnet as the requesting interface when several exist."},
	{Key: "dns-loop-detect", Kind: KindFlag, Cat: "upstream", Label: "DNS loop detection",
		Help: "Detect and break forwarding loops through upstream servers."},

	// ── DNS Records ──────────────────────────────────────────────────────
	{Key: "address", Kind: KindMulti, Cat: "records", Label: "Wildcard address",
		Syntax: "/domain[/domain]/[ipaddr]",
		Help:   "Return this address for any host in the given domains (never forwarded). Without an address, returns NXDOMAIN — handy for blocking.", Placeholder: "/ads.example.com/0.0.0.0"},
	{Key: "host-record", Kind: KindMulti, Cat: "records", Label: "Host record (A/AAAA/PTR)",
		Syntax: "<name>[,<name>...],[IPv4],[IPv6][,TTL]",
		Help:   "Add A, AAAA and PTR records for one or more names.", Placeholder: "nas.home.lan,192.168.1.10"},
	{Key: "cname", Kind: KindMulti, Cat: "records", Label: "CNAME alias",
		Syntax: "<cname>[,<cname>],<target>[,TTL]",
		Help:   "Alias pointing at a target that must already be known to dnsmasq (from /etc/hosts, dhcp-host or host-record).", Placeholder: "www.home.lan,nas.home.lan"},
	{Key: "srv-host", Kind: KindMulti, Cat: "records", Label: "SRV record",
		Syntax: "<_service>.<_prot>[.domain],[target[,port[,priority[,weight]]]]",
		Help:   "Service-location record (RFC 2782).", Placeholder: "_ldap._tcp.example.com,ldapserver.example.com,389"},
	{Key: "txt-record", Kind: KindMulti, Cat: "records", Label: "TXT record",
		Syntax: "<name>[[,<text>],<text>]",
		Help:   "Text record; multiple comma-separated strings allowed, quote strings containing commas.", Placeholder: "example.com,\"v=spf1 a -all\""},
	{Key: "ptr-record", Kind: KindMulti, Cat: "records", Label: "PTR record",
		Syntax: "<name>[,<target>]",
		Help:   "Explicit reverse-lookup record.", Placeholder: "10.1.168.192.in-addr.arpa,nas.home.lan"},
	{Key: "mx-host", Kind: KindMulti, Cat: "records", Label: "MX record",
		Syntax: "<mx name>[[,<hostname>],<preference>]",
		Help:   "Mail-exchanger record pointing at hostname (or the mx-target / local host).", Placeholder: "example.com,mail.example.com,10"},
	{Key: "mx-target", Kind: KindScalar, Cat: "records", Label: "Default MX target",
		Help: "Default target for MX records when mx-host gives none.", Placeholder: "mail.example.com"},
	{Key: "naptr-record", Kind: KindMulti, Cat: "records", Label: "NAPTR record",
		Syntax: "<name>,<order>,<preference>,<flags>,<service>,<regexp>[,<replacement>]",
		Help:   "Naming-authority pointer record (RFC 3403).", Placeholder: "example.com,100,10,\"s\",\"SIP+D2U\",\"\",_sip._udp.example.com"},
	{Key: "caa-record", Kind: KindMulti, Cat: "records", Label: "CAA record",
		Syntax: "<name>,<flags>,<tag>,<value>",
		Help:   "Certification-authority authorisation record (RFC 6844).", Placeholder: "example.com,0,issue,letsencrypt.org"},
	{Key: "dns-rr", Kind: KindMulti, Cat: "records", Label: "Arbitrary DNS RR",
		Syntax: "<name>,<RR-number>,[<hex data>]",
		Help:   "Return any resource-record type as raw hex data.", Placeholder: "example.com,257,012345"},
	{Key: "interface-name", Kind: KindMulti, Cat: "records", Label: "Interface name",
		Syntax: "<name>,<interface>[/4|/6]",
		Help:   "DNS name that always resolves to the current address of the given local interface.", Placeholder: "router.home.lan,eth0"},

	// ── Local Domains & Hosts ────────────────────────────────────────────
	{Key: "domain", Kind: KindMulti, Cat: "domains", Label: "Domain",
		Syntax: "<domain>[,<address range>[,local]]",
		Help:   "The network's domain: qualifies DHCP hostnames and sets the domain DHCP option. With an address range it applies to a subnet only.", Placeholder: "home.lan"},
	{Key: "expand-hosts", Kind: KindFlag, Cat: "domains", Label: "Expand hosts",
		Help: "Append the domain to plain names from /etc/hosts and DHCP."},
	{Key: "no-hosts", Kind: KindFlag, Cat: "domains", Label: "Ignore /etc/hosts",
		Help: "Don't load names from /etc/hosts."},
	{Key: "addn-hosts", Kind: KindMulti, Cat: "domains", Label: "Additional hosts file",
		Help: "Extra hosts file(s) to read in addition to /etc/hosts.", Placeholder: "/etc/dnsmasq.hosts"},
	{Key: "hostsdir", Kind: KindMulti, Cat: "domains", Label: "Hosts directory",
		Help: "Directory of hosts files, re-read automatically via inotify when files change.", Placeholder: "/etc/hosts.d"},

	// ── Cache & TTL ──────────────────────────────────────────────────────
	{Key: "cache-size", Kind: KindScalar, Cat: "cache", Label: "Cache size",
		Help: "Number of cached DNS entries (default 150, 0 disables caching).", Placeholder: "1000"},
	{Key: "no-negcache", Kind: KindFlag, Cat: "cache", Label: "Disable negative cache",
		Help: "Don't cache NXDOMAIN and other negative answers."},
	{Key: "neg-ttl", Kind: KindScalar, Cat: "cache", Label: "Negative TTL (s)",
		Help: "TTL for negative answers from upstream that carry no TTL of their own.", Placeholder: "60"},
	{Key: "local-ttl", Kind: KindScalar, Cat: "cache", Label: "Local TTL (s)",
		Help: "TTL sent for answers from /etc/hosts, DHCP leases and local records (default 0).", Placeholder: "300"},
	{Key: "max-ttl", Kind: KindScalar, Cat: "cache", Label: "Max TTL sent (s)",
		Help: "Cap the TTL value sent to clients without changing what is cached.", Placeholder: "3600"},
	{Key: "max-cache-ttl", Kind: KindScalar, Cat: "cache", Label: "Max cache TTL (s)",
		Help: "Cap how long upstream answers stay in the cache.", Placeholder: "86400"},
	{Key: "min-cache-ttl", Kind: KindScalar, Cat: "cache", Label: "Min cache TTL (s)",
		Help: "Extend short upstream TTLs up to this value (capped at 1h — use sparingly).", Placeholder: "300"},
	{Key: "dns-forward-max", Kind: KindScalar, Cat: "cache", Label: "Max concurrent queries",
		Help: "Maximum concurrent forwarded DNS queries (default 150).", Placeholder: "150"},

	// ── DNSSEC ───────────────────────────────────────────────────────────
	{Key: "dnssec", Kind: KindFlag, Cat: "dnssec", Label: "Enable DNSSEC validation",
		Help: "Validate upstream answers and set the AD bit; requires at least one trust anchor."},
	{Key: "trust-anchor", Kind: KindMulti, Cat: "dnssec", Label: "Trust anchor",
		Syntax: "[<class>],<domain>,<key-tag>,<algorithm>,<digest-type>,<digest>",
		Help:   "DS-record trust anchor; normally the DNS root key.", Placeholder: ".,20326,8,2,E06D44B80B8F1D39A95C0B0D7C65D08458E880409BBC683457104237C7F8EC8D"},
	{Key: "dnssec-check-unsigned", Kind: KindFlag, Cat: "dnssec", Label: "Verify unsigned replies",
		Help: "Prove that unsigned answers come from genuinely unsigned zones (recommended with DNSSEC)."},
	{Key: "dnssec-no-timecheck", Kind: KindFlag, Cat: "dnssec", Label: "Skip time checks",
		Help: "Defer signature time validation until the clock is known-good (for systems without an RTC)."},
	{Key: "proxy-dnssec", Kind: KindFlag, Cat: "dnssec", Label: "Proxy DNSSEC data",
		Help: "Copy upstream validation results to clients instead of validating locally."},

	// ── ipset / nftset ───────────────────────────────────────────────────
	{Key: "ipset", Kind: KindMulti, Cat: "sets", Label: "ipset",
		Syntax: "/domain[/domain]/<ipset>[,<ipset>]",
		Help:   "Add the resolved addresses of the given domains to Netfilter IP sets.", Placeholder: "/netflix.com/vpn_bypass"},
	{Key: "nftset", Kind: KindMulti, Cat: "sets", Label: "nftset",
		Syntax: "/domain[/domain]/[(4|6)#[family#]table#set]",
		Help:   "Add resolved addresses to nftables sets (must already exist).", Placeholder: "/example.com/4#ip#filter#allowed"},
	{Key: "connmark-allowlist-enable", Kind: KindScalar, Cat: "sets", Label: "Connmark allowlist",
		Help: "Enable DNS-name allowlisting based on connection-track marks.", Placeholder: "0x01"},

	// ── Rebind Protection & Filtering ────────────────────────────────────
	{Key: "stop-dns-rebind", Kind: KindFlag, Cat: "filtering", Label: "Block DNS rebind",
		Help: "Reject upstream answers containing private IP ranges (anti-rebinding protection)."},
	{Key: "rebind-localhost-ok", Kind: KindFlag, Cat: "filtering", Label: "Allow 127.0.0.0/8 rebind",
		Help: "Exempt the localhost range from rebind protection (needed for some RBL servers)."},
	{Key: "rebind-domain-ok", Kind: KindMulti, Cat: "filtering", Label: "Rebind-exempt domain",
		Syntax: "[/]domain[/[domain]...]",
		Help:   "Domains allowed to resolve to private addresses despite rebind protection.", Placeholder: "/plex.direct/"},
	{Key: "bogus-nxdomain", Kind: KindMulti, Cat: "filtering", Label: "Bogus NXDOMAIN address",
		Syntax: "<ipaddr>[/prefix]",
		Help:   "Transform answers containing this address into NXDOMAIN (defeats ISP ad redirects).", Placeholder: "64.94.110.11"},
	{Key: "ignore-address", Kind: KindMulti, Cat: "filtering", Label: "Ignore address",
		Syntax: "<ipaddr>[/prefix]",
		Help:   "Silently drop any upstream answer containing this address.", Placeholder: "192.0.2.1"},
	{Key: "filterwin2k", Kind: KindFlag, Cat: "filtering", Label: "Filter Windows DNS noise",
		Help: "Drop periodic Windows queries that can wake dial-on-demand links."},
	{Key: "filter-A", Kind: KindFlag, Cat: "filtering", Label: "Filter A records",
		Help: "Remove IPv4 addresses from answers."},
	{Key: "filter-AAAA", Kind: KindFlag, Cat: "filtering", Label: "Filter AAAA records",
		Help: "Remove IPv6 addresses from answers (force IPv4-only resolution)."},

	// ── DHCP: Ranges & Leases ────────────────────────────────────────────
	{Key: "dhcp-range", Kind: KindMulti, Cat: "dhcp-core", Label: "DHCP range",
		Syntax: "[tag:<tag>,][set:<tag>,]<start>[,<end>|<mode>][,<netmask>[,<broadcast>]][,<lease time>] — IPv6: <start>[,<end>|constructor:<iface>][,ra-only|slaac|ra-names|ra-stateless][,<prefix-len>][,<lease>]",
		Help:   "Enables the DHCP/DHCPv6/RA server for a subnet. Mode 'static' serves only dhcp-host entries; 'proxy' enables PXE proxy-DHCP; IPv6 modes control router advertisement.", Placeholder: "192.168.1.100,192.168.1.200,255.255.255.0,12h"},
	{Key: "dhcp-authoritative", Kind: KindFlag, Cat: "dhcp-core", Label: "Authoritative mode",
		Help: "Claim authority for the subnet: take over unknown leases instead of ignoring them. Recommended when this is the only DHCP server."},
	{Key: "dhcp-rapid-commit", Kind: KindFlag, Cat: "dhcp-core", Label: "Rapid commit",
		Help: "Two-message DHCP exchange (RFC 4039) for faster lease acquisition."},
	{Key: "dhcp-lease-max", Kind: KindScalar, Cat: "dhcp-core", Label: "Max leases",
		Help: "Upper bound on concurrent leases (default 1000) — denial-of-service protection.", Placeholder: "1000"},
	{Key: "dhcp-leasefile", Kind: KindScalar, Cat: "dhcp-core", Label: "Lease file",
		Help: "Where the lease database is stored.", Placeholder: "/var/lib/dnsmasq/dnsmasq.leases"},
	{Key: "dhcp-sequential-ip", Kind: KindFlag, Cat: "dhcp-core", Label: "Sequential IPs",
		Help: "Allocate addresses in order instead of hashing the MAC (leases may move between hosts)."},
	{Key: "no-ping", Kind: KindFlag, Cat: "dhcp-core", Label: "Skip conflict ping",
		Help: "Don't ICMP-probe an address before allocating it (faster, small conflict risk)."},
	{Key: "dhcp-ttl", Kind: KindScalar, Cat: "dhcp-core", Label: "DHCP name TTL (s)",
		Help: "TTL for DNS answers about DHCP leases.", Placeholder: "300"},
	{Key: "dhcp-reply-delay", Kind: KindMulti, Cat: "dhcp-core", Label: "Reply delay",
		Syntax: "[tag:<tag>,]<seconds>",
		Help:   "Delay DHCP replies (some PXE ROMs need time to get ready).", Placeholder: "2"},

	// ── DHCP: Static Hosts ───────────────────────────────────────────────
	{Key: "dhcp-host", Kind: KindMulti, Cat: "dhcp-hosts", Label: "Static host",
		Syntax: "[<hwaddr>][,id:<client_id>|*][,set:<tag>][,tag:<tag>][,<ipaddr>][,<hostname>][,<lease_time>][,ignore]",
		Help:   "Per-host settings: fixed IP, hostname, lease time or 'ignore' keyed on MAC / client-id.", Placeholder: "aa:bb:cc:dd:ee:ff,192.168.1.50,nas,infinite"},
	{Key: "dhcp-hostsfile", Kind: KindScalar, Cat: "dhcp-hosts", Label: "Hosts file",
		Help: "External file of dhcp-host entries, re-read on SIGHUP without restart.", Placeholder: "/etc/dnsmasq.d/hosts.conf"},
	{Key: "dhcp-hostsdir", Kind: KindScalar, Cat: "dhcp-hosts", Label: "Hosts directory",
		Help: "Directory of dhcp-host files, picked up automatically via inotify.", Placeholder: "/etc/dnsmasq-hosts.d"},
	{Key: "read-ethers", Kind: KindFlag, Cat: "dhcp-hosts", Label: "Read /etc/ethers",
		Help: "Load static host mappings from /etc/ethers."},
	{Key: "dhcp-fqdn", Kind: KindFlag, Cat: "dhcp-hosts", Label: "Require FQDNs",
		Help: "Only insert fully-qualified DHCP names into DNS (requires a domain to be set)."},
	{Key: "dhcp-ignore-names", Kind: KindMulti, Cat: "dhcp-hosts", Label: "Ignore client names",
		Syntax: "[tag:<tag>[,tag:<tag>]]",
		Help:   "Don't trust hostnames sent by (matching) clients.", Placeholder: "tag:untrusted"},

	// ── DHCP: Options ────────────────────────────────────────────────────
	{Key: "dhcp-option", Kind: KindMulti, Cat: "dhcp-options", Label: "DHCP option",
		Syntax: "[tag:<tag>,][encap:<opt>,][vendor:[<class>],]<opt>|option:<name>|option6:<name>,[<value>]",
		Help:   "Send an option to (matching) clients: 3=router, 6=dns-server, 15=domain, 42=ntp… Empty value suppresses the default.", Placeholder: "option:router,192.168.1.1"},
	{Key: "dhcp-option-force", Kind: KindMulti, Cat: "dhcp-options", Label: "Forced option",
		Syntax: "same as dhcp-option",
		Help:   "Send the option even if the client didn't request it (PXE menus etc.).", Placeholder: "209,configs/common"},
	{Key: "dhcp-optsfile", Kind: KindScalar, Cat: "dhcp-options", Label: "Options file",
		Help: "External file of dhcp-option entries, re-read on SIGHUP.", Placeholder: "/etc/dnsmasq.d/opts.conf"},
	{Key: "dhcp-optsdir", Kind: KindScalar, Cat: "dhcp-options", Label: "Options directory",
		Help: "Directory of option files, picked up via inotify.", Placeholder: "/etc/dnsmasq-opts.d"},

	// ── DHCP: Boot & PXE ─────────────────────────────────────────────────
	{Key: "dhcp-boot", Kind: KindMulti, Cat: "dhcp-boot", Label: "Boot file",
		Syntax: "[tag:<tag>,]<filename>[,<servername>[,<server address>]]",
		Help:   "BOOTP/netboot parameters: boot file, TFTP server name and address.", Placeholder: "pxelinux.0,server,192.168.1.5"},
	{Key: "pxe-prompt", Kind: KindMulti, Cat: "dhcp-boot", Label: "PXE prompt",
		Syntax: "[tag:<tag>,]<prompt>[,<timeout>]",
		Help:   "Boot-menu prompt and timeout shown by PXE clients.", Placeholder: "\"Press F8 for menu\",10"},
	{Key: "pxe-service", Kind: KindMulti, Cat: "dhcp-boot", Label: "PXE service",
		Syntax: "[tag:<tag>,]<CSA>,<menu text>[,<basename>|<bootservicetype>][,<server address>]",
		Help:   "PXE boot-menu entry. CSA: x86PC, X86-64_EFI, ARM64_EFI…", Placeholder: "x86PC,\"Install Linux\",pxelinux"},
	{Key: "dhcp-match", Kind: KindMulti, Cat: "dhcp-boot", Label: "Option match → tag",
		Syntax: "set:<tag>,<option number>|option:<name>[,<value>]",
		Help:   "Set a tag when a client request contains a given option/value (e.g. detect PXE arch).", Placeholder: "set:efi-x86_64,option:client-arch,7"},
	{Key: "dhcp-broadcast", Kind: KindMulti, Cat: "dhcp-boot", Label: "Force broadcast",
		Syntax: "[tag:<tag>[,tag:<tag>]]",
		Help:   "Always use broadcast replies for (matching) clients — needed by some old BOOTP devices.", Placeholder: "tag:needs-broadcast"},

	// ── DHCP: Tags & Matching ────────────────────────────────────────────
	{Key: "dhcp-mac", Kind: KindMulti, Cat: "dhcp-tags", Label: "MAC → tag",
		Syntax: "set:<tag>,<MAC address>",
		Help:   "Set a tag for hosts matching a MAC pattern (wildcards allowed).", Placeholder: "set:printers,00:11:22:*:*:*"},
	{Key: "dhcp-vendorclass", Kind: KindMulti, Cat: "dhcp-tags", Label: "Vendor class → tag",
		Syntax: "set:<tag>,[enterprise:<number>,]<vendor-class>",
		Help:   "Set a tag from the DHCP vendor-class string (substring match).", Placeholder: "set:android,dhcpcd"},
	{Key: "dhcp-userclass", Kind: KindMulti, Cat: "dhcp-tags", Label: "User class → tag",
		Syntax: "set:<tag>,<user-class>",
		Help:   "Set a tag from the DHCP user-class string (substring match).", Placeholder: "set:kiosk,kiosk-machines"},
	{Key: "dhcp-circuitid", Kind: KindMulti, Cat: "dhcp-tags", Label: "Circuit-ID → tag",
		Syntax: "set:<tag>,<circuit-id>",
		Help:   "Tag from relay-agent circuit-id (option 82).", Placeholder: "set:floor1,00:01"},
	{Key: "dhcp-remoteid", Kind: KindMulti, Cat: "dhcp-tags", Label: "Remote-ID → tag",
		Syntax: "set:<tag>,<remote-id>",
		Help:   "Tag from relay-agent remote-id (option 82).", Placeholder: "set:site-a,aa:bb"},
	{Key: "dhcp-subscrid", Kind: KindMulti, Cat: "dhcp-tags", Label: "Subscriber-ID → tag",
		Syntax: "set:<tag>,<subscriber-id>",
		Help:   "Tag from relay-agent subscriber-id.", Placeholder: "set:cust1,customer-1"},
	{Key: "dhcp-name-match", Kind: KindMulti, Cat: "dhcp-tags", Label: "Name → tag",
		Syntax: "set:<tag>,<name>[*]",
		Help:   "Set a tag when the client supplies a matching hostname (trailing * globs).", Placeholder: "set:phones,iphone*"},
	{Key: "dhcp-ignore", Kind: KindMulti, Cat: "dhcp-tags", Label: "Ignore clients",
		Syntax: "tag:<tag>[,tag:<tag>]",
		Help:   "Never respond to clients matching all given tags (e.g. tag:!known to serve only known hosts).", Placeholder: "tag:!known"},
	{Key: "tag-if", Kind: KindMulti, Cat: "dhcp-tags", Label: "Tag expression",
		Syntax: "set:<tag>[,set:<tag>][,tag:<tag>[,tag:<tag>]]",
		Help:   "Boolean combination: set tags when all tag: conditions (possibly negated with !) hold.", Placeholder: "set:trusted,tag:known,tag:!guest"},

	// ── DHCP: IPv6 & RA ──────────────────────────────────────────────────
	{Key: "enable-ra", Kind: KindFlag, Cat: "dhcp-v6", Label: "Enable router advertisements",
		Help: "Send IPv6 router advertisements on subnets with dhcp-range entries."},
	{Key: "ra-param", Kind: KindMulti, Cat: "dhcp-v6", Label: "RA parameters",
		Syntax: "<interface>,[mtu:<n>|<iface>|off,][high|low,]<ra-interval>[,<router lifetime>]",
		Help:   "Tune router-advertisement interval, priority, MTU and lifetime per interface.", Placeholder: "eth0,high,60,1800"},
	{Key: "dhcp-duid", Kind: KindScalar, Cat: "dhcp-v6", Label: "Server DUID",
		Syntax: "<enterprise-id>,<uid>",
		Help:   "Force a specific DHCPv6 server DUID.", Placeholder: "1234,00:11:22:33"},
	{Key: "quiet-ra", Kind: KindFlag, Cat: "dhcp-v6", Label: "Quiet RA logging",
		Help: "Suppress router-advertisement log lines when log-dhcp is on."},

	// ── DHCP Relay ───────────────────────────────────────────────────────
	{Key: "dhcp-relay", Kind: KindMulti, Cat: "dhcp-relay", Label: "Relay",
		Syntax: "<local address>[,<server address>[#port]][,<interface>]",
		Help:   "Relay DHCP requests arriving on the local address to another DHCP server.", Placeholder: "192.168.1.1,10.0.0.1"},

	// ── TFTP ─────────────────────────────────────────────────────────────
	{Key: "enable-tftp", Kind: KindFlag, Cat: "tftp", Label: "Enable TFTP server",
		Help: "Built-in read-only TFTP server for network boot."},
	{Key: "tftp-root", Kind: KindMulti, Cat: "tftp", Label: "TFTP root",
		Syntax: "<directory>[,<interface>]",
		Help:   "Directory served over TFTP (optionally per interface); requests outside it are refused.", Placeholder: "/srv/tftp"},
	{Key: "tftp-secure", Kind: KindFlag, Cat: "tftp", Label: "Secure mode",
		Help: "Only serve files owned by the dnsmasq user."},
	{Key: "tftp-no-fail", Kind: KindFlag, Cat: "tftp", Label: "Tolerate missing root",
		Help: "Don't abort startup if the TFTP root is unavailable."},
	{Key: "tftp-unique-root", Kind: KindScalar, Cat: "tftp", Label: "Unique root per client",
		Syntax: "[ip|mac]",
		Help:   "Prefix the root with the client's IP or MAC for per-client boot directories.", Placeholder: "mac"},
	{Key: "tftp-lowercase", Kind: KindFlag, Cat: "tftp", Label: "Lowercase filenames",
		Help: "Convert requested filenames to lowercase."},
	{Key: "tftp-max", Kind: KindScalar, Cat: "tftp", Label: "Max connections",
		Help: "Maximum concurrent TFTP transfers (default 50).", Placeholder: "50"},
	{Key: "tftp-mtu", Kind: KindScalar, Cat: "tftp", Label: "MTU ceiling",
		Help: "Cap the blocksize negotiated with clients.", Placeholder: "1400"},
	{Key: "tftp-no-blocksize", Kind: KindFlag, Cat: "tftp", Label: "No blocksize negotiation",
		Help: "Refuse the blocksize option (helps some broken boot ROMs)."},
	{Key: "tftp-port-range", Kind: KindScalar, Cat: "tftp", Label: "Port range",
		Syntax: "<start>,<end>",
		Help:   "Restrict TFTP transfer ports (for firewalls).", Placeholder: "50000,50100"},
	{Key: "tftp-single-port", Kind: KindFlag, Cat: "tftp", Label: "Single port",
		Help: "Do all transfers from port 69 (helps NAT traversal)."},

	// ── Interfaces & Listening ───────────────────────────────────────────
	{Key: "interface", Kind: KindMulti, Cat: "network", Label: "Interface",
		Help: "Listen on this interface (repeatable). Loopback is added automatically.", Placeholder: "eth0"},
	{Key: "except-interface", Kind: KindMulti, Cat: "network", Label: "Excluded interface",
		Help: "Never listen on this interface (wins over interface=).", Placeholder: "wlan0"},
	{Key: "listen-address", Kind: KindMulti, Cat: "network", Label: "Listen address",
		Help: "Listen on this IP address (works with or without interface=).", Placeholder: "192.168.1.1"},
	{Key: "no-dhcp-interface", Kind: KindMulti, Cat: "network", Label: "DNS-only interface",
		Help: "Provide DNS but not DHCP/TFTP on this interface.", Placeholder: "eth1"},
	{Key: "bind-interfaces", Kind: KindFlag, Cat: "network", Label: "Bind interfaces",
		Help: "Bind individual addresses instead of the wildcard — required to coexist with other DNS servers."},
	{Key: "bind-dynamic", Kind: KindFlag, Cat: "network", Label: "Bind dynamically",
		Help: "Like bind-interfaces but picks up interfaces that appear later (Linux only)."},
	{Key: "port", Kind: KindScalar, Cat: "network", Label: "DNS port",
		Help: "Listen port for DNS (0 disables DNS entirely — DHCP/TFTP-only mode).", Placeholder: "53"},
	{Key: "local-service", Kind: KindFlag, Cat: "network", Label: "Local subnets only",
		Help: "Accept DNS queries only from directly-connected subnets (default when no interface config given)."},

	// ── Logging ──────────────────────────────────────────────────────────
	{Key: "log-queries", Kind: KindScalar, Cat: "logging", Label: "Log queries",
		Syntax: "[=extra|proto]",
		Help:   "Log every DNS query. Set to 'extra' for request IDs and client addresses (needed for the live query stream detail).", Placeholder: "extra"},
	{Key: "log-dhcp", Kind: KindFlag, Cat: "logging", Label: "Log DHCP",
		Help: "Verbose logging of all DHCP transactions and options."},
	{Key: "log-facility", Kind: KindScalar, Cat: "logging", Label: "Log facility / file",
		Help: "Syslog facility, or an absolute path to log straight to a file ('-' for stderr).", Placeholder: "DAEMON"},
	{Key: "log-async", Kind: KindScalar, Cat: "logging", Label: "Async log lines",
		Help: "Queue up to N log lines asynchronously so slow syslog never blocks dnsmasq.", Placeholder: "25"},
	{Key: "quiet-dhcp", Kind: KindFlag, Cat: "logging", Label: "Quiet DHCPv4",
		Help: "Suppress routine DHCPv4 log lines."},
	{Key: "quiet-dhcp6", Kind: KindFlag, Cat: "logging", Label: "Quiet DHCPv6",
		Help: "Suppress routine DHCPv6 log lines."},

	// ── System & Includes ────────────────────────────────────────────────
	{Key: "user", Kind: KindScalar, Cat: "system", Label: "Run as user",
		Help: "Drop privileges to this user after startup (default 'nobody').", Placeholder: "dnsmasq"},
	{Key: "group", Kind: KindScalar, Cat: "system", Label: "Run as group",
		Help: "Group to run as (default 'dip' where it exists).", Placeholder: "dnsmasq"},
	{Key: "pid-file", Kind: KindScalar, Cat: "system", Label: "PID file",
		Help: "Where to write the process ID.", Placeholder: "/run/dnsmasq.pid"},
	{Key: "edns-packet-max", Kind: KindScalar, Cat: "system", Label: "EDNS0 packet max",
		Help: "Maximum UDP EDNS.0 packet size advertised (default 1232).", Placeholder: "1232"},
	{Key: "conf-file", Kind: KindMulti, Cat: "system", Label: "Include file",
		Help: "Read an additional configuration file.", Placeholder: "/etc/dnsmasq.more.conf"},
	{Key: "conf-dir", Kind: KindMulti, Cat: "system", Label: "Include directory",
		Syntax: "<directory>[,<file-extension>...]",
		Help:   "Read all files in a directory as configuration (optionally filtered by extension).", Placeholder: "/etc/dnsmasq.d/,*.conf"},
	{Key: "servers-file", Kind: KindScalar, Cat: "system", Label: "Servers file",
		Help: "File of server= lines re-read on SIGHUP without a restart.", Placeholder: "/etc/dnsmasq.servers"},
}

// registryIndex allows O(1) lookup by key.
var registryIndex = func() map[string]*Directive {
	m := make(map[string]*Directive, len(Registry))
	for i := range Registry {
		m[Registry[i].Key] = &Registry[i]
	}
	return m
}()

// LookupDirective returns the registry entry for a key, if known.
func LookupDirective(key string) (*Directive, bool) {
	d, ok := registryIndex[key]
	return d, ok
}
