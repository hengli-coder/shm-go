@echo off
set PATH=C:\Program Files\Go\bin;%PATH%
cd /d d:\go\shm-go
echo === RUNNING TESTS ===
go test ./pkg/shmcache/ -v -count=1
echo === EXIT CODE: %ERRORLEVEL% ===
pause
