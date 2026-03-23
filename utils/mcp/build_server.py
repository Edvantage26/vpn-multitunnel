#!/usr/bin/env python3
"""
MCP Server for VPN MultiTunnel Build & Release

Provides tools for building the installer and creating releases.
Separated from vpn-debug to avoid dependency on the running VPN app.

Usage:
    python build_server.py

Configure in .mcp.json:
    {
        "mcpServers": {
            "vpn-build": {
                "command": "python",
                "args": ["utils/mcp/build_server.py"]
            }
        }
    }
"""

import json
import os
import re
import subprocess
from pathlib import Path

from mcp.server import Server
from mcp.server.stdio import stdio_server
from mcp.types import Tool, TextContent

# Ensure Go binaries (wails, go) are in PATH for subprocess calls
_go_bin_path = Path.home() / "go" / "bin"
if str(_go_bin_path) not in os.environ.get("PATH", ""):
    os.environ["PATH"] = str(_go_bin_path) + os.pathsep + os.environ.get("PATH", "")

PROJECT_ROOT = Path(__file__).parent.parent.parent

server = Server("vmt-build")


@server.list_tools()
async def list_tools() -> list[Tool]:
    return [
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
            name="create_release",
            description="Bump version, commit, tag, and push to trigger a GitHub Actions release build. The workflow builds the installer and creates the GitHub Release automatically.",
            inputSchema={
                "type": "object",
                "properties": {
                    "version": {
                        "type": "string",
                        "description": "Explicit version tag (e.g., 'v1.1.0'). Mutually exclusive with 'bump'."
                    },
                    "bump": {
                        "type": "string",
                        "enum": ["major", "minor", "patch"],
                        "description": "Auto-bump version from current. 'patch': 1.0.1→1.0.2, 'minor': 1.0.1→1.1.0, 'major': 1.0.1→2.0.0. Mutually exclusive with 'version'."
                    }
                },
                "required": []
            }
        ),
    ]


@server.call_tool()
async def call_tool(name: str, arguments: dict) -> list[TextContent]:
    """Handle tool calls"""

    try:
        if name == "build_installer":
            return _handle_build_installer()

        if name == "create_release":
            return _handle_create_release(arguments)

        return [TextContent(type="text", text=f"❌ Unknown tool: {name}")]

    except Exception as unexpected_error:
        return [TextContent(type="text", text=f"❌ Error: {str(unexpected_error)}")]


