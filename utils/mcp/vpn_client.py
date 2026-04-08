"""
HTTP Client for VPN MultiTunnel Debug API
"""
import httpx
from typing import Any, Optional
from dataclasses import dataclass


@dataclass
class HostTestResult:
    hostname: str
    profile_id: str
    profile_name: str
    dns_resolved: bool
    real_ip: str
    loopback_ip: str
    dns_server: str
    dns_rule: str
    dns_error: Optional[str]
    tcp_connected: bool
    tcp_port: int
    tcp_latency_ms: int
    tcp_error: Optional[str]


@dataclass
class HostMappingInfo:
    hostname: str
    real_ip: str
    loopback_ip: str
    profile_id: str
    profile_name: str
    resolved_at: str
    expires_at: str


@dataclass
class DNSDiagnostic:
    hostname: str
    matched_rule: Optional[dict]
    all_rules: list
    would_resolve: bool
    reason: str
    suggested_fix: Optional[str]


class VPNDebugClient:
    """Client for the VPN MultiTunnel Debug API"""

    def __init__(self, base_url: str = "http://127.0.0.1:8765"):
        self.base_url = base_url.rstrip("/")
        self.client = httpx.Client(timeout=httpx.Timeout(300.0, connect=5.0))

    def _get(self, endpoint: str, params: Optional[dict] = None) -> dict:
        """Make a GET request to the API"""
        url = f"{self.base_url}{endpoint}"
        response = self.client.get(url, params=params)
        response.raise_for_status()
        data = response.json()
        if not data.get("success", False):
            raise Exception(data.get("error", "Unknown error"))
        return data.get("data")

    def _post(self, endpoint: str, json_data: dict) -> dict:
        """Make a POST request to the API"""
        url = f"{self.base_url}{endpoint}"
        response = self.client.post(url, json=json_data)
        response.raise_for_status()
        data = response.json()
        if not data.get("success", False):
            raise Exception(data.get("error", "Unknown error"))
        return data.get("data")

    def health_check(self) -> bool:
        """Check if the API is available"""
        try:
            self._get("/api/health")
            return True
        except Exception:
            return False

    def get_status(self) -> dict:
        """Get complete status of VPNs, DNS, and TCP proxy"""
        return self._get("/api/status")

    def get_vpn_status(self) -> list:
        """Get status of all VPN tunnels"""
        status = self.get_status()
        return status.get("vpns", [])

    def get_connect_errors(self) -> dict:
        """Get connection errors for all profiles"""
        return self._get("/api/connect-errors")

    def get_host_mappings(self) -> list[dict]:
        """Get all active host mappings"""
        return self._get("/api/host-mappings")

    def test_host(self, hostname: str, port: int = 443, profile_id: str = "", use_system_dns: bool = True) -> dict:
        """
        Test connectivity to a host through the VPN.
        Returns DNS resolution info and TCP connectivity test results.

        Args:
            hostname: The hostname to test
            port: TCP port to test (default: 443)
            profile_id: Optional profile ID to use
            use_system_dns: If True (default), resolve via system DNS (same path as real apps like DBeaver).
                           If False, resolve directly via VPN tunnel.
        """
        return self._post("/api/test-host", {
            "hostname": hostname,
            "port": port,
            "profileId": profile_id,
            "useSystemDNS": use_system_dns
        })

    def diagnose_dns(self, hostname: str) -> dict:
        """
        Diagnose DNS resolution for a hostname.
        Returns which rule matches, whether it would resolve, and suggestions.
        """
        return self._post("/api/diagnose-dns", {
            "hostname": hostname
        })

    def get_logs(
        self,
        level: str = "",
        component: str = "",
        profile_id: str = "",
        limit: int = 100
    ) -> list[dict]:
        """
        Get logs filtered by level, component, or profile.

        Args:
            level: Filter by log level (debug, info, warn, error)
            component: Filter by component (dns, tunnel, proxy, app, frontend:*)
            profile_id: Filter by VPN profile ID
            limit: Maximum number of logs to return
        """
        params = {"limit": str(limit)}
        if level:
            params["level"] = level
        if component:
            params["component"] = component
        if profile_id:
            params["profileId"] = profile_id
        return self._get("/api/logs", params)

    def get_frontend_logs(self, limit: int = 100) -> list[dict]:
        """Get logs from the frontend (React) only"""
        return self._get("/api/logs/frontend", {"limit": str(limit)})

    def get_errors(self, limit: int = 50) -> list[dict]:
        """Get recent errors"""
        return self._get("/api/errors", {"limit": str(limit)})

    def get_metrics(self) -> dict:
        """Get performance metrics"""
        return self._get("/api/metrics")

    def get_diagnostic_report(self) -> dict:
        """Generate a complete diagnostic report"""
        return self._post("/api/diagnostic", {})

    def get_openvpn_status(self) -> dict:
        """Get OpenVPN installation status (version, path, needsUpgrade)"""
        return self._get("/api/openvpn-status")

    def upgrade_openvpn(self) -> dict:
        """Download and install/upgrade OpenVPN to latest version"""
        return self._post("/api/openvpn-upgrade", {})

    def vpn_connect(self, profile_id: str) -> dict:
        """Connect a VPN profile"""
        return self._post("/api/vpn-connect", {"profileId": profile_id})

    def vpn_disconnect(self, profile_id: str) -> dict:
        """Disconnect a VPN profile"""
        return self._post("/api/vpn-disconnect", {"profileId": profile_id})

    def configure_dns(self) -> dict:
        """Configure system DNS to use the VPN DNS proxy"""
        return self._post("/api/dns-configure", {})

    def restore_dns(self) -> dict:
        """Restore original system DNS configuration"""
        return self._post("/api/dns-restore", {})

    def is_dns_configured(self) -> bool:
        """Check if system DNS is currently configured to use the proxy"""
        status = self.get_status()
        return status.get("dnsConfigured", False)

    def query_dns(
        self,
        hostname: str,
        profile_id: str,
        query_type: str = "A",
        dns_server: str = ""
    ) -> dict:
        """
        Query a DNS server through a VPN tunnel.

        Args:
            hostname: The hostname to query
            profile_id: The VPN profile to use
            query_type: DNS record type (A, AAAA, CNAME, MX, TXT, NS, SOA, PTR, ANY)
            dns_server: DNS server IP (optional, uses profile default if empty)
        """
        return self._post("/api/dns-query", {
            "hostname": hostname,
            "profileId": profile_id,
            "queryType": query_type,
            "dnsServer": dns_server
        })

    def close(self):
        """Close the HTTP client"""
        self.client.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()


# Convenience functions for quick access
_default_client: Optional[VPNDebugClient] = None


def get_client() -> VPNDebugClient:
    """Get or create the default client"""
    global _default_client
    if _default_client is None:
        _default_client = VPNDebugClient()
    return _default_client


def test_host(hostname: str, port: int = 443) -> dict:
    """Quick test of a host"""
    return get_client().test_host(hostname, port)


def diagnose_dns(hostname: str) -> dict:
    """Quick DNS diagnosis"""
    return get_client().diagnose_dns(hostname)


def get_status() -> dict:
    """Quick status check"""
    return get_client().get_status()


def get_logs(limit: int = 100) -> list[dict]:
    """Quick log retrieval"""
    return get_client().get_logs(limit=limit)


def get_errors(limit: int = 50) -> list[dict]:
    """Quick error retrieval"""
    return get_client().get_errors(limit=limit)
