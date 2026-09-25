@echo off
setlocal
set "QA=%LOCALAPPDATA%\GatewayClient-054-QA"
if not exist "%QA%" mkdir "%QA%"
if errorlevel 1 (
  echo Could not create the separate QA directory.
  pause
  exit /b 1
)
if not exist "%QA%\settings.json" (
  > "%QA%\settings.json" echo {"core_disabled":true,"core_mount_disabled":true,"network_disabled":false,"prepare_mounted_files":false,"onboarded":false,"rpc_auth_mode":"auto","rpc_port":8332,"cache_blocks":true,"privacy_mode":true,"share_cache":false,"serve_data":false,"graph_index":true,"satline_enabled":true,"ord_enabled":true,"satline_use_peers":false,"satline_serve_published":false}
)
echo Gateway Client v0.5.4 testing candidate
echo Dedicated data: %QA%
echo This resumes the SAME discovery ledger on every normal restart.
echo TestFreshStandalone.cmd starts another isolated session without deleting this one.
echo Existing QA settings are retained. Your ordinary Gateway profile is not used.
echo No Windows or browser integration is changed by this launcher.
"%~dp0GatewayClient.exe" -data "%QA%" -setup
if errorlevel 1 pause
endlocal
