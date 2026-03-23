#!/usr/bin/env python3
"""
MCP Server for VPN MultiTunnel Debugging

This server provides tools to debug VPN connections, DNS resolution,
host mappings, and view logs/errors from the VPN MultiTunnel application.

Usage:
    python vpn_mcp_server.py

Configure in Claude Code settings:
    {
        "mcpServers": {
            "vpn-debug": {
                "command": "python",
                "args": ["path/to/mcp/vpn_mcp_server.py"]
            }
        }
    }
"""

import json
import os
import shutil
import socket
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

from mcp.server import Server
from mcp.server.stdio import stdio_server
from mcp.types import Tool, TextContent

from vpn_client import VPNDebugClient

# Ensure Go binaries (wails, go) are in PATH for subprocess calls
_go_bin_path = Path.home() / "go" / "bin"
if str(_go_bin_path) not in os.environ.get("PATH", ""):
    os.environ["PATH"] = str(_go_bin_path) + os.pathsep + os.environ.get("PATH", "")


# Path to the VPN executable - prefer production (has service connection), fallback to dev
VPN_APP_PROD_DIR = Path("C:/Program Files/VPNMultiTunnel/VPNMultiTunnel")
PROJECT_ROOT = Path(__file__).parent.parent.parent
VPN_APP_DEV_PATH = PROJECT_ROOT / "build" / "bin" / "VPNMultiTunnel.exe"
VPN_APP_PATH = VPN_APP_PROD_DIR / "VPNMultiTunnel.exe" if VPN_APP_PROD_DIR.exists() else VPN_APP_DEV_PATH

# Paths for service/app update
VPN_SERVICE_DEV_PATH = PROJECT_ROOT / "build" / "bin" / "VPNMultiTunnel-service.exe"
VPN_SERVICE_PROD_PATH = VPN_APP_PROD_DIR / "VPNMultiTunnel-service.exe"
VPN_APP_PROD_PATH = VPN_APP_PROD_DIR / "VPNMultiTunnel.exe"


# Initialize the MCP server
server = Server("vpn-debug")

# Initialize the VPN debug client
vpn_client = VPNDebugClient()


def format_json(data: Any) -> str:
    """Format data as pretty JSON"""
    return json.dumps(data, indent=2, default=str)


def format_host_test_result(result: dict) -> str:
    """Format a host test result for display"""
    dns_method = "System DNS → DNS Proxy" if result.get('usedSystemDNS') else "Direct VPN Tunnel"
    lines = [
        f"# Host Test: {result.get('hostname', 'unknown')}",
        f"_Resolution method: {dns_method}_",
        "",
        "## DNS Resolution",
        f"- Resolved: {'✅ Yes' if result.get('dnsResolved') else '❌ No'}",
    ]

    if result.get('dnsResolved'):
        lines.extend([
            f"- Real IP: {result.get('realIP', 'N/A')}",
            f"- Loopback IP: {result.get('loopbackIP', 'N/A')}",
            f"- DNS Server: {result.get('dnsServer', 'N/A')}",
            f"- Matched Rule: {result.get('dnsRule', 'N/A')}",
        ])
    elif result.get('dnsError'):
        lines.append(f"- Error: {result.get('dnsError')}")

    lines.extend([
        "",
        "## TCP Connectivity",
        f"- Connected: {'✅ Yes' if result.get('tcpConnected') else '❌ No'}",
        f"- Port: {result.get('tcpPort', 'N/A')}",
    ])

    if result.get('tcpConnected'):
        lines.append(f"- Latency: {result.get('tcpLatencyMs', 0)}ms")
    elif result.get('tcpError'):
        lines.append(f"- Error: {result.get('tcpError')}")

    if result.get('profileName'):
        lines.extend([
            "",
            "## Profile",
            f"- Name: {result.get('profileName')}",
            f"- ID: {result.get('profileId')}",
        ])

    return "\n".join(lines)


def format_dns_diagnostic(diag: dict) -> str:
    """Format a DNS diagnostic for display"""
    lines = [
        f"# DNS Diagnostic: {diag.get('hostname', 'unknown')}",
        "",
        f"## Would Resolve: {'✅ Yes' if diag.get('wouldResolve') else '❌ No'}",
        f"## Reason: {diag.get('reason', 'Unknown')}",
    ]

    if diag.get('suggestedFix'):
        lines.append(f"## Suggested Fix: {diag.get('suggestedFix')}")

    if diag.get('matchedRule'):
        rule = diag['matchedRule']
        lines.extend([
            "",
            "## Matched Rule",
            f"- Suffix: {rule.get('suffix', 'N/A')}",
            f"- Profile: {rule.get('profileName', 'N/A')} ({rule.get('profileId', 'N/A')})",
            f"- DNS Server: {rule.get('dnsServer', 'N/A')}",
            f"- Strip Suffix: {rule.get('stripSuffix', True)}",
        ])
        if rule.get('hosts'):
            lines.append(f"- Static Hosts: {len(rule['hosts'])} entries")

    if diag.get('allRules'):
        lines.extend([
            "",
            "## All DNS Rules",
        ])
        for i, rule in enumerate(diag['allRules'], 1):
            lines.append(f"{i}. `{rule.get('suffix')}` → {rule.get('profileName', 'N/A')} (DNS: {rule.get('dnsServer', 'N/A')})")

    return "\n".join(lines)


def format_vpn_status(vpns: list) -> str:
    """Format VPN status list for display"""
    if not vpns:
        return "No VPN profiles configured."

    lines = ["# VPN Status", ""]

    for vpn in vpns:
        status_icon = "🟢" if vpn.get('connected') else "🔴"
        health_icon = "✅" if vpn.get('healthy') else "⚠️" if vpn.get('connected') else ""

        lines.append(f"## {status_icon} {vpn.get('profileName', 'Unknown')} {health_icon}")
        lines.append(f"- Profile ID: `{vpn.get('profileId', 'N/A')}`")
        lines.append(f"- Connected: {'Yes' if vpn.get('connected') else 'No'}")

        if vpn.get('connected'):
            lines.extend([
                f"- Healthy: {'Yes' if vpn.get('healthy') else 'No'}",
                f"- Endpoint: {vpn.get('endpoint', 'N/A')}",
                f"- Tunnel IP: {vpn.get('tunnelIP', 'N/A')}",
                f"- Bytes Sent: {vpn.get('bytesSent', 0):,}",
                f"- Bytes Received: {vpn.get('bytesRecv', 0):,}",
            ])
            if vpn.get('avgLatencyMs'):
                lines.append(f"- Avg Latency: {vpn.get('avgLatencyMs'):.1f}ms")

        lines.append("")

    return "\n".join(lines)


def format_host_mappings(mappings: list) -> str:
    """Format host mappings for display"""
    if not mappings:
        return "No active host mappings."

    lines = [
        "# Active Host Mappings",
        "",
        "| Hostname | Real IP | Loopback IP | Profile |",
        "|----------|---------|-------------|---------|",
    ]

    for m in mappings:
        lines.append(
            f"| {m.get('hostname', 'N/A')} | {m.get('realIP', 'N/A')} | "
            f"{m.get('loopbackIP', 'N/A')} | {m.get('profileName', 'N/A')} |"
        )

    return "\n".join(lines)


