@echo off
setlocal
for /f %%G in ('powershell -NoProfile -Command "[guid]::NewGuid().ToString('N')"') do set "SESSION=%%G"
if not defined SESSION (
  echo Could not create a unique test-session identifier. No existing files changed.
  pause
  exit /b 1
)
set "QA=%LOCALAPPDATA%\Gateway-QA-%SESSION%"
mkdir "%QA%"
if errorlevel 1 (
  echo Could not create the fresh QA directory. No existing files changed.
  pause
  exit /b 1
)
> "%QA%\settings.json" echo {"core_disabled":true,"core_mount_disabled":true,"network_disabled":false,"prepare_mounted_files":false,"onboarded":false,"rpc_auth_mode":"auto","rpc_port":8332,"cache_blocks":true,"privacy_mode":true,"share_cache":false,"serve_data":true,"serve_gateway_data":false,"graph_index":false,"satline_enabled":false,"ord_enabled":true,"satline_use_peers":false,"satline_serve_published":false}
> "%QA%\ResumeTest.cmd" echo @echo off
>> "%QA%\ResumeTest.cmd" echo "%~dp0GatewayClient.exe" -data "%QA%" -setup
>> "%QA%\ResumeTest.cmd" echo if errorlevel 1 pause
echo Fresh Gateway Headers, Blocks and Inscriptions test. Other test directories remain untouched.
echo Data and a ResumeTest.cmd launcher: %QA%
echo The normal TestStandalone.cmd uses its own separate reusable QA directory.
echo Inscriptions, Bitcoin serving and private caching are enabled.
echo Related transaction locators, Transaction Index and Gateway peerhood stay locked.
"%~dp0GatewayClient.exe" -data "%QA%" -setup
if errorlevel 1 pause
endlocal
