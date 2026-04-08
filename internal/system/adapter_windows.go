package system

import (
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AdapterInfo holds information about a network adapter
type AdapterInfo struct {
	Name        string   // Friendly name (e.g., "OpenVPN TAP-Windows6")
	Description string   // Adapter description
	IPv4Addrs   []string // IPv4 addresses assigned to this adapter
	IPv6Addrs   []string // IPv6 addresses assigned to this adapter
	DNSServers  []string // DNS servers configured on this adapter
	OperStatus  uint32   // Operational status (1 = Up)
}

// IsUp returns whether the adapter is operationally up
func (adapter_info *AdapterInfo) IsUp() bool {
	return adapter_info.OperStatus == 1 // windows.IfOperStatusUp
}

// GetAllAdapters returns information about all network adapters on the system.
// Uses the Win32 GetAdaptersAddresses API for fast, reliable results.
func GetAllAdapters() ([]AdapterInfo, error) {
	const gaaFlags = windows.GAA_FLAG_INCLUDE_PREFIX | windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST

	var bufferSize uint32
	windows.GetAdaptersAddresses(syscall.AF_UNSPEC, gaaFlags, 0, nil, &bufferSize)
	if bufferSize == 0 {
		return nil, fmt.Errorf("GetAdaptersAddresses returned zero buffer size")
	}

	adapterBuffer := make([]byte, bufferSize)
	firstAdapter := (*windows.IpAdapterAddresses)(unsafe.Pointer(&adapterBuffer[0]))

	getAdaptersErr := windows.GetAdaptersAddresses(syscall.AF_UNSPEC, gaaFlags, 0, firstAdapter, &bufferSize)
	if getAdaptersErr != nil {
		return nil, fmt.Errorf("GetAdaptersAddresses failed: %w", getAdaptersErr)
	}

	var adapterList []AdapterInfo
	for currentAdapter := firstAdapter; currentAdapter != nil; currentAdapter = currentAdapter.Next {
		adapterEntry := AdapterInfo{
			Name:        windows.UTF16PtrToString(currentAdapter.FriendlyName),
			Description: windows.UTF16PtrToString(currentAdapter.Description),
			OperStatus:  currentAdapter.OperStatus,
		}

		// Collect unicast addresses
		for unicastAddr := currentAdapter.FirstUnicastAddress; unicastAddr != nil; unicastAddr = unicastAddr.Next {
			socketAddr := unicastAddr.Address
			if socketAddr.Sockaddr == nil {
				continue
			}
			rawSockaddr := (*syscall.RawSockaddrAny)(unsafe.Pointer(socketAddr.Sockaddr))
			sockaddrPtr, parseErr := rawSockaddr.Sockaddr()
			if parseErr != nil {
				continue
			}
			switch typedAddr := sockaddrPtr.(type) {
			case *syscall.SockaddrInet4:
				adapterEntry.IPv4Addrs = append(adapterEntry.IPv4Addrs, net.IP(typedAddr.Addr[:]).String())
			case *syscall.SockaddrInet6:
				adapterEntry.IPv6Addrs = append(adapterEntry.IPv6Addrs, net.IP(typedAddr.Addr[:]).String())
			}
		}

		// Collect DNS servers
		for dnsAddr := currentAdapter.FirstDnsServerAddress; dnsAddr != nil; dnsAddr = dnsAddr.Next {
			socketAddr := dnsAddr.Address
			if socketAddr.Sockaddr == nil {
				continue
			}
			rawSockaddr := (*syscall.RawSockaddrAny)(unsafe.Pointer(socketAddr.Sockaddr))
			sockaddrPtr, parseErr := rawSockaddr.Sockaddr()
			if parseErr != nil {
				continue
			}
			switch typedAddr := sockaddrPtr.(type) {
			case *syscall.SockaddrInet4:
				adapterEntry.DNSServers = append(adapterEntry.DNSServers, net.IP(typedAddr.Addr[:]).String())
			case *syscall.SockaddrInet6:
				adapterEntry.DNSServers = append(adapterEntry.DNSServers, net.IP(typedAddr.Addr[:]).String())
			}
		}

		adapterList = append(adapterList, adapterEntry)
	}

	return adapterList, nil
}

// FindAdapterByNameSubstring finds an adapter whose friendly name contains the given substring (case-insensitive).
// Returns nil if no matching adapter is found.
func FindAdapterByNameSubstring(nameSubstring string) (*AdapterInfo, error) {
	allAdapters, listErr := GetAllAdapters()
	if listErr != nil {
		return nil, listErr
	}

	lowerSubstring := strings.ToLower(nameSubstring)
	for _, adapter := range allAdapters {
		if strings.Contains(strings.ToLower(adapter.Name), lowerSubstring) {
			matchedAdapter := adapter
			return &matchedAdapter, nil
		}
	}
	return nil, nil
}

// FindAdapterByDescriptionSubstring finds an adapter whose description contains the given substring (case-insensitive).
func FindAdapterByDescriptionSubstring(descriptionSubstring string) (*AdapterInfo, error) {
	allAdapters, listErr := GetAllAdapters()
	if listErr != nil {
		return nil, listErr
	}

	lowerSubstring := strings.ToLower(descriptionSubstring)
	for _, adapter := range allAdapters {
		if strings.Contains(strings.ToLower(adapter.Description), lowerSubstring) {
			matchedAdapter := adapter
			return &matchedAdapter, nil
		}
	}
	return nil, nil
}

// WaitForAdapterIP polls for a network adapter matching the given name substring to appear
// and have an IPv4 address assigned. Returns the adapter info once ready, or error on timeout.
func WaitForAdapterIP(nameSubstring string, pollInterval time.Duration, timeout time.Duration) (*AdapterInfo, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		matchedAdapter, findErr := FindAdapterByNameSubstring(nameSubstring)
		if findErr != nil {
			return nil, fmt.Errorf("error scanning adapters: %w", findErr)
		}

		if matchedAdapter != nil && matchedAdapter.IsUp() && len(matchedAdapter.IPv4Addrs) > 0 {
			return matchedAdapter, nil
		}

		time.Sleep(pollInterval)
	}

	return nil, fmt.Errorf("timeout waiting for adapter '%s' to get an IP address", nameSubstring)
}