def format_logs(logs: list) -> str:
    """Format logs for display"""
    if not logs:
        return "No logs found."

    lines = ["# Logs (newest first)", ""]

    for log in logs:
        level = log.get('level', 'info').upper()
        level_icon = {
            'DEBUG': '🔍',
            'INFO': 'ℹ️',
            'WARN': '⚠️',
            'ERROR': '❌'
        }.get(level, 'ℹ️')

        timestamp = log.get('timestamp', '')[:19].replace('T', ' ')
        component = log.get('component', 'unknown')
        message = log.get('message', '')
        profile = log.get('profileId', '')

        profile_str = f" [{profile}]" if profile else ""
        lines.append(f"{level_icon} `{timestamp}` **{component}**{profile_str}: {message}")

        if log.get('fields'):
            for key, value in log['fields'].items():
                lines.append(f"   - {key}: {value}")

    return "\n".join(lines)


def format_dns_query_result(result: dict) -> str:
    """Format a DNS query result for display"""
    lines = [
        f"# DNS Query: {result.get('hostname', 'unknown')}",
        "",
        "## Query Info",
        f"- Type: {result.get('queryType', 'A')}",
        f"- DNS Server: {result.get('dnsServer', 'N/A')}",
        f"- Profile: {result.get('profileName', 'N/A')} (`{result.get('profileId', 'N/A')}`)",
        f"- Latency: {result.get('latencyMs', 0)}ms",
        "",
        "## Result",
    ]

    if result.get('error'):
        lines.append(f"❌ Error: {result.get('error')}")
        return "\n".join(lines)

    rcode = result.get('rcode', -1)
    rcode_name = result.get('rcodeName', 'UNKNOWN')
    success = result.get('success', False)

    if success:
        lines.append(f"✅ Success (rcode={rcode}, {rcode_name})")
    else:
        lines.append(f"❌ Failed (rcode={rcode}, {rcode_name})")

    records = result.get('records', [])
    if records:
        lines.extend(["", "## Records"])
        for rec in records:
            lines.append(f"- **{rec.get('type', '?')}** `{rec.get('value', '')}` (TTL: {rec.get('ttl', 0)}s)")
    else:
        lines.extend(["", "No records returned."])

    return "\n".join(lines)


def format_errors(errors: list) -> str:
    """Format errors for display"""
    if not errors:
        return "No errors recorded. 🎉"

    lines = ["# Recent Errors", ""]

    for err in errors:
        timestamp = err.get('timestamp', '')[:19].replace('T', ' ')
        component = err.get('component', 'unknown')
        operation = err.get('operation', 'unknown')
        error_msg = err.get('error', 'Unknown error')
        profile = err.get('profileId', '')

        profile_str = f" [{profile}]" if profile else ""

        lines.extend([
            f"## ❌ {timestamp} - {component}{profile_str}",
            f"- Operation: {operation}",
            f"- Error: {error_msg}",
        ])

        if err.get('context'):
            lines.append("- Context:")
            for key, value in err['context'].items():
                lines.append(f"  - {key}: {value}")

        lines.append("")

    return "\n".join(lines)


