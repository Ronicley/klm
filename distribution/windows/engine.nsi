!ifndef VERSION
!define VERSION "0.2.1"
!endif
Unicode true
ManifestDPIAware true
RequestExecutionLevel user
SetCompressor /SOLID lzma
!include MUI2.nsh
!include LogicLib.nsh
!include x64.nsh

Name "KLM Engine"
OutFile "..\..\dist\KLM-Engine-${VERSION}-x64-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\KLM Engine"
InstallDirRegKey HKCU "Software\KLM\Engine" "InstallDir"
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "KLM Engine"
VIAddVersionKey "FileDescription" "KLM Engine Installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "KLM"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "KLM Engine requires 64-bit Windows." /SD IDOK
    Abort
  ${EndIf}
FunctionEnd

Section "KLM Engine"
  SetShellVarContext current
  ; Ask before stopping work or replacing installed files. Silent setup declines.
  MessageBox MB_YESNO|MB_ICONEXCLAMATION|MB_DEFBUTTON2 "Setup will stop any running KLM engine to install this version.$\r$\n$\r$\nAll work currently running in KLM will be interrupted. In-progress work may be lost.$\r$\n$\r$\nDo you want to stop the engine and continue?" /SD IDNO IDYES stop_engine_confirmed
  SetErrorLevel 1
  Abort
  stop_engine_confirmed:
  ; The new CLI can stop an installed worker before replacing its executable.
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "..\..\dist\engine\klm.exe"
  DetailPrint "Stopping the existing engine..."
  nsExec::ExecToStack /TIMEOUT=150000 '"$PLUGINSDIR\klm.exe" stop'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_OK|MB_ICONSTOP "Cannot stop the existing engine. Stop it before installing.$\r$\n$1" /SD IDOK
    Abort
  ${EndIf}

  SetOutPath "$INSTDIR"
  File "..\..\dist\engine\klm.exe"
  File "environment.ps1"
  File "start-engine.vbs"
  SetOutPath "$INSTDIR\prompts"
  File "..\..\dist\engine\prompts\*.md"
  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "Software\KLM\Engine" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine" "DisplayName" "KLM Engine"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine" "Publisher" "KLM"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine" "UninstallString" '$\"$INSTDIR\uninstall.exe$\"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine" "QuietUninstallString" '$\"$INSTDIR\uninstall.exe$\" /S'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine" "InstallLocation" "$INSTDIR"
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine" "NoRepair" 1

  DetailPrint "Configuring user PATH and login startup..."
  nsExec::ExecToStack /TIMEOUT=60000 '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$INSTDIR\environment.ps1" -Action Install'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_OK|MB_ICONSTOP "Cannot configure the engine's user PATH and login startup.$\r$\n$1" /SD IDOK
    Abort
  ${EndIf}
  DetailPrint "Starting the engine..."
  nsExec::ExecToStack /TIMEOUT=150000 '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$INSTDIR\environment.ps1" -Action Start'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_OK|MB_ICONEXCLAMATION "KLM Engine is installed but could not start.$\r$\n$1$\r$\nCheck %APPDATA%\klm\engine\engine.log, then run klm start." /SD IDOK
    SetErrorLevel 2
  ${Else}
    DetailPrint "Engine ready at http://localhost:7331."
  ${EndIf}
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  DetailPrint "Stopping the engine..."
  nsExec::ExecToStack /TIMEOUT=150000 '"$INSTDIR\klm.exe" stop'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_OK|MB_ICONSTOP "Cannot stop the engine. Retry uninstall after it stops.$\r$\n$1" /SD IDOK
    Abort
  ${EndIf}
  DetailPrint "Removing user PATH and login startup..."
  nsExec::ExecToStack /TIMEOUT=60000 '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$INSTDIR\environment.ps1" -Action Uninstall'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_OK|MB_ICONSTOP "Cannot remove PATH/login registration.$\r$\n$1" /SD IDOK
    Abort
  ${EndIf}
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\KLMEngine"
  DeleteRegKey HKCU "Software\KLM\Engine"
  Delete "$INSTDIR\klm.exe"
  Delete "$INSTDIR\environment.ps1"
  Delete "$INSTDIR\start-engine.vbs"
  Delete "$INSTDIR\uninstall.exe"
  RMDir /r "$INSTDIR\prompts"
  RMDir "$INSTDIR"
  ; Engine data under APPDATA and harness credentials are deliberately preserved.
SectionEnd