def _handle_build_installer() -> list[TextContent]:
    lines = ["# Building Installer", ""]
    nsis_path = Path("C:/Program Files (x86)/NSIS/makensis.exe")

    # Step 1: Build app with wails
    lines.append("## Step 1: Building app (wails build)...")
    try:
        result = subprocess.run(
            ["wails", "build"],
            cwd=str(PROJECT_ROOT),
            capture_output=True,
            text=True,
            timeout=300
        )
        if result.returncode == 0:
            lines.append("✅ App build successful")
        else:
            error_output = result.stderr.strip() or result.stdout.strip()
            if len(error_output) > 500:
                error_output = error_output[:500] + "..."
            lines.append(f"❌ App build failed:\n```\n{error_output}\n```")
            return [TextContent(type="text", text="\n".join(lines))]
    except subprocess.TimeoutExpired:
        lines.append("❌ Build timed out (300s)")
        return [TextContent(type="text", text="\n".join(lines))]
    except FileNotFoundError:
        lines.append("❌ Wails CLI not found")
        return [TextContent(type="text", text="\n".join(lines))]
    except Exception as build_error:
        lines.append(f"❌ Error: {build_error}")
        return [TextContent(type="text", text="\n".join(lines))]
    lines.append("")

    # Step 2: Build service
    lines.append("## Step 2: Building service...")
    try:
        result = subprocess.run(
            ["go", "build", "-o", "build/bin/VPNMultiTunnel-service.exe", "./cmd/service"],
            cwd=str(PROJECT_ROOT),
            capture_output=True,
            text=True,
            timeout=120
        )
        if result.returncode == 0:
            lines.append("✅ Service build successful")
        else:
            lines.append(f"❌ Service build failed:\n```\n{result.stderr.strip()}\n```")
            return [TextContent(type="text", text="\n".join(lines))]
    except Exception as service_error:
        lines.append(f"❌ Error: {service_error}")
        return [TextContent(type="text", text="\n".join(lines))]
    lines.append("")

    # Step 3: Run NSIS
    lines.append("## Step 3: Creating installer (NSIS)...")
    if not nsis_path.exists():
        lines.append(f"❌ NSIS not found at {nsis_path}")
        lines.append("Install NSIS from https://nsis.sourceforge.io/Download")
        return [TextContent(type="text", text="\n".join(lines))]

    try:
        app_exe = PROJECT_ROOT / "build" / "bin" / "VPNMultiTunnel.exe"
        nsi_script = PROJECT_ROOT / "build" / "windows" / "installer" / "project.nsi"

        result = subprocess.run(
            [str(nsis_path), f"/DARG_WAILS_AMD64_BINARY={app_exe}", str(nsi_script)],
            capture_output=True,
            text=True,
            timeout=120
        )
        if result.returncode == 0:
            lines.append("✅ Installer created successfully")
        else:
            lines.append(f"❌ NSIS failed:\n```\n{result.stderr.strip() or result.stdout.strip()}\n```")
            return [TextContent(type="text", text="\n".join(lines))]
    except Exception as nsis_error:
        lines.append(f"❌ Error: {nsis_error}")
        return [TextContent(type="text", text="\n".join(lines))]
    lines.append("")

    # Summary
    installer_path = PROJECT_ROOT / "build" / "bin" / "VPNMultiTunnel-amd64-installer.exe"
    lines.append("## Done!")
    lines.append(f"Installer: `{installer_path}`")

    if installer_path.exists():
        size_mb = installer_path.stat().st_size / (1024 * 1024)
        lines.append(f"Size: {size_mb:.1f} MB")

    return [TextContent(type="text", text="\n".join(lines))]