@server.list_tools()
async def list_tools() -> list[Tool]:
    """List available tools"""
    return [
        Tool(
            name="test_host",
            description="Test connectivity to a host through the VPN. Performs DNS resolution and TCP connectivity test. Returns detailed results including which DNS rule matched, real IP, loopback IP, and latency.",
            inputSchema={
                "type": "object",
                "properties": {
                    "hostname": {
                        "type": "string",
                        "description": "The hostname to test (e.g., 'db.svi', 'api.internal')"
                    },
                    "port": {
                        "type": "integer",
                        "description": "TCP port to test (default: 443)",
                        "default": 443
                    },
                    "profile_id": {
                        "type": "string",
                        "description": "Optional: specific VPN profile ID to use. If not specified, uses DNS rules to determine profile."
                    },
                    "use_system_dns": {
                        "type": "boolean",
                        "description": "If true (default), resolve via system DNS (same path as real apps like DBeaver). If false, resolve directly via VPN tunnel.",
                        "default": True
                    }
                },
                "required": ["hostname"]
            }
        ),
        Tool(
            name="diagnose_dns",
            description="Diagnose DNS resolution for a hostname. Shows which DNS rule would match, whether resolution would succeed, and provides suggestions for fixes.",
            inputSchema={
                "type": "object",
                "properties": {
                    "hostname": {
                        "type": "string",
                        "description": "The hostname to diagnose (e.g., 'db.svi')"
                    }
                },
                "required": ["hostname"]
            }
        ),
        Tool(
            name="get_vpn_status",
            description="Get detailed status of all VPN tunnels. Shows connection state, health, endpoint, bytes transferred, and latency for each profile.",
            inputSchema={
                "type": "object",
                "properties": {},
                "required": []
            }
        ),
        Tool(
            name="get_host_mappings",
            description="Get all active host mappings. Shows hostname to IP mappings with their assigned loopback IPs and which VPN profile they route through.",
            inputSchema={
                "type": "object",
                "properties": {},
                "required": []
            }
        ),
        Tool(
            name="get_logs",
            description="Get application logs. Can filter by level (debug/info/warn/error), component (dns/tunnel/proxy/app/frontend:*), or profile ID.",
            inputSchema={
                "type": "object",
                "properties": {
                    "level": {
                        "type": "string",
                        "description": "Filter by log level: debug, info, warn, error",
                        "enum": ["debug", "info", "warn", "error"]
                    },
                    "component": {
                        "type": "string",
                        "description": "Filter by component: dns, tunnel, proxy, app, api, frontend:*"
                    },
                    "profile_id": {
                        "type": "string",
                        "description": "Filter by VPN profile ID"
                    },
                    "limit": {
                        "type": "integer",
                        "description": "Maximum number of logs to return (default: 100)",
                        "default": 100
                    }
                },
                "required": []
            }
        ),
        Tool(
            name="get_frontend_logs",
            description="Get logs from the frontend (React UI) only. Useful for debugging UI issues.",
            inputSchema={
                "type": "object",
                "properties": {
                    "limit": {
                        "type": "integer",
                        "description": "Maximum number of logs to return (default: 100)",
                        "default": 100
                    }
                },
                "required": []
            }
        ),
        Tool(
            name="get_errors",
            description="Get recent errors from the application. Shows error details with context and timestamps.",
            inputSchema={
                "type": "object",
                "properties": {
                    "limit": {
                        "type": "integer",
                        "description": "Maximum number of errors to return (default: 50)",
                        "default": 50
                    }
                },
                "required": []
            }
        ),
        Tool(
            name="get_metrics",
            description="Get performance metrics including DNS query stats, proxy connection counts, and latency measurements.",
            inputSchema={
                "type": "object",
                "properties": {},
                "required": []
            }
        ),
        Tool(
            name="get_diagnostic_report",
            description="Generate a complete diagnostic report. Includes system info, VPN status, DNS config, host mappings, recent errors, and metrics.",
            inputSchema={
                "type": "object",
                "properties": {},
                "required": []
            }
        ),
        Tool(
            name="get_loopback_ips",
            description="Get all assigned loopback IPs. Shows which IPs are assigned to each profile and dynamically created hosts.",
            inputSchema={
                "type": "object",
                "properties": {},
                "required": []
            }
        ),
        Tool(
            name="restart_app",
            description="Kill all running VPN MultiTunnel instances and start a fresh one. Useful when testing code changes or when the app is in a bad state.",
            inputSchema={
                "type": "object",
                "properties": {
                    "wait_seconds": {
                        "type": "integer",
                        "description": "Seconds to wait after starting the app for it to initialize (default: 8)",
                        "default": 8
                    }
                },
                "required": []
            }
        ),
        Tool(
            name="update_service",
            description="Full build and deploy: runs 'wails build' (frontend+backend), builds the service, then stops service, copies both exes to Program Files, and starts service. Requires admin privileges for the copy/restart.",
            inputSchema={
                "type": "object",
                "properties": {
                    "build": {
                        "type": "boolean",
                        "description": "Build before copying (default: true). Set to false to skip build and just copy/restart.",
                        "default": True
                    },
                    "wait_seconds": {
                        "type": "integer",
                        "description": "Seconds to wait between operations (default: 2)",
                        "default": 2
                    }
                },
                "required": []
            }
        ),
        Tool(
            name="build_installer",
            description="Build a complete NSIS installer: compiles app (wails build), compiles service, and creates installer exe. Output: build/bin/VPNMultiTunnel-amd64-installer.exe",
            inputSchema={
                "type": "object",
                "properties": {},
                "required": []
            }
        ),
        Tool(
            name="vpn_connect",
            description="Connect a VPN profile by its profile ID. Use get_vpn_status first to see available profiles and their IDs.",
            inputSchema={
                "type": "object",
                "properties": {
                    "profile_id": {
                        "type": "string",
                        "description": "The profile ID to connect (e.g., 'svi-edgar-b5d4de49')"
                    }
                },
                "required": ["profile_id"]
            }
        ),
        Tool(
            name="vpn_disconnect",
            description="Disconnect a VPN profile by its profile ID.",
            inputSchema={
                "type": "object",
                "properties": {
                    "profile_id": {
                        "type": "string",
                        "description": "The profile ID to disconnect"
                    }
                },
                "required": ["profile_id"]
            }
        ),
        Tool(
            name="configure_dns",
            description="Toggle DNS proxy configuration. 'enable' configures system DNS to use the VPN DNS proxy (equivalent to clicking Configure in the UI). 'disable' restores the original DNS. 'status' shows current state.",
            inputSchema={
                "type": "object",
                "properties": {
                    "action": {
                        "type": "string",
                        "description": "Action to perform: 'enable' (configure DNS proxy), 'disable' (restore original DNS), or 'status' (check current state)",
                        "enum": ["enable", "disable", "status"]
                    }
                },
                "required": ["action"]
            }
        ),
        Tool(
            name="flush_dns",
            description="Flush the Windows DNS resolver cache. Useful after enabling/disabling DNS proxy or changing DNS configuration.",
            inputSchema={
                "type": "object",
                "properties": {},
                "required": []
            }
        ),
        Tool(
            name="check_port53",
            description="Check port 53 status: which processes hold it, whether the DNS proxy can bind, and test a DNS query to 127.0.0.53:53. Use this to diagnose SharedAccess/ICS conflicts with the DNS proxy.",
            inputSchema={
                "type": "object",
                "properties": {
                    "test_query": {
                        "type": "string",
                        "description": "Optional hostname to resolve via 127.0.0.53:53 as a test (default: 'google.com')",
                        "default": "google.com"
                    }
                },
                "required": []
            }
        ),
        Tool(
            name="dns_query",
            description="Query a DNS server through a specific VPN tunnel. Useful for debugging DNS resolution issues. Returns detailed DNS response including all records, rcode, and latency.",
            inputSchema={
                "type": "object",
                "properties": {
                    "hostname": {
                        "type": "string",
                        "description": "The hostname to query (e.g., 'db.internal', 'web-server')"
                    },
                    "profile_id": {
                        "type": "string",
                        "description": "The VPN profile ID to route the query through (e.g., 'svi-edgar', 'contabo-2')"
                    },
                    "query_type": {
                        "type": "string",
                        "description": "DNS record type: A, AAAA, CNAME, MX, TXT, NS, SOA, PTR, ANY (default: A)",
                        "enum": ["A", "AAAA", "CNAME", "MX", "TXT", "NS", "SOA", "PTR", "ANY"],
                        "default": "A"
                    },
                    "dns_server": {
                        "type": "string",
                        "description": "DNS server IP to query (optional, uses profile default if not specified)"
                    }
                },
                "required": ["hostname", "profile_id"]
            }
        ),
        Tool(
            name="create_release",
            description="Build the installer and create a GitHub Release on Edvantage26/vpn-multitunnel. Uploads the NSIS installer as a release asset. Optionally skips the build step if the installer is already built.",
            inputSchema={
                "type": "object",
                "properties": {
                    "version": {
                        "type": "string",
                        "description": "Version tag (e.g., 'v1.0.0'). Will be used as the git tag and release name."
                    },
                    "title": {
                        "type": "string",
                        "description": "Release title (default: 'VPN MultiTunnel <version>')"
                    },
                    "notes": {
                        "type": "string",
                        "description": "Release notes in markdown (default: auto-generated from version)"
                    },
                    "draft": {
                        "type": "boolean",
                        "description": "Create as draft release (default: false)",
                        "default": False
                    },
                    "build": {
                        "type": "boolean",
                        "description": "Build the installer before creating the release (default: true). Set to false if already built.",
                        "default": True
                    }
                },
                "required": ["version"]
            }
        ),
    ]


