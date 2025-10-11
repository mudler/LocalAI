@echo off
setlocal
chcp 65001 > nul

set "DISTRO_NAME=Ubuntu"
set "PORT=8080"

echo [INFO] Starting LocalAI server via WSL
echo.

:: ==================================================================
:: STEP 1/2 : PREPARING LAUNCH
:: ==================================================================
echo [STEP 1/2] Preparing to launch

:: Get WSL username to build the path
for /f %%i in ('wsl -d %DISTRO_NAME% -- whoami') do set "WSL_USER=%%i"

set "MODELS_DIR_WSL_NATIVE=/home/%WSL_USER%/models"

echo [INFO] Models will be loaded from the WSL folder: %MODELS_DIR_WSL_NATIVE%
echo [INFO] To manage your models use the '_Access_Models_Folder' shortcut
echo.

:: Get WSL IP address for connection
for /f "tokens=2 delims=/ " %%i in ('wsl -d %DISTRO_NAME% -- ip addr show eth0 ^| findstr /r /c:"^ *inet "') do (
    set "WSL_IP=%%i"
)

echo [IMPORTANT] Once started the server will be accessible at this address
echo.
echo           http://%WSL_IP%:%PORT%
echo.
echo [INFO] To stop the server press CTRL + C in this window
echo.
pause

:: ==================================================================
:: STEP 2/2 : LAUNCHING THE SERVER
:: ==================================================================
echo [STEP 2/2] Launching the LocalAI server

set "CMD_TO_RUN=local-ai --models-path "%MODELS_DIR_WSL_NATIVE%" --address 0.0.0.0:%PORT% --context-size 700"

wsl -d %DISTRO_NAME% -- bash -c "%CMD_TO_RUN%"

echo.
echo [INFO] The LocalAI server has been stopped
pause
exit /b 0
