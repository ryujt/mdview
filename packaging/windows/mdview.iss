#ifndef AppVersion
  #error AppVersion is required
#endif
#ifndef PayloadDir
  #error PayloadDir is required
#endif
#ifndef ReleaseDir
  #error ReleaseDir is required
#endif

[Setup]
AppId={{1EF7F0CF-B28A-4EEB-9949-1CF8046C169B}
AppName=mdview
AppVersion={#AppVersion}
AppPublisher=tools.camp
AppPublisherURL=https://tools.camp
DefaultDirName={localappdata}\Programs\mdview
DefaultGroupName=mdview
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
UninstallDisplayIcon={app}\mdview.exe
OutputDir={#ReleaseDir}
OutputBaseFilename=mdview-{#AppVersion}-windows-x64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ChangesEnvironment=yes
CloseApplications=yes
RestartApplications=no
DisableProgramGroupPage=yes
InfoAfterFile={#PayloadDir}\INSTALL.txt

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "korean"; MessagesFile: "compiler:Languages\Korean.isl"

[Tasks]
Name: "addtopath"; Description: "Add mdview to the current user's PATH (open a new terminal after installation)"
Name: "desktopicon"; Description: "Create a desktop shortcut"; Flags: unchecked

[Files]
Source: "{#PayloadDir}\mdview.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\INSTALL.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\config.example.json"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#PayloadDir}\THIRD-PARTY-NOTICES.txt"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\mdview"; Filename: "{app}\mdview.exe"; WorkingDir: "{userdocs}"
Name: "{group}\Installation guide"; Filename: "{app}\INSTALL.txt"
Name: "{autodesktop}\mdview"; Filename: "{app}\mdview.exe"; WorkingDir: "{userdocs}"; Tasks: desktopicon

[Code]
const
  StateKey = 'Software\tools.camp\mdview';

function SplitPath(Value: String): TArrayOfString;
var
  Separator, Count: Integer;
begin
  SetArrayLength(Result, 0);
  repeat
    Separator := Pos(';', Value);
    Count := GetArrayLength(Result);
    SetArrayLength(Result, Count + 1);
    if Separator = 0 then
      Result[Count] := Value
    else
    begin
      Result[Count] := Copy(Value, 1, Separator - 1);
      Delete(Value, 1, Separator);
    end;
  until Separator = 0;
end;

function HasPathEntry(Value, Entry: String): Boolean;
var
  Parts: TArrayOfString;
  I: Integer;
begin
  Result := False;
  Parts := SplitPath(Value);
  for I := 0 to GetArrayLength(Parts) - 1 do
    if CompareText(RemoveBackslash(RemoveQuotes(Trim(Parts[I]))), RemoveBackslash(Entry)) = 0 then
    begin
      Result := True;
      Exit;
    end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Value, Entry: String;
begin
  if (CurStep = ssPostInstall) and WizardIsTaskSelected('addtopath') then
  begin
    Entry := ExpandConstant('{app}');
    RegQueryStringValue(HKCU, 'Environment', 'Path', Value);
    if not HasPathEntry(Value, Entry) then
    begin
      // Always add our own separator so uninstall preserves a trailing one.
      if Value <> '' then
        Value := Value + ';';
      if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', Value + Entry) then
        RaiseException('Unable to update the user PATH.');
      // Retain ownership across upgrades; never remove a pre-existing entry.
      RegWriteStringValue(HKCU, StateKey, 'PathEntry', Entry);
    end;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Value, Entry, Updated: String;
  Parts: TArrayOfString;
  I: Integer;
  HasKeptEntry: Boolean;
begin
  if (CurUninstallStep = usUninstall) and
     RegQueryStringValue(HKCU, StateKey, 'PathEntry', Entry) and
     (CompareText(Entry, ExpandConstant('{app}')) = 0) then
  begin
    if RegQueryStringValue(HKCU, 'Environment', 'Path', Value) then
    begin
      Updated := '';
      HasKeptEntry := False;
      Parts := SplitPath(Value);
      for I := 0 to GetArrayLength(Parts) - 1 do
        if CompareText(RemoveBackslash(RemoveQuotes(Trim(Parts[I]))), RemoveBackslash(Entry)) <> 0 then
        begin
          if HasKeptEntry then
            Updated := Updated + ';';
          Updated := Updated + Parts[I];
          HasKeptEntry := True;
        end;
      if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', Updated) then
        RaiseException('Unable to remove mdview from the user PATH.');
    end;
    RegDeleteValue(HKCU, StateKey, 'PathEntry');
    RegDeleteKeyIfEmpty(HKCU, StateKey);
  end;
end;
