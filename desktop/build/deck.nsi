; The Windows installer of deck: the launcher and the Linux build of deck for WSL, for this user, without admin
; rights; the launcher readies WSL itself (deck.exe -setup), Windows asking for consent when WSL is missing.
Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"

Name "deck"
OutFile "deck-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\deck"
RequestExecutionLevel user
!define MUI_ICON "deck.ico"
!define MUI_UNICON "deck.ico"
!define UNINSTALL "Software\Microsoft\Windows\CurrentVersion\Uninstall\deck"

!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Russian"

LangString Restart ${LANG_ENGLISH} "WSL is installed. Restart Windows, then open deck from the Start menu."
LangString Restart ${LANG_RUSSIAN} "WSL установлен. Перезагрузите Windows и откройте deck из меню «Пуск»."
LangString Failed ${LANG_ENGLISH} "deck could not get WSL ready; the log above says why. Run the installer again to retry."
LangString Failed ${LANG_RUSSIAN} "deck не смог подготовить WSL; почему — в журнале выше. Запустите установщик снова."

Section
  nsExec::Exec 'taskkill /IM deck.exe /F'
  Pop $0
  SetOutPath "$INSTDIR"
  File "deck.exe"
  File "deck-linux"
  File "deck.ico"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\deck.lnk" "$INSTDIR\deck.exe" "" "$INSTDIR\deck.ico"
  WriteRegStr HKCU "${UNINSTALL}" "DisplayName" "deck"
  WriteRegStr HKCU "${UNINSTALL}" "DisplayIcon" "$INSTDIR\deck.ico"
  WriteRegStr HKCU "${UNINSTALL}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTALL}" "NoModify" 1
  nsExec::ExecToLog '"$INSTDIR\deck.exe" -setup'
  Pop $0
  ${If} $0 == 3
    MessageBox MB_OK|MB_ICONINFORMATION "$(Restart)"
  ${ElseIf} $0 != 0
    MessageBox MB_OK|MB_ICONEXCLAMATION "$(Failed)"
    Abort
  ${Else}
    Exec '"$INSTDIR\deck.exe"'
  ${EndIf}
SectionEnd

; The distribution deck with the owner's data stays; wsl --unregister deck removes it.
Section "Uninstall"
  nsExec::Exec 'taskkill /IM deck.exe /F'
  Pop $0
  nsExec::Exec 'wsl.exe -d deck --exec pkill -TERM -x deck'
  Pop $0
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "deck"
  DeleteRegKey HKCU "${UNINSTALL}"
  Delete "$SMPROGRAMS\deck.lnk"
  Delete "$INSTDIR\deck.exe"
  Delete "$INSTDIR\deck-linux"
  Delete "$INSTDIR\deck.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
