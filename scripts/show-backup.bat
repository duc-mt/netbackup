@echo off
setlocal EnableDelayedExpansion
rem ==============================================================================
rem Script Name:   show-backup.bat
rem Description:   Quickly view or locate the latest configuration backup on Windows.
rem Usage:         show-backup.bat [options] <node-name>
rem ==============================================================================

set "SCRIPT_DIR=%~dp0"
set "ROOT_DIR=%SCRIPT_DIR%..\"
set "BACKUP_DIR=%ROOT_DIR%backups"

set "MODE=config"
set "PRINT_PATH_ONLY=0"
set "LIST_DEVICES=0"
set "NODE_QUERY="

:parse_args
if "%~1"=="" goto :validate_args
if /i "%~1"=="-h" goto :show_usage
if /i "%~1"=="--help" goto :show_usage
if /i "%~1"=="-l" (
    set "LIST_DEVICES=1"
    shift
    goto :parse_args
)
if /i "%~1"=="--list" (
    set "LIST_DEVICES=1"
    shift
    goto :parse_args
)
if /i "%~1"=="-d" (
    set "MODE=diff"
    shift
    goto :parse_args
)
if /i "%~1"=="--diff" (
    set "MODE=diff"
    shift
    goto :parse_args
)
if /i "%~1"=="-p" (
    set "PRINT_PATH_ONLY=1"
    shift
    goto :parse_args
)
if /i "%~1"=="--path" (
    set "PRINT_PATH_ONLY=1"
    shift
    goto :parse_args
)
if "%~1:~0,1%"=="-" (
    echo Unknown option: %~1
    goto :show_usage
)
if "%NODE_QUERY%"=="" (
    set "NODE_QUERY=%~1"
) else (
    echo Unexpected argument: %~1
    goto :show_usage
)
shift
goto :parse_args

:show_usage
echo Usage:
echo   show-backup.bat [OPTIONS] ^<node-name^>
echo   show-backup.bat -l ^| --list
echo.
echo Options:
echo   -d, --diff     Show latest diff instead of full configuration
echo   -p, --path     Print only file path without opening a pager
echo   -l, --list     List all devices with existing backups
echo   -h, --help     Show this help message
echo.
echo Examples:
echo   show-backup.bat NHG-HO-GW-01
echo   show-backup.bat GW-01           (prompts selection if multiple match)
echo   show-backup.bat -d NHG-HO-GW-01
echo   show-backup.bat --path NHG-HO-GW-01
echo   show-backup.bat -l
exit /b 0

:validate_args
if not exist "%BACKUP_DIR%" (
    echo Error: Backups directory '%BACKUP_DIR%' not found.
    exit /b 1
)

if %LIST_DEVICES% equ 1 (
    echo Available nodes in backup history:
    powershell -NoProfile -Command ^
        "Get-ChildItem -Path '%BACKUP_DIR%' -Recurse -Filter '*.config' | Select-Object -ExpandProperty BaseName -Unique | Sort-Object | ForEach-Object { '  - ' + $_ }"
    exit /b 0
)

if "%NODE_QUERY%"=="" (
    echo Error: Node name or substring is required.
    echo.
    goto :show_usage
)

rem Use PowerShell to locate matches, handle selection, and output the latest file path
set "TARGET_FILE="
for /f "delims=" %%F in ('powershell -NoProfile -Command ^
    "$backupDir = '%BACKUP_DIR%';" ^
    "$query = '%NODE_QUERY%';" ^
    "$mode = '%MODE%';" ^
    "$ext = '.' + $mode;" ^
    "$files = Get-ChildItem -Path $backupDir -Recurse -Filter ('*' + $ext) -File;" ^
    "if (-not $files) { Write-Error ('No ' + $mode + ' files found in backups directory.'); exit 1 };" ^
    "$matchedNodes = $files | Select-Object -ExpandProperty BaseName -Unique | Where-Object { $_ -like ('*' + $query + '*') } | Sort-Object;" ^
    "if (-not $matchedNodes) { Write-Error ('No ' + $mode + ' files found matching ' + $query); exit 1 };" ^
    "$exact = $matchedNodes | Where-Object { $_ -eq $query };" ^
    "if ($exact) { $chosen = $exact[0] }" ^
    "elseif ($matchedNodes.Count -eq 1) { $chosen = $matchedNodes[0] }" ^
    "else {" ^
    "    Write-Host ('Multiple nodes match ''' + $query + ''':');" ^
    "    for ($i=0; $i -lt $matchedNodes.Count; $i++) { Write-Host ('  [' + ($i+1) + '] ' + $matchedNodes[$i]) };" ^
    "    $sel = Read-Host ('Select a node [1-' + $matchedNodes.Count + '] (q to quit)');" ^
    "    if ($sel -eq 'q' -or $sel -eq 'Q') { exit 0 };" ^
    "    $idx = [int]$sel - 1;" ^
    "    if ($idx -lt 0 -or $idx -ge $matchedNodes.Count) { Write-Error 'Invalid choice.'; exit 1 };" ^
    "    $chosen = $matchedNodes[$idx];" ^
    "};" ^
    "$latest = $files | Where-Object { $_.BaseName -eq $chosen } | Sort-Object LastWriteTime -Descending | Select-Object -First 1;" ^
    "if ($latest) { Write-Output $latest.FullName } else { exit 1 }"') do (
    set "TARGET_FILE=%%F"
)

if "%TARGET_FILE%"=="" (
    exit /b 1
)

if %PRINT_PATH_ONLY% equ 1 (
    echo %TARGET_FILE%
    exit /b 0
)

echo ==^> Latest %MODE%: %TARGET_FILE%
more "%TARGET_FILE%"
exit /b 0
