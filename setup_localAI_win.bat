@echo off
setlocal
chcp 65001 > nul

set "DISTRO_NAME=Ubuntu"

echo [INFO] Starting LocalAI environment setup
echo.

:: ==================================================================
:: STEP 1/3 : WSL/UBUNTU INSTALLATION
:: ==================================================================
echo [STEP 1/3] Attempting to install WSL distribution '%DISTRO_NAME%'
echo [INFO] If '%DISTRO_NAME%' is already installed this step will be skipped
echo [IMPORTANT] THIS STEP MAY REQUIRE ADMINISTRATOR PRIVILEGES
pause

wsl --install -d %DISTRO_NAME%

echo [INFO] Assuming installation was successful continuing

:: ==================================================================
:: STEP 2/3 : INSTALLING TOOLS IN WSL
:: ==================================================================
echo.
echo [STEP 2/3] Installing LocalAI and required tools in WSL

echo [INFO] Updating packages and installing 'curl'
wsl -d %DISTRO_NAME% -- bash -c "sudo apt-get update && sudo apt-get install -y curl"

echo [INFO] Running the official LocalAI installation script
set "INSTALL_COMMAND=curl -L https://localai.io/install.sh -o /tmp/install.sh && DOCKER_INSTALL="false" bash /tmp/install.sh"
wsl -d %DISTRO_NAME% -- bash -c "%INSTALL_COMMAND%"

if %ERRORLEVEL% neq 0 (
    echo [ERROR] LocalAI installation failed
    pause
    exit /b 1
)

echo [INFO] Disabling background service for manual control
wsl -d %DISTRO_NAME% -u root -- bash -c "systemctl stop local-ai.service && systemctl disable local-ai.service" >nul 2>nul

:: ==================================================================
:: STEP 3/3 : CREATING FOLDERS AND SHORTCUT
:: ==================================================================
echo.
echo [STEP 3/3] Creating folder structure and access shortcut

for /f %%i in ('wsl -d %DISTRO_NAME% -- whoami') do set "WSL_USER=%%i"

echo [INFO] Creating 'models' folder in the WSL environment
wsl -d %DISTRO_NAME% -- bash -c "mkdir -p '/home/%WSL_USER%/models'"

set "TARGET_FOLDER=\\wsl$\%DISTRO_NAME%\home\%WSL_USER%\models"
set "SHORTCUT_PATH=%~dp0_Access_Models_Folder.lnk"

echo [INFO] Creating a shortcut for easy access to the models folder
:: This new method creates a shortcut that opens the folder path with explorer.exe
:: It is more reliable than pointing directly to a WSL path
powershell -Command "$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut('%SHORTCUT_PATH%'); $s.TargetPath = 'explorer.exe'; $s.Arguments = '%TARGET_FOLDER%'; $s.Save()"

echo.
echo [SUCCESS] Installation finished
echo [IMPORTANT] A shortcut '_Access_Models_Folder' has been created in this directory
echo [IMPORTANT] Use this shortcut to add and manage your model files
echo [IMPORTANT] You can now run 'run_localAI.bat'
echo.
pause