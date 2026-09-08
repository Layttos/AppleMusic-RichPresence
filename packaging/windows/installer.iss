; Inno Setup script for applemusic-rp.
;
; It installs into the user's own profile and asks for no elevation: the daemon
; writes nothing outside that profile, and an unprivileged install is one fewer
; consent prompt for something that only ever talks to Discord over a local
; pipe.
;
; Build it through build-windows.ps1, which compiles the executable first and
; passes the version in. Compiling this file on its own needs the two defines
; below to be supplied with /D.

#define AppName "Apple Music Rich Presence"
#define AppShortName "AppleMusicRP"
#define AppExeName "applemusic-rp.exe"
#define AppPublisher "Layttos"
#define AppURL "https://github.com/Layttos/AppleMusic-RichPresence"

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
; The version shown to people can be a tag such as "1.2.0-rc1"; the one in the
; file header has to be four plain numbers, so it is passed in separately.
#ifndef NumericVersion
  #define NumericVersion "0.0.0"
#endif
; BuildDir holds the compiled executable; RepoDir the sources.
#ifndef BuildDir
  #define BuildDir "..\..\dist"
#endif
#ifndef RepoDir
  #define RepoDir "..\.."
#endif

[Setup]
; Never change AppId: it is what lets an upgrade recognise an earlier install
; rather than leaving two entries behind.
AppId={{C5C3DF2C-8009-4281-8B6E-5C05BA5495E6}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases
VersionInfoVersion={#NumericVersion}
VersionInfoProductName={#AppName}

DefaultDirName={autopf}\{#AppShortName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
DisableWelcomePage=no
LicenseFile={#RepoDir}\LICENSE

; lowest keeps the whole install inside the user's profile, so no elevation
; prompt appears and {autopf} resolves to %LOCALAPPDATA%\Programs.
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

OutputDir={#BuildDir}
OutputBaseFilename={#AppShortName}-Setup-{#AppVersion}
SetupIconFile={#RepoDir}\internal\ui\icon.ico
UninstallDisplayIcon={app}\{#AppExeName}
UninstallDisplayName={#AppName}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern

; The running daemon is stopped from [Code] instead: it keeps no window that
; the restart manager could ask to close.
CloseApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "french"; MessagesFile: "compiler:Languages\French.isl"

[Tasks]
Name: "startup"; Description: "{cm:StartAtSignIn}"; GroupDescription: "{cm:Options}"
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:Options}"; Flags: unchecked

[Files]
Source: "{#BuildDir}\{#AppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#RepoDir}\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion
Source: "{#RepoDir}\NOTICE"; DestDir: "{app}"; DestName: "NOTICE.txt"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExeName}"
; The uninstaller Inno writes into {app} is reachable from Installed apps, but
; a Start menu entry beside the application saves looking for it.
Name: "{group}\{cm:UninstallProgram,{#AppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Registry]
; Per-user autostart. uninsdeletevalue takes it away again on uninstall.
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; \
    ValueType: string; ValueName: "{#AppShortName}"; ValueData: """{app}\{#AppExeName}"""; \
    Flags: uninsdeletevalue; Tasks: startup

[Run]
Filename: "{app}\{#AppExeName}"; Description: "{cm:LaunchProgram,{#AppName}}"; \
    Flags: nowait postinstall skipifsilent

[CustomMessages]
english.Options=Options:
french.Options=Options :
english.StartAtSignIn=Start it when I sign in
french.StartAtSignIn=Lancer au démarrage de la session
english.StoppingApp=Closing the running copy…
french.StoppingApp=Fermeture de la copie en cours d'exécution…

[Code]
{ The daemon has no window of its own, so it is asked to close and then, if it
  is still running, ended. Discord drops the presence as soon as the pipe
  closes, so neither path leaves a stale activity behind. }
procedure StopRunningApp();
var
  ResultCode: Integer;
begin
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/IM {#AppExeName}', '',
       SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Sleep(700);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM {#AppExeName}', '',
       SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Sleep(300);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  WizardForm.StatusLabel.Caption := ExpandConstant('{cm:StoppingApp}');
  StopRunningApp();
  Result := '';
end;

function InitializeUninstall(): Boolean;
begin
  StopRunningApp();
  Result := True;
end;
