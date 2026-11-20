Unicode true

####
## NeuroRift NSIS installer (#28).
##
## Invoked by the Release workflow (and locally, see docs/BUILDING.md):
##
##   cd build/windows/nsis
##   makensis -DARG_WAILS_AMD64_BINARY=..\..\..\build\bin\neurorift.exe `
##            -DNR_MIHOMO_BINARY=..\..\..\core_stage\mihomo-windows-amd64.exe `
##            -DINFO_PRODUCTVERSION=1.2.3 project.nsi
##
## Everything below has a working default so a bare `makensis project.nsi`
## (with only ARG_WAILS_AMD64_BINARY provided) still produces an installer;
## optional payloads (core, docs, WebView2 bootstrapper) are compile-time
## detected with !if /FileExists and skipped gracefully when absent.
##
## Defines (all overridable via -DNAME=VALUE):
##   INFO_PRODUCTVERSION  x.y.z release version (also used for VIProductVersion)
##   NR_OUTPUT            output path of the setup exe
##   NR_MIHOMO_BINARY     mihomo.exe bundled as core\mihomo.exe (offline first run)
##   WAILS_SKIP_WEBVIEW2  set automatically when the bootstrapper is missing
####

!ifndef INFO_PROJECTNAME
    !define INFO_PROJECTNAME "neurorift"
!endif
!ifndef INFO_COMPANYNAME
    !define INFO_COMPANYNAME "NeuroRift Project"
!endif
!ifndef INFO_PRODUCTNAME
    !define INFO_PRODUCTNAME "NeuroRift"
!endif
!ifndef INFO_PRODUCTVERSION
    !define INFO_PRODUCTVERSION "0.0.0"
!endif
!ifndef INFO_COPYRIGHT
    !define INFO_COPYRIGHT "(c) 2026 NeuroRift Project contributors"
!endif
!ifndef PRODUCT_EXECUTABLE
    !define PRODUCT_EXECUTABLE "neurorift.exe"
!endif
!ifndef UNINST_KEY_NAME
    !define UNINST_KEY_NAME "NeuroRift ProjectNeuroRift"
!endif
!ifndef NR_OUTPUT
    !define NR_OUTPUT "..\..\..\build\bin\NeuroRift-${INFO_PRODUCTVERSION}-windows-x64-setup.exe"
!endif

# The WebView2 Evergreen bootstrapper must sit next to this script. Local
# builds without it simply skip the runtime check (the app re-checks at
# startup and offers a guided install anyway - startup_windows.go).
!if /FileExists "MicrosoftEdgeWebview2Setup.exe"
!else
    !define WAILS_SKIP_WEBVIEW2
!endif

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

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "Run ${INFO_PRODUCTNAME}"

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "${NR_OUTPUT}"
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show # This will always show the installation details.

Function .onInit
    !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    # Self-update flow (#13): the app quits itself and launches
    # "NeuroRift-<ver>-windows-x64-setup.exe /S /update". Give the app a
    # moment to release file locks before we overwrite them.
    IfSilent silent_grace no_grace
    silent_grace:
        Sleep 2500
    no_grace:

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files

    # Bundled mihomo core: makes the first run work fully offline (parity
    # with the portable zip). Optional at compile time.
    !ifdef NR_MIHOMO_BINARY
        !if /FileExists "${NR_MIHOMO_BINARY}"
            CreateDirectory "$INSTDIR\core"
            File /oname=core\mihomo.exe "${NR_MIHOMO_BINARY}"
        !else
            !error "NR_MIHOMO_BINARY is defined but '${NR_MIHOMO_BINARY}' does not exist"
        !endif
    !endif

    # Documentation (optional at compile time, always present in CI builds).
    !if /FileExists "..\..\..\README.md"
        File /oname=README.md "..\..\..\README.md"
    !endif
    !if /FileExists "..\..\..\LICENSE"
        File /oname=LICENSE "..\..\..\LICENSE"
    !endif
    !if /FileExists "..\..\..\TROUBLESHOOTING.md"
        File /oname=TROUBLESHOOTING.md "..\..\..\TROUBLESHOOTING.md"
    !endif
    !if /FileExists "..\..\..\docs\CHANGELOG.md"
        File /oname=CHANGELOG.md "..\..\..\docs\CHANGELOG.md"
    !endif

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller

    # Silent self-update: relaunch the app after the files are replaced.
    # (Interactive installs get the MUI finish page with its Run checkbox.)
    IfSilent check_update_opt nolaunch
    check_update_opt:
    ClearErrors
    ${GetOptions} $CMDLINE "/update" $R0
    IfErrors nolaunch
        Exec '"$INSTDIR\${PRODUCT_EXECUTABLE}"'
    nolaunch:
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    # Best effort: stop a running instance so $INSTDIR is not locked.
    # (Graceful WM_CLOSE would just hide the app to the tray - close-to-tray.)
    DetailPrint "Stopping ${INFO_PRODUCTNAME}..."
    nsExec::ExecToLog 'taskkill /F /IM "${PRODUCT_EXECUTABLE}"'

    RMDir /r "$INSTDIR"

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller

    # User data (profiles, subscriptions, settings) lives in
    # %APPDATA%\NeuroRift and is KEPT by default. Interactive uninstalls may
    # opt into deleting it; silent uninstalls always keep it.
    IfSilent keepdata askdata
    askdata:
        MessageBox MB_YESNO|MB_ICONQUESTION "Also delete your settings and profiles?$\r$\n$\r$\n($APPDATA\${INFO_PRODUCTNAME} - subscriptions, tested nodes, preferences)" IDNO keepdata
        RMDir /r "$APPDATA\${INFO_PRODUCTNAME}"
    keepdata:
SectionEnd