@server.call_tool()
async def call_tool(name: str, arguments: dict) -> list[TextContent]:
    """Handle tool calls"""

    try:
        # restart_app doesn't require the API to be running
        if name == "restart_app":
            wait_seconds = arguments.get("wait_seconds", 8)
            lines = ["# Restarting VPN MultiTunnel", ""]

            # Kill all existing instances
            lines.append("## Killing existing instances...")
            try:
                result = subprocess.run(
                    ["taskkill", "/F", "/IM", "VPNMultiTunnel.exe"],
                    capture_output=True,
                    text=True
                )
                if result.returncode == 0:
                    killed_count = result.stdout.count("SUCCESS")
                    lines.append(f"✅ Killed {killed_count} instance(s)")
                else:
                    lines.append("ℹ️ No instances were running")
            except Exception as e:
                lines.append(f"⚠️ Error killing processes: {e}")

            # Wait a moment for processes to fully terminate
            time.sleep(2)

            # Start new instance
            lines.append("")
            lines.append("## Starting new instance...")

            if not VPN_APP_PATH.exists():
                lines.append(f"❌ Executable not found: {VPN_APP_PATH}")
                return [TextContent(type="text", text="\n".join(lines))]

            try:
                subprocess.Popen(
                    [str(VPN_APP_PATH)],
                    cwd=str(VPN_APP_PATH.parent.parent.parent),
                    creationflags=subprocess.DETACHED_PROCESS | subprocess.CREATE_NEW_PROCESS_GROUP
                )
                lines.append(f"✅ Started VPNMultiTunnel.exe")
            except Exception as e:
                lines.append(f"❌ Error starting app: {e}")
                return [TextContent(type="text", text="\n".join(lines))]

            # Wait for initialization
            lines.append(f"⏳ Waiting {wait_seconds}s for initialization...")
            time.sleep(wait_seconds)

            # Check if API is responding
            if vpn_client.health_check():
                lines.append("✅ Debug API is responding")

                # Get quick status
                try:
                    status = vpn_client.get_status()
                    vpns = status.get("vpns", [])
                    connected = sum(1 for v in vpns if v.get("connected"))
                    lines.append(f"✅ {connected}/{len(vpns)} VPN profiles connected")
                except:
                    pass
            else:
                lines.append("⚠️ Debug API not responding yet (app may still be starting)")

            return [TextContent(type="text", text="\n".join(lines))]

        # update_service doesn't require the API to be running
        if name == "update_service":
            wait_seconds = arguments.get("wait_seconds", 2)
            should_build = arguments.get("build", True)
            lines = ["# Updating Windows Service", ""]

            # Build if requested
            if should_build:
                project_root = Path(__file__).parent.parent.parent

                # Step 0a: Build the main app with wails
                lines.append("## Step 0a: Building app (wails build)...")
                try:
                    result = subprocess.run(
                        ["wails", "build"],
                        cwd=str(project_root),
                        capture_output=True,
                        text=True,
                        timeout=300  # 5 minutes for full build
                    )
                    if result.returncode == 0:
                        lines.append("✅ App build successful")
                    else:
                        lines.append(f"❌ App build failed:")
                        error_output = result.stderr.strip() or result.stdout.strip()
                        # Truncate if too long
                        if len(error_output) > 500:
                            error_output = error_output[:500] + "..."
                        lines.append(f"```\n{error_output}\n```")
                        return [TextContent(type="text", text="\n".join(lines))]
                except subprocess.TimeoutExpired:
                    lines.append("❌ App build timed out (300s)")
                    return [TextContent(type="text", text="\n".join(lines))]
                except FileNotFoundError:
                    lines.append("❌ Wails CLI not found. Make sure wails is installed and in PATH.")
                    return [TextContent(type="text", text="\n".join(lines))]
                except Exception as e:
                    lines.append(f"❌ App build error: {e}")
                    return [TextContent(type="text", text="\n".join(lines))]
                lines.append("")

                # Step 0b: Build the service
                lines.append("## Step 0b: Building service...")
                try:
                    result = subprocess.run(
                        ["go", "build", "-o", "build/bin/VPNMultiTunnel-service.exe", "./cmd/service"],
                        cwd=str(project_root),
                        capture_output=True,
                        text=True,
                        timeout=120
                    )
                    if result.returncode == 0:
                        lines.append("✅ Service build successful")
                    else:
                        lines.append(f"❌ Service build failed:")
                        lines.append(f"```\n{result.stderr.strip()}\n```")
                        return [TextContent(type="text", text="\n".join(lines))]
                except subprocess.TimeoutExpired:
                    lines.append("❌ Service build timed out (120s)")
                    return [TextContent(type="text", text="\n".join(lines))]
                except FileNotFoundError:
                    lines.append("❌ Go compiler not found. Make sure Go is installed and in PATH.")
                    return [TextContent(type="text", text="\n".join(lines))]
                except Exception as e:
                    lines.append(f"❌ Service build error: {e}")
                    return [TextContent(type="text", text="\n".join(lines))]
                lines.append("")

            # Check that dev exe exists
            if not VPN_SERVICE_DEV_PATH.exists():
                lines.append(f"❌ Development executable not found: {VPN_SERVICE_DEV_PATH}")
                lines.append("")
                lines.append("Build the service first with:")
                lines.append("```")
                lines.append("go build -o build/bin/VPNMultiTunnel-service.exe ./cmd/service")
                lines.append("```")
                return [TextContent(type="text", text="\n".join(lines))]

            # Check that production directory exists
            if not VPN_SERVICE_PROD_PATH.parent.exists():
                lines.append(f"❌ Production directory not found: {VPN_SERVICE_PROD_PATH.parent}")
                lines.append("")
                lines.append("The VPN MultiTunnel needs to be installed first.")
                return [TextContent(type="text", text="\n".join(lines))]

            lines.append("## Elevating privileges (UAC prompt)...")
            lines.append("")

            try:
                # Run PowerShell elevated - this triggers UAC
                # We use -Wait to wait for completion and capture output via temp file
                import tempfile
                import os

                # Use a fixed temp location that's easy to find
                temp_dir = Path(os.environ.get('TEMP', 'C:/Temp'))
                script_path = temp_dir / "vpn_update_service.ps1"
                output_file = temp_dir / "vpn_update_service_output.txt"

                # PowerShell script that runs elevated: kills app, stops service, copies, starts service, starts app
                ps_elevated_script = f'''
$ErrorActionPreference = "Continue"
$serviceProdPath = "{VPN_SERVICE_PROD_PATH}"
$serviceDevPath = "{VPN_SERVICE_DEV_PATH}"
$appProdPath = "{VPN_APP_PROD_PATH}"
$appDevPath = "{VPN_APP_DEV_PATH}"
$outputFile = "{output_file}"

$output = @()

# Step 1: Kill the app first (releases app exe)
$output += "STEP1: Killing app..."
$killResult = taskkill /F /IM VPNMultiTunnel.exe 2>&1 | Out-String
if ($LASTEXITCODE -eq 0) {{
    $output += "SUCCESS: App killed"
}} else {{
    $output += "INFO: App was not running"
}}
Start-Sleep -Seconds 1

# Step 2: Stop the service (releases service exe)
$output += "STEP2: Stopping service..."
$stopResult = & $serviceProdPath stop 2>&1 | Out-String
$output += $stopResult

# Wait for service to fully release the exe file
Start-Sleep -Seconds 4

$output += "STEP3: Copying executables..."
$copySuccess = $true
try {{
    Copy-Item -Path $serviceDevPath -Destination $serviceProdPath -Force -ErrorAction Stop
    $output += "SUCCESS: Service exe copied"
}} catch {{
    $output += "ERROR: Service exe: $($_.Exception.Message)"
    $copySuccess = $false
}}
try {{
    Copy-Item -Path $appDevPath -Destination $appProdPath -Force -ErrorAction Stop
    $output += "SUCCESS: App exe copied"
}} catch {{
    $output += "ERROR: App exe: $($_.Exception.Message)"
    $copySuccess = $false
}}

$output += "STEP4: Starting service..."
$startResult = & $serviceProdPath start 2>&1 | Out-String
$output += $startResult
Start-Sleep -Seconds 1

$output += "STEP5: Checking status..."
$statusResult = & $serviceProdPath status 2>&1 | Out-String
$output += $statusResult

# Step 6: Start the app from production path
$output += "STEP6: Starting app..."
Start-Process -FilePath $appProdPath -WorkingDirectory (Split-Path $appProdPath)
$output += "SUCCESS: App started"

$output | Out-File -FilePath $outputFile -Encoding UTF8

# Explicit exit to ensure PowerShell terminates
exit 0
'''

                with open(script_path, 'w', encoding='utf-8') as f:
                    f.write(ps_elevated_script)

                # Delete old output file if exists
                try:
                    output_file.unlink(missing_ok=True)
                except:
                    pass

                # Run elevated WITHOUT -Wait, then poll for completion
                ps_cmd = f'Start-Process powershell -Verb RunAs -ArgumentList \'-ExecutionPolicy Bypass -File "{script_path}"\''

                subprocess.run(
                    ["powershell", "-ExecutionPolicy", "Bypass", "-Command", ps_cmd],
                    capture_output=True,
                    text=True,
                    timeout=10  # This returns quickly since we don't use -Wait
                )

                # Poll for the output file to appear (script writes it at the end)
                max_wait = 60  # seconds
                waited = 0
                while waited < max_wait:
                    time.sleep(2)
                    waited += 2
                    if output_file.exists():
                        # Check if script completed by looking for STEP6
                        try:
                            content = output_file.read_text(encoding='utf-8', errors='ignore')
                            if "STEP6:" in content:
                                break
                        except:
                            pass

                # Give it a moment to finish writing
                time.sleep(1)

                # Read the output file
                try:
                    with open(output_file, 'r', encoding='utf-8', errors='ignore') as f:
                        output = f.read()
                except FileNotFoundError:
                    output = "(No output captured - UAC may have been cancelled)"

                # Parse output and format nicely
                if "STEP1:" in output:
                    lines.append("## Step 1: Killing app...")
                    if "SUCCESS: App killed" in output:
                        lines.append("✅ App killed")
                    else:
                        lines.append("ℹ️ App was not running")

                if "STEP2:" in output:
                    lines.append("")
                    lines.append("## Step 2: Stopping service...")
                    lines.append("✅ Stop command executed")

                if "STEP3:" in output:
                    lines.append("")
                    lines.append("## Step 3: Copying executables...")
                    if "SUCCESS: Service exe copied" in output:
                        lines.append("✅ Service exe copied")
                    if "SUCCESS: App exe copied" in output:
                        lines.append("✅ App exe copied")
                    # Check for errors
                    error_lines = [l for l in output.split('\n') if 'ERROR:' in l]
                    for err in error_lines:
                        lines.append(f"❌ {err.strip()}")

                if "STEP4:" in output:
                    lines.append("")
                    lines.append("## Step 4: Starting service...")
                    lines.append("✅ Start command executed")

                if "STEP5:" in output:
                    lines.append("")
                    lines.append("## Step 5: Service status...")
                    # Extract status from STEP5 section
                    step5_content = output.split("STEP5:")[-1].split("STEP6:")[0] if "STEP6:" in output else output.split("STEP5:")[-1]
                    if "running" in step5_content.lower():
                        lines.append("✅ Service is running")
                    else:
                        lines.append("ℹ️ Checking status...")

                if "STEP6:" in output:
                    lines.append("")
                    lines.append("## Step 6: Starting app...")
                    if "SUCCESS: App started" in output:
                        lines.append("✅ App started")

                if "No output captured" in output or not output.strip():
                    lines.append("⚠️ UAC was cancelled or no output captured")

                # Cleanup temp files
                try:
                    script_path.unlink(missing_ok=True)
                    output_file.unlink(missing_ok=True)
                except:
                    pass

            except subprocess.TimeoutExpired:
                lines.append("❌ Timeout waiting for elevated process")
            except Exception as e:
                lines.append(f"❌ Error: {e}")

            # Wait for app to initialize and check API
            time.sleep(4)
            lines.append("")
            lines.append("## Verifying...")
            if vpn_client.health_check():
                lines.append("✅ Debug API responding")
            else:
                lines.append("⏳ App starting (Debug API not ready yet)")

            lines.append("")
            lines.append("## Summary")
            lines.append(f"- Service: `{VPN_SERVICE_PROD_PATH}`")
            lines.append(f"- App: `{VPN_APP_PROD_PATH}`")

            return [TextContent(type="text", text="\n".join(lines))]

        # build_installer doesn't require the API
        if name == "build_installer":
            lines = ["# Building Installer", ""]
            project_root = Path(__file__).parent.parent.parent
            nsis_path = Path("C:/Program Files (x86)/NSIS/makensis.exe")

            # Step 1: Build app with wails
            lines.append("## Step 1: Building app (wails build)...")
            try:
                result = subprocess.run(
                    ["wails", "build"],
                    cwd=str(project_root),
                    capture_output=True,
                    text=True,
                    timeout=300
                )
                if result.returncode == 0:
                    lines.append("✅ App build successful")
                else:
                    lines.append(f"❌ App build failed:")
                    error_output = result.stderr.strip() or result.stdout.strip()
                    if len(error_output) > 500:
                        error_output = error_output[:500] + "..."
                    lines.append(f"```\n{error_output}\n```")
                    return [TextContent(type="text", text="\n".join(lines))]
            except subprocess.TimeoutExpired:
                lines.append("❌ Build timed out (300s)")
                return [TextContent(type="text", text="\n".join(lines))]
            except FileNotFoundError:
                lines.append("❌ Wails CLI not found")
                return [TextContent(type="text", text="\n".join(lines))]
            except Exception as e:
                lines.append(f"❌ Error: {e}")
                return [TextContent(type="text", text="\n".join(lines))]
            lines.append("")

            # Step 2: Build service
            lines.append("## Step 2: Building service...")
            try:
                result = subprocess.run(
                    ["go", "build", "-o", "build/bin/VPNMultiTunnel-service.exe", "./cmd/service"],
                    cwd=str(project_root),
                    capture_output=True,
                    text=True,
                    timeout=120
                )
                if result.returncode == 0:
                    lines.append("✅ Service build successful")
                else:
                    lines.append(f"❌ Service build failed:")
                    lines.append(f"```\n{result.stderr.strip()}\n```")
                    return [TextContent(type="text", text="\n".join(lines))]
            except Exception as e:
                lines.append(f"❌ Error: {e}")
                return [TextContent(type="text", text="\n".join(lines))]
            lines.append("")

            # Step 3: Run NSIS
            lines.append("## Step 3: Creating installer (NSIS)...")
            if not nsis_path.exists():
                lines.append(f"❌ NSIS not found at {nsis_path}")
                lines.append("Install NSIS from https://nsis.sourceforge.io/Download")
                return [TextContent(type="text", text="\n".join(lines))]

            try:
                app_exe = project_root / "build" / "bin" / "VPNMultiTunnel.exe"
                nsi_script = project_root / "build" / "windows" / "installer" / "project.nsi"

                result = subprocess.run(
                    [str(nsis_path), f"/DARG_WAILS_AMD64_BINARY={app_exe}", str(nsi_script)],
                    capture_output=True,
                    text=True,
                    timeout=120
                )
                if result.returncode == 0:
                    lines.append("✅ Installer created successfully")
                else:
                    lines.append(f"❌ NSIS failed:")
                    lines.append(f"```\n{result.stderr.strip() or result.stdout.strip()}\n```")
                    return [TextContent(type="text", text="\n".join(lines))]
            except Exception as e:
                lines.append(f"❌ Error: {e}")
                return [TextContent(type="text", text="\n".join(lines))]
            lines.append("")

            # Summary
            installer_path = project_root / "build" / "bin" / "VPNMultiTunnel-amd64-installer.exe"
            lines.append("## Done!")
            lines.append(f"Installer: `{installer_path}`")

            if installer_path.exists():
                size_mb = installer_path.stat().st_size / (1024 * 1024)
                lines.append(f"Size: {size_mb:.1f} MB")

            return [TextContent(type="text", text="\n".join(lines))]

        # create_release doesn't require the API
        if name == "create_release":
            version_tag = arguments.get("version", "")
            release_title = arguments.get("title", f"VPN MultiTunnel {version_tag}")
            release_notes = arguments.get("notes", f"Release {version_tag}")
            is_draft = arguments.get("draft", False)
            should_build = arguments.get("build", True)

            lines = ["# Creating GitHub Release", ""]
            project_root = Path(__file__).parent.parent.parent
            installer_path = project_root / "build" / "bin" / "VPNMultiTunnel-amd64-installer.exe"
            github_repo = "Edvantage26/vpn-multitunnel"

            # Step 1: Build installer (optional)
            if should_build:
                lines.append("## Step 1: Building installer...")
                nsis_path = Path("C:/Program Files (x86)/NSIS/makensis.exe")

                # Build app
                try:
                    build_result = subprocess.run(
                        ["wails", "build"],
                        cwd=str(project_root),
                        capture_output=True,
                        text=True,
                        timeout=300
                    )
                    if build_result.returncode != 0:
                        error_output = build_result.stderr.strip() or build_result.stdout.strip()
                        if len(error_output) > 500:
                            error_output = error_output[:500] + "..."
                        lines.append(f"❌ App build failed:\n```\n{error_output}\n```")
                        return [TextContent(type="text", text="\n".join(lines))]
                    lines.append("- App build: OK")
                except Exception as build_error:
                    lines.append(f"❌ App build error: {build_error}")
                    return [TextContent(type="text", text="\n".join(lines))]

                # Build service
                try:
                    service_result = subprocess.run(
                        ["go", "build", "-o", "build/bin/VPNMultiTunnel-service.exe", "./cmd/service"],
                        cwd=str(project_root),
                        capture_output=True,
                        text=True,
                        timeout=120
                    )
                    if service_result.returncode != 0:
                        lines.append(f"❌ Service build failed:\n```\n{service_result.stderr.strip()}\n```")
                        return [TextContent(type="text", text="\n".join(lines))]
                    lines.append("- Service build: OK")
                except Exception as service_error:
                    lines.append(f"❌ Service build error: {service_error}")
                    return [TextContent(type="text", text="\n".join(lines))]

                # Run NSIS
                if not nsis_path.exists():
                    lines.append(f"❌ NSIS not found at {nsis_path}")
                    return [TextContent(type="text", text="\n".join(lines))]

                try:
                    app_exe = project_root / "build" / "bin" / "VPNMultiTunnel.exe"
                    nsi_script = project_root / "build" / "windows" / "installer" / "project.nsi"
                    nsis_result = subprocess.run(
                        [str(nsis_path), f"/DARG_WAILS_AMD64_BINARY={app_exe}", str(nsi_script)],
                        capture_output=True,
                        text=True,
                        timeout=120
                    )
                    if nsis_result.returncode != 0:
                        lines.append(f"❌ NSIS failed:\n```\n{nsis_result.stderr.strip() or nsis_result.stdout.strip()}\n```")
                        return [TextContent(type="text", text="\n".join(lines))]
                    lines.append("- Installer created: OK")
                except Exception as nsis_error:
                    lines.append(f"❌ NSIS error: {nsis_error}")
                    return [TextContent(type="text", text="\n".join(lines))]

                lines.append("")
            else:
                lines.append("## Step 1: Skipping build (build=false)")
                lines.append("")

            # Step 2: Verify installer exists
            lines.append("## Step 2: Verifying installer...")
            if not installer_path.exists():
                lines.append(f"❌ Installer not found at `{installer_path}`")
                lines.append("Run `build_installer` first or set build=true")
                return [TextContent(type="text", text="\n".join(lines))]

            installer_size_mb = installer_path.stat().st_size / (1024 * 1024)
            lines.append(f"- Installer: `{installer_path.name}` ({installer_size_mb:.1f} MB)")
            lines.append("")

            # Step 3: Check gh CLI
            lines.append("## Step 3: Creating GitHub Release...")
            try:
                gh_check = subprocess.run(["gh", "auth", "status"], capture_output=True, text=True, timeout=10)
                if gh_check.returncode != 0:
                    lines.append("❌ GitHub CLI not authenticated. Run `gh auth login` first.")
                    return [TextContent(type="text", text="\n".join(lines))]
            except FileNotFoundError:
                lines.append("❌ GitHub CLI (gh) not found. Install from https://cli.github.com/")
                return [TextContent(type="text", text="\n".join(lines))]

            # Step 4: Create release
            gh_command = [
                "gh", "release", "create", version_tag,
                "--repo", github_repo,
                "--title", release_title,
                "--notes", release_notes,
                str(installer_path)
            ]
            if is_draft:
                gh_command.insert(4, "--draft")

            try:
                release_result = subprocess.run(
                    gh_command,
                    capture_output=True,
                    text=True,
                    timeout=120,
                    cwd=str(project_root)
                )
                if release_result.returncode != 0:
                    error_msg = release_result.stderr.strip() or release_result.stdout.strip()
                    lines.append(f"❌ Release creation failed:\n```\n{error_msg}\n```")
                    return [TextContent(type="text", text="\n".join(lines))]

                release_url = release_result.stdout.strip()
                lines.append(f"Release created successfully!")
                lines.append("")
                lines.append("## Summary")
                lines.append(f"- Version: **{version_tag}**")
                lines.append(f"- Title: {release_title}")
                lines.append(f"- Draft: {'Yes' if is_draft else 'No'}")
                lines.append(f"- Asset: `{installer_path.name}` ({installer_size_mb:.1f} MB)")
                lines.append(f"- URL: {release_url}")

            except subprocess.TimeoutExpired:
                lines.append("❌ Release creation timed out (120s)")
            except Exception as release_error:
                lines.append(f"❌ Error: {release_error}")

            return [TextContent(type="text", text="\n".join(lines))]

        # Check if API is available for other tools
        if not vpn_client.health_check():
            return [TextContent(
                type="text",
                text="❌ VPN MultiTunnel Debug API is not available.\n\n"
                     "Make sure:\n"
                     "1. VPN MultiTunnel is running\n"
                     "2. Debug API is enabled in settings (debugApiEnabled: true)\n"
                     "3. The API is listening on port 8765\n\n"
                     "💡 Tip: Use the `restart_app` tool to start the application."
            )]

        if name == "test_host":
            hostname = arguments.get("hostname", "")
            port = arguments.get("port", 443)
            profile_id = arguments.get("profile_id", "")
            use_system_dns = arguments.get("use_system_dns", True)

            result = vpn_client.test_host(hostname, port, profile_id, use_system_dns)
            return [TextContent(type="text", text=format_host_test_result(result))]

        elif name == "diagnose_dns":
            hostname = arguments.get("hostname", "")
            result = vpn_client.diagnose_dns(hostname)
            return [TextContent(type="text", text=format_dns_diagnostic(result))]

        elif name == "get_vpn_status":
            status = vpn_client.get_status()
            vpns = status.get("vpns", [])
            return [TextContent(type="text", text=format_vpn_status(vpns))]

        elif name == "get_host_mappings":
            mappings = vpn_client.get_host_mappings()
            return [TextContent(type="text", text=format_host_mappings(mappings))]

        elif name == "get_logs":
            level = arguments.get("level", "")
            component = arguments.get("component", "")
            profile_id = arguments.get("profile_id", "")
            limit = arguments.get("limit", 100)

            logs = vpn_client.get_logs(level, component, profile_id, limit)
            return [TextContent(type="text", text=format_logs(logs))]

        elif name == "get_frontend_logs":
            limit = arguments.get("limit", 100)
            logs = vpn_client.get_frontend_logs(limit)
            return [TextContent(type="text", text=format_logs(logs))]

        elif name == "get_errors":
            limit = arguments.get("limit", 50)
            errors = vpn_client.get_errors(limit)
            return [TextContent(type="text", text=format_errors(errors))]

        elif name == "get_metrics":
            metrics = vpn_client.get_metrics()
            return [TextContent(type="text", text=f"# Performance Metrics\n\n```json\n{format_json(metrics)}\n```")]

        elif name == "get_diagnostic_report":
            report = vpn_client.get_diagnostic_report()
            return [TextContent(type="text", text=f"# Diagnostic Report\n\n```json\n{format_json(report)}\n```")]

        elif name == "get_loopback_ips":
            status = vpn_client.get_status()
            tcp_proxy = status.get("tcpProxy", {})
            tunnel_ips = tcp_proxy.get("tunnelIPs", {})

            lines = ["# Loopback IPs", ""]

            if tunnel_ips:
                lines.append("## Profile IPs")
                for profile_id, ip in tunnel_ips.items():
                    lines.append(f"- `{profile_id}`: {ip}")

            mappings = vpn_client.get_host_mappings()
            if mappings:
                lines.extend(["", "## Dynamic Host IPs"])
                for m in mappings:
                    lines.append(f"- `{m.get('hostname')}`: {m.get('loopbackIP')} → {m.get('realIP')}")

            if not tunnel_ips and not mappings:
                lines.append("No loopback IPs assigned yet.")

            return [TextContent(type="text", text="\n".join(lines))]

        elif name == "vpn_connect":
            profile_id = arguments.get("profile_id", "")
            lines = [f"# VPN Connect: `{profile_id}`", ""]
            try:
                result = vpn_client.vpn_connect(profile_id)
                if result.get("success"):
                    lines.append(f"✅ Connected `{profile_id}`")
                    # Wait a moment and get status
                    time.sleep(2)
                    try:
                        status = vpn_client.get_status()
                        for vpn in status.get("vpns", []):
                            if vpn.get("profileId") == profile_id:
                                lines.append(f"- Healthy: {'✅' if vpn.get('healthy') else '⏳ checking...'}")
                                lines.append(f"- Endpoint: {vpn.get('endpoint', 'N/A')}")
                                lines.append(f"- Tunnel IP: {vpn.get('tunnelIP', 'N/A')}")
                                break
                    except:
                        pass
                else:
                    lines.append(f"❌ Failed: {result.get('error', 'Unknown error')}")
            except Exception as e:
                lines.append(f"❌ Error: {e}")
            return [TextContent(type="text", text="\n".join(lines))]

        elif name == "vpn_disconnect":
            profile_id = arguments.get("profile_id", "")
            lines = [f"# VPN Disconnect: `{profile_id}`", ""]
            try:
                result = vpn_client.vpn_disconnect(profile_id)
                if result.get("success"):
                    lines.append(f"✅ Disconnected `{profile_id}`")
                else:
                    lines.append(f"❌ Failed: {result.get('error', 'Unknown error')}")
            except Exception as e:
                lines.append(f"❌ Error: {e}")
            return [TextContent(type="text", text="\n".join(lines))]

        elif name == "configure_dns":
            action = arguments.get("action", "status")
            lines = ["# DNS Proxy Configuration", ""]

            if action == "enable":
                lines.append("## Enabling DNS proxy...")
                try:
                    result = vpn_client.configure_dns()
                    if result.get("success"):
                        lines.append(f"✅ DNS configured to {result.get('dnsAddress', '?')}")
                        lines.append(f"- Port 53 free: {'✅' if result.get('port53Free') else '❌'}")
                        lines.append(f"- DNS Client stopped: {'✅' if result.get('dnsClientDown') else '❌'}")
                        # Flush DNS cache
                        subprocess.run(["ipconfig", "/flushdns"], capture_output=True, timeout=5)
                        lines.append("- DNS cache flushed: ✅")
                    else:
                        lines.append(f"❌ Failed: {result.get('error', 'Unknown error')}")
                except Exception as e:
                    lines.append(f"❌ Error: {e}")

            elif action == "disable":
                lines.append("## Restoring original DNS...")
                try:
                    result = vpn_client.restore_dns()
                    if result.get("success"):
                        lines.append("✅ DNS restored to original configuration")
                        # Flush DNS cache
                        subprocess.run(["ipconfig", "/flushdns"], capture_output=True, timeout=5)
                        lines.append("- DNS cache flushed: ✅")
                    else:
                        lines.append(f"❌ Failed: {result.get('error', 'Unknown error')}")
                except Exception as e:
                    lines.append(f"❌ Error: {e}")

            elif action == "status":
                lines.append("## Current DNS Status")
                try:
                    status = vpn_client.get_status()
                    dns_cfg = status.get("dns", {})
                    system = status.get("system", {})

                    is_configured = system.get("dnsConfigured", False)
                    lines.append(f"- System DNS → Proxy: {'✅ Active' if is_configured else '❌ Not active'}")
                    lines.append(f"- DNS Proxy enabled: {'✅' if dns_cfg.get('enabled') else '❌'}")
                    lines.append(f"- Listen port: {dns_cfg.get('listenPort', '?')}")
                    lines.append(f"- Rules: {len(dns_cfg.get('rules', []))}")
                except Exception as e:
                    lines.append(f"❌ Error: {e}")

            return [TextContent(type="text", text="\n".join(lines))]

        elif name == "flush_dns":
            lines = ["# Flush DNS Cache", ""]
            try:
                result = subprocess.run(
                    ["ipconfig", "/flushdns"],
                    capture_output=True, text=True, timeout=5
                )
                if result.returncode == 0:
                    lines.append("✅ Windows DNS resolver cache flushed")
                else:
                    lines.append(f"❌ Failed: {result.stderr.strip()}")
            except Exception as e:
                lines.append(f"❌ Error: {e}")
            return [TextContent(type="text", text="\n".join(lines))]

        elif name == "check_port53":
            test_query = arguments.get("test_query", "google.com")
            lines = ["# Port 53 Diagnostic", ""]

            # 1. Check what processes hold port 53
            lines.append("## Processes on port 53")
            try:
                result = subprocess.run(
                    ["netstat", "-ano"],
                    capture_output=True, text=True, timeout=10
                )
                port53_lines = []
                for line in result.stdout.splitlines():
                    if ":53 " in line and ("UDP" in line or "TCP" in line):
                        port53_lines.append(line.strip())

                if port53_lines:
                    # Get process names for PIDs
                    pids_seen = set()
                    for p53line in port53_lines:
                        fields = p53line.split()
                        pid = fields[-1] if fields else "?"
                        proto = fields[0] if fields else "?"
                        local_addr = fields[1] if len(fields) > 1 else "?"

                        proc_name = "unknown"
                        if pid not in pids_seen:
                            try:
                                tasklist = subprocess.run(
                                    ["tasklist", "/FI", f"PID eq {pid}", "/FO", "CSV", "/NH"],
                                    capture_output=True, text=True, timeout=5
                                )
                                csv_line = tasklist.stdout.strip()
                                if csv_line and csv_line.startswith('"'):
                                    proc_name = csv_line.split('"')[1]
                            except:
                                pass
                            pids_seen.add(pid)

                        lines.append(f"- `{proto}` `{local_addr}` → PID {pid} (`{proc_name}`)")
                else:
                    lines.append("- ✅ No process is using port 53")
            except Exception as e:
                lines.append(f"- ❌ Error checking: {e}")

            # 2. Check SharedAccess and Dnscache service status
            lines.extend(["", "## Service Status"])
            for svc_name, svc_label in [("SharedAccess", "SharedAccess (ICS)"), ("Dnscache", "DNS Client")]:
                try:
                    result = subprocess.run(
                        ["sc", "query", svc_name],
                        capture_output=True, text=True, timeout=5
                    )
                    if "RUNNING" in result.stdout:
                        lines.append(f"- `{svc_label}`: 🟢 Running")
                    elif "STOPPED" in result.stdout:
                        lines.append(f"- `{svc_label}`: 🔴 Stopped")
                    elif "DISABLED" in result.stdout or "1060" in result.stderr:
                        lines.append(f"- `{svc_label}`: ⚫ Disabled")
                    else:
                        lines.append(f"- `{svc_label}`: ⚪ Unknown")
                except Exception as e:
                    lines.append(f"- `{svc_label}`: ❌ Error: {e}")

            # 3. Test bind to 127.0.0.53:53
            lines.extend(["", "## Bind Test (127.0.0.53:53)"])
            try:
                sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                sock.bind(("127.0.0.53", 53))
                sock.close()
                lines.append("- ✅ SO_REUSEADDR bind to 127.0.0.53:53 succeeded")
            except OSError as e:
                lines.append(f"- ❌ Bind failed: {e}")

            # 4. Test DNS query to 127.0.0.53:53
            lines.extend(["", f"## DNS Query Test ({test_query} → 127.0.0.53:53)"])
            try:
                # Build a minimal DNS query (A record)
                import struct
                import os
                txn_id = os.urandom(2)
                # Header: ID, flags=0x0100 (recursion desired), qdcount=1
                header = txn_id + struct.pack(">HHHHH", 0x0100, 1, 0, 0, 0)
                # Question: encode hostname
                qname = b""
                for label in test_query.split("."):
                    qname += bytes([len(label)]) + label.encode()
                qname += b"\x00"
                question = qname + struct.pack(">HH", 1, 1)  # Type A, Class IN

                sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                sock.settimeout(3)
                sock.sendto(header + question, ("127.0.0.53", 53))
                data, addr = sock.recvfrom(512)
                sock.close()

                # Parse response
                resp_flags = struct.unpack(">H", data[2:4])[0]
                rcode = resp_flags & 0x0F
                ancount = struct.unpack(">H", data[6:8])[0]

                if rcode == 0 and ancount > 0:
                    # Try to extract first A record IP
                    # Skip header (12 bytes) + question section
                    offset = 12
                    # Skip QNAME
                    while offset < len(data) and data[offset] != 0:
                        if data[offset] & 0xC0 == 0xC0:
                            offset += 2
                            break
                        offset += data[offset] + 1
                    else:
                        offset += 1
                    offset += 4  # QTYPE + QCLASS

                    # Parse first answer
                    ips = []
                    for _ in range(min(ancount, 5)):
                        if offset >= len(data):
                            break
                        # Skip NAME (may be pointer)
                        if offset < len(data) and data[offset] & 0xC0 == 0xC0:
                            offset += 2
                        else:
                            while offset < len(data) and data[offset] != 0:
                                offset += data[offset] + 1
                            offset += 1
                        if offset + 10 > len(data):
                            break
                        rtype, rclass, ttl, rdlength = struct.unpack(">HHIH", data[offset:offset+10])
                        offset += 10
                        if rtype == 1 and rdlength == 4:  # A record
                            ip = ".".join(str(b) for b in data[offset:offset+4])
                            ips.append(ip)
                        offset += rdlength

                    lines.append(f"- ✅ Response: {ancount} answers, rcode={rcode}")
                    if ips:
                        lines.append(f"- IPs: {', '.join(ips)}")
                elif rcode == 0:
                    lines.append(f"- ⚠️ Response OK but no answers (rcode={rcode}, answers={ancount})")
                else:
                    rcode_names = {1: "FORMERR", 2: "SERVFAIL", 3: "NXDOMAIN", 5: "REFUSED"}
                    lines.append(f"- ❌ Response error: rcode={rcode} ({rcode_names.get(rcode, 'UNKNOWN')})")
            except socket.timeout:
                lines.append("- ❌ Timeout (no response in 3s — DNS proxy may not be running on port 53)")
            except ConnectionResetError:
                lines.append("- ❌ Connection reset (port 53 is not accepting queries)")
            except OSError as e:
                lines.append(f"- ❌ Error: {e}")

            # 5. Get DNS proxy status from the app API
            lines.extend(["", "## App DNS Proxy Status"])
            try:
                if vpn_client.health_check():
                    status = vpn_client.get_status()
                    dns_cfg = status.get("dns", {})
                    system = status.get("system", {})
                    lines.append(f"- Enabled: {'✅' if dns_cfg.get('enabled') else '❌'}")
                    lines.append(f"- Listen port: {dns_cfg.get('listenPort', '?')}")
                    lines.append(f"- Rules: {len(dns_cfg.get('rules', []))}")
                    lines.append(f"- System DNS → Proxy: {'✅' if system.get('dnsConfigured') else '❌'}")
                else:
                    lines.append("- ⚠️ App API not available")
            except Exception as e:
                lines.append(f"- ❌ Error: {e}")

            return [TextContent(type="text", text="\n".join(lines))]

        elif name == "dns_query":
            hostname = arguments.get("hostname", "")
            profile_id = arguments.get("profile_id", "")
            query_type = arguments.get("query_type", "A")
            dns_server = arguments.get("dns_server", "")

            result = vpn_client.query_dns(hostname, profile_id, query_type, dns_server)
            return [TextContent(type="text", text=format_dns_query_result(result))]

        else:
            return [TextContent(type="text", text=f"Unknown tool: {name}")]

    except Exception as e:
        return [TextContent(type="text", text=f"❌ Error: {str(e)}")]


async def main():
    """Main entry point"""
    async with stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream, server.create_initialization_options())


if __name__ == "__main__":
    import asyncio
    asyncio.run(main())
