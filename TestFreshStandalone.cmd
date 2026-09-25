@echo off
setlocal
for /f %%G in ('powershell -NoProfile -Command "[guid]::NewGuid().ToString('N')"') do set "SESSION=%%G"
if not defined SESSION (
  echo Could not create a unique test-session identifier. No existing files changed.
  pause
  exit /b 1
)
set "QA=%LOCALAPPDATA%\GatewayClient-054-QA-%SESSION%"
mkdir "%QA%"
if errorlevel 1 (
  echo Could not create the fresh QA directory. No existing files changed.
  pause
  exit /b 1
)
> "%QA%\settings.json" echo {"core_disabled":true,"core_mount_disabled":true,"network_disabled":false,"prepare_mounted_files":false,"onboarded":false,"rpc_auth_mode":"auto","rpc_port":8332,"cache_blocks":true,"privacy_mode":true,"share_cache":false,"serve_data":false,"graph_index":true,"satline_enabled":true,"ord_enabled":true,"satline_use_peers":false,"satline_serve_published":false}
> "%QA%\ResumeTest.cmd" echo @echo off
>> "%QA%\ResumeTest.cmd" echo "%~dp0GatewayClient.exe" -data "%QA%" -setup
>> "%QA%\ResumeTest.cmd" echo if errorlevel 1 pause
echo Fresh Gateway v0.5.4 test. Other test directories remain untouched.
echo Data and a ResumeTest.cmd launcher: %QA%
echo The normal TestStandalone.cmd uses its own separate reusable QA directory.
"%~dp0GatewayClient.exe" -data "%QA%" -setup
if errorlevel 1 pause
endlocal
