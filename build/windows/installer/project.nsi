Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them with the values from ProjectInfo.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
##
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the ProjectInfo file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "MyProject" # Default "{{.Name}}"
## !define INFO_COMPANYNAME    "MyCompany" # Default "{{.Info.CompanyName}}"
## !define INFO_PRODUCTNAME    "MyProduct" # Default "{{.Info.ProductName}}"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "{{.Info.ProductVersion}}"
## !define INFO_COPYRIGHT      "Copyright" # Default "{{.Info.Copyright}}"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

# Run app after install
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "Launch VPN MultiTunnel"
!define MUI_FINISHPAGE_RUN_CHECKED

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uinstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
InstallDir "$PROGRAMFILES64\Edvantage\VPN MultiTunnel" # Default installing folder
ShowInstDetails show # This will always show the installation details.

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    # Kill running app instances first (before copying files)
    DetailPrint "Closing running application..."
    nsExec::ExecToLog 'taskkill /F /IM VPNMultiTunnel.exe'
    nsExec::ExecToLog 'taskkill /F /IM vpnmulticlient.exe'
    Sleep 2000

    # Stop existing service if running (before copying files)
    DetailPrint "Stopping existing service if running..."
    nsExec::ExecToLog 'sc stop VPNMultiTunnelService'
    nsExec::ExecToLog 'sc stop VPNMultiClientService'
    Sleep 2000

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files

    # Install the service executable
    File "..\..\bin\VPNMultiTunnel-service.exe"

    # Uninstall old services (ignore errors)
    DetailPrint "Removing old service installations..."
    nsExec::ExecToLog '"$INSTDIR\VPNMultiTunnel-service.exe" uninstall'
    nsExec::ExecToLog 'sc delete VPNMultiClientService'
    Sleep 1000

    # Install the service
    DetailPrint "Installing VPN MultiTunnel Service..."
    nsExec::ExecToLog '"$INSTDIR\VPNMultiTunnel-service.exe" install'
    Pop $0
    ${If} $0 != 0
        DetailPrint "Warning: Service installation returned $0"
    ${EndIf}

    # Start the service
    DetailPrint "Starting VPN MultiTunnel Service..."
    nsExec::ExecToLog '"$INSTDIR\VPNMultiTunnel-service.exe" start'
    Pop $0
    ${If} $0 != 0
        DetailPrint "Warning: Service start returned $0"
    ${EndIf}

    # Pre-create loopback IPs:
    # - 127.0.0.53 for DNS proxy (avoids conflict with Windows DNS Client on 127.0.0.1)
    # - 127.0.1.1 through 127.0.10.1 for VPN tunnel transparent proxies
    DetailPrint "Configuring loopback IP addresses..."
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.0.53 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.1.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.2.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.3.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.4.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.5.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.6.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.7.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.8.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.9.1 255.255.255.0'
    nsExec::ExecToLog 'netsh interface ipv4 add address "Loopback Pseudo-Interface 1" 127.0.10.1 255.255.255.0'

    # Create configs directory and config.json with write permissions for users
    DetailPrint "Creating configuration files..."
    CreateDirectory "$INSTDIR\configs"

    # Create default config.json only if it doesn't exist (preserve user config on upgrades)
    IfFileExists "$INSTDIR\config.json" skip_config_creation
        FileOpen $0 "$INSTDIR\config.json" w
        FileWrite $0 '{"version":1,"settings":{"logLevel":"info","autoConnect":[],"portRangeStart":10800,"minimizeToTray":true,"startMinimized":false,"autoConfigureLoopback":true,"autoConfigureDNS":true,"usePort53":true,"useService":true,"debugApiEnabled":true,"debugApiPort":8765,"logBufferSize":10000,"errorBufferSize":1000,"metricsEnabled":true},"profiles":[],"dnsProxy":{"enabled":false,"listenPort":10053,"rules":[],"fallback":"system"},"tcpProxy":{"enabled":false,"tunnelIPs":{},"ports":[80,443,8080,8443,3000,4000,5000,5432,3306,6379,27017,1433,11211,9200]}}'
        FileClose $0
        nsExec::ExecToLog 'icacls "$INSTDIR\config.json" /grant Users:F'
    skip_config_creation:

    # Grant write permissions to BUILTIN\Users for configs folder
    DetailPrint "Setting permissions..."
    nsExec::ExecToLog 'icacls "$INSTDIR\configs" /grant Users:(OI)(CI)F'

    # Configure autostart at login via Scheduled Task (more reliable than registry Run key on Windows 11)
    DetailPrint "Configuring autostart via Scheduled Task..."
    # Remove legacy registry Run keys if present
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${INFO_PRODUCTNAME}"
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "VPNMultiTunnel"
    # Create scheduled task with 10s delay after logon (PowerShell handles current user automatically)
    # Remove old task name if present (renamed from "VPNMultiTunnel Autostart" to "VPN MultiTunnel")
    nsExec::ExecToLog 'schtasks /Delete /TN "VPNMultiTunnel Autostart" /F'
    nsExec::ExecToLog 'powershell -ExecutionPolicy Bypass -Command "$$action = New-ScheduledTaskAction -Execute \"$INSTDIR\${PRODUCT_EXECUTABLE}\"; $$trigger = New-ScheduledTaskTrigger -AtLogOn -User $$env:USERNAME; $$trigger.Delay = \"PT10S\"; $$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable; Register-ScheduledTask -TaskName \"VPN MultiTunnel\" -Action $$action -Trigger $$trigger -Settings $$settings -Force"'

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    # FIRST: Kill all application instances (removes tray icons)
    DetailPrint "Closing VPN MultiTunnel application..."
    nsExec::ExecToLog 'taskkill /F /IM VPNMultiTunnel.exe'
    nsExec::ExecToLog 'taskkill /F /IM vpnmulticlient.exe'

    # Wait for processes to fully terminate
    Sleep 2000

    # Stop the service
    DetailPrint "Stopping VPN MultiTunnel Service..."
    nsExec::ExecToLog '"$INSTDIR\VPNMultiTunnel-service.exe" stop'

    # Wait for service to stop
    Sleep 2000

    # Fallback: Force stop via sc
    nsExec::ExecToLog 'sc stop VPNMultiTunnelService'
    nsExec::ExecToLog 'sc stop VPNMultiClientService'
    Sleep 1000

    # Uninstall the service
    DetailPrint "Uninstalling VPN MultiTunnel Service..."
    nsExec::ExecToLog '"$INSTDIR\VPNMultiTunnel-service.exe" uninstall'

    # Fallback: Force delete via sc
    nsExec::ExecToLog 'sc delete VPNMultiTunnelService'
    nsExec::ExecToLog 'sc delete VPNMultiClientService'
    Sleep 1000

    # Remove loopback IPs (ignore errors - they may not exist)
    DetailPrint "Removing loopback IP addresses..."
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.0.53'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.1.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.2.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.3.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.4.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.5.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.6.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.7.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.8.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.9.1'
    nsExec::ExecToLog 'netsh interface ipv4 delete address "Loopback Pseudo-Interface 1" 127.0.10.1'

    # Remove autostart configuration (Scheduled Task + legacy registry keys)
    DetailPrint "Removing autostart configuration..."
    nsExec::ExecToLog 'schtasks /Delete /TN "VPN MultiTunnel" /F'
    nsExec::ExecToLog 'schtasks /Delete /TN "VPNMultiTunnel Autostart" /F'
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${INFO_PRODUCTNAME}"
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "VPNMultiTunnel"
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "VPN MultiClient"

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    # Remove installation directory
    RMDir /r $INSTDIR

    # Clean up legacy vpnmulticlient installation
    DetailPrint "Cleaning up legacy installations..."
    RMDir /r "$PROGRAMFILES64\vpnmulticlient"

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
    Delete "$SMPROGRAMS\VPN MultiClient.lnk"
    Delete "$DESKTOP\VPN MultiClient.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
