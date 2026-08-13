#define MyAppName "Pictogrep"
#define MyAppVersion GetEnv("PICTOGREP_VERSION")
#define MyAppPublisher "Tiago Hierath"
#define MyAppURL "https://navylily.tv/pictogrep"

[Setup]
AppId={{75F49D9A-81CD-47CB-B329-2355B197C8B3}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName={localappdata}\Programs\Pictogrep
DefaultGroupName=Pictogrep
DisableProgramGroupPage=yes
OutputDir=..\..\dist
OutputBaseFilename=pictogrep-windows-x86_64-setup
Compression=lzma2
SolidCompression=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern
SetupIconFile=..\..\assets\pictogrep.ico
UninstallDisplayIcon={app}\pictogrep.ico

[Files]
Source: "..\..\dist\pictogrep.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\gallery-dl.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\assets\pictogrep.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\Pictogrep"; Filename: "{app}\pictogrep.exe"; IconFilename: "{app}\pictogrep.ico"
Name: "{userdesktop}\Pictogrep"; Filename: "{app}\pictogrep.exe"; IconFilename: "{app}\pictogrep.ico"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional shortcuts:"; Flags: unchecked

[Run]
Filename: "{app}\pictogrep.exe"; Description: "Launch Pictogrep"; Flags: nowait postinstall skipifsilent