def _handle_create_release(arguments: dict) -> list[TextContent]:
    version_tag = arguments.get("version", "")
    bump_type = arguments.get("bump", "")

    lines = ["# Creating Release Tag", ""]

    # Resolve version from bump or explicit version
    if bump_type and version_tag:
        lines.append("❌ Cannot specify both 'version' and 'bump'. Use one or the other.")
        return [TextContent(type="text", text="\n".join(lines))]

    if bump_type:
        # Read current version from version.go
        version_go_path = PROJECT_ROOT / "internal" / "app" / "version.go"
        try:
            version_go_content = version_go_path.read_text(encoding="utf-8")
            current_match = re.search(r'var AppVersion = "(\d+\.\d+\.\d+)"', version_go_content)
            if not current_match:
                lines.append("❌ Could not read current version from version.go")
                return [TextContent(type="text", text="\n".join(lines))]
            current_parts = current_match.group(1).split(".")
            major_num = int(current_parts[0])
            minor_num = int(current_parts[1])
            patch_num = int(current_parts[2])

            if bump_type == "major":
                major_num += 1
                minor_num = 0
                patch_num = 0
            elif bump_type == "minor":
                minor_num += 1
                patch_num = 0
            elif bump_type == "patch":
                patch_num += 1

            semver = f"{major_num}.{minor_num}.{patch_num}"
            version_tag = f"v{semver}"
            lines.append(f"## Auto-bump: {bump_type} ({current_match.group(1)} → {semver})")
            lines.append("")
        except Exception as read_error:
            lines.append(f"❌ Failed to read current version: {read_error}")
            return [TextContent(type="text", text="\n".join(lines))]
    else:
        semver = version_tag.lstrip("v")

    if not semver:
        lines.append("❌ Version is required. Use 'version' (e.g., v1.1.0) or 'bump' (patch/minor/major).")
        return [TextContent(type="text", text="\n".join(lines))]

    # Step 1: Update version in all source files
    lines.append(f"## Step 1: Updating version to {semver}...")

    version_files_updated = []
    try:
        # Update internal/app/version.go
        version_go_path = PROJECT_ROOT / "internal" / "app" / "version.go"
        if version_go_path.exists():
            version_go_content = version_go_path.read_text(encoding="utf-8")
            version_go_new = re.sub(
                r'var AppVersion = ".*?"',
                f'var AppVersion = "{semver}"',
                version_go_content
            )
            version_go_path.write_text(version_go_new, encoding="utf-8")
            version_files_updated.append("internal/app/version.go")

        # Update build/info.json
        build_info_path = PROJECT_ROOT / "build" / "info.json"
        if build_info_path.exists():
            build_info = json.loads(build_info_path.read_text(encoding="utf-8"))
            build_info["fixed"]["file_version"] = f"{semver}.0"
            build_info["info"]["0000"]["ProductVersion"] = semver
            build_info_path.write_text(json.dumps(build_info, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
            version_files_updated.append("build/info.json")

        # Update wails.json
        wails_json_path = PROJECT_ROOT / "wails.json"
        if wails_json_path.exists():
            wails_config = json.loads(wails_json_path.read_text(encoding="utf-8"))
            wails_config["info"]["productVersion"] = semver
            wails_json_path.write_text(json.dumps(wails_config, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
            version_files_updated.append("wails.json")

        lines.append(f"- Updated: {', '.join(version_files_updated)}")

    except Exception as version_error:
        lines.append(f"❌ Version update failed: {version_error}")
        return [TextContent(type="text", text="\n".join(lines))]

    lines.append("")

    # Step 2: Git commit, tag, and push
    lines.append("## Step 2: Committing and tagging...")

    try:
        subprocess.run(
            ["git", "add"] + version_files_updated,
            cwd=str(PROJECT_ROOT), capture_output=True, text=True, timeout=10
        )
        commit_result = subprocess.run(
            ["git", "commit", "-m", f"chore: bump version to {semver}"],
            cwd=str(PROJECT_ROOT), capture_output=True, text=True, timeout=10
        )
        if commit_result.returncode == 0:
            lines.append("- Git commit: OK")
        else:
            lines.append(f"- Git commit: skipped ({commit_result.stdout.strip() or 'no changes'})")

        # Create annotated tag
        tag_result = subprocess.run(
            ["git", "tag", "-a", version_tag, "-m", f"Release {version_tag}"],
            cwd=str(PROJECT_ROOT), capture_output=True, text=True, timeout=10
        )
        if tag_result.returncode == 0:
            lines.append(f"- Git tag {version_tag}: OK")
        else:
            lines.append(f"❌ Git tag failed: {tag_result.stderr.strip()}")
            return [TextContent(type="text", text="\n".join(lines))]

        # Push commit and tag
        push_result = subprocess.run(
            ["git", "push"],
            cwd=str(PROJECT_ROOT), capture_output=True, text=True, timeout=30
        )
        if push_result.returncode == 0:
            lines.append("- Git push: OK")
        else:
            lines.append(f"⚠️ Git push failed: {push_result.stderr.strip()}")

        push_tag_result = subprocess.run(
            ["git", "push", "origin", version_tag],
            cwd=str(PROJECT_ROOT), capture_output=True, text=True, timeout=30
        )
        if push_tag_result.returncode == 0:
            lines.append(f"- Git push tag {version_tag}: OK")
        else:
            lines.append(f"❌ Tag push failed: {push_tag_result.stderr.strip()}")
            return [TextContent(type="text", text="\n".join(lines))]

    except Exception as git_error:
        lines.append(f"❌ Git operation failed: {git_error}")
        return [TextContent(type="text", text="\n".join(lines))]

    lines.append("")
    lines.append("## Summary")
    lines.append(f"- Version: **{version_tag}**")
    lines.append(f"- GitHub Actions will now build the installer and create the release")
    lines.append(f"- Monitor: https://github.com/Edvantage26/vpn-multitunnel/actions")

    return [TextContent(type="text", text="\n".join(lines))]


async def main():
    """Main entry point"""
    async with stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream, server.create_initialization_options())


if __name__ == "__main__":
    import asyncio
    asyncio.run(main())
